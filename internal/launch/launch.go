// Package launch runs on the host: it turns a project's policy into srt
// settings, starts what the project needs, and runs the command under srt.
package launch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/sandbox"
)

// repoSearchDepth bounds how deep ProtectRepos looks for nested repos. It
// reaches a worktree inside a repo inside a workspace: ws/repo/worktrees/wt.
const repoSearchDepth = 5

// Build returns the settings srt will receive for project, and the meta keys
// srtbox acts on. It does not start anything.
func Build(project string) (config.Meta, map[string]any, error) {
	doc, err := config.Load(project)
	if err != nil {
		return config.Meta{}, nil, err
	}
	doc = config.Expand(doc, os.LookupEnv).(map[string]any)
	meta, settings := config.Split(doc)

	if meta.Root != "" {
		if paths := config.ProtectRepos(meta.Root, repoSearchDepth, nil); len(paths) > 0 {
			config.Append(settings, []string{"filesystem", "denyWrite"}, paths...)
		}
	}

	// The login ssh-agent is never reachable: its variable is withheld, and
	// its socket is masked, since the path alone is enough to use it.
	deny := config.DenyEnv(meta.DenyEnv, os.Environ())
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		deny = append(deny, "SSH_AUTH_SOCK")
		if _, err := os.Stat(sock); err == nil {
			config.Append(settings, []string{"filesystem", "denyRead"}, sock)
		}
	}
	if len(deny) > 0 {
		config.AddEnvDeny(settings, deny...)
	}

	// srtbox runs again inside the sandbox as `srtbox init`, so its own binary
	// has to be readable there.
	if exe, err := self(); err == nil {
		config.Append(settings, []string{"filesystem", "allowRead"}, exe)
	}
	return meta, settings, nil
}

// Main launches a command: srtbox run [-p project] [flags] [--] cmd...
// Without -p, the project is the one whose _root contains the working
// directory.
func Main(args []string) int {
	opts, cmdArgs, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 2
	}
	project, err := resolveProject(opts.project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	meta, settings, err := Build(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	if meta.Root != "" {
		if cwd, _ := os.Getwd(); !config.Within(cwd, meta.Root) {
			fmt.Fprintf(os.Stderr, "srtbox: warning: cwd is outside %s; the sandbox grants that tree, not this one\n", meta.Root)
		}
	}
	for _, d := range meta.Mkdir {
		os.MkdirAll(d, 0o700)
	}

	env := append(os.Environ(),
		"SRTBOX_PROJECT="+project,
		"SRTBOX_ROOT="+meta.Root,
		"SRTBOX_FORWARD="+jsonString(meta.Forward),
	)

	var cleanup []func()
	defer func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}()

	if opts.sshHost != "" {
		s, err := setupSSH(opts.sshHost, opts.sshKey)
		if s != nil {
			cleanup = append(cleanup, s.cleanup)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "srtbox:", err)
			return 1
		}
		config.Append(settings, []string{"network", "allowedDomains"}, s.allow...)
		env = append(env, s.env...)
	}

	if len(meta.Broker) > 0 {
		runBroker(meta.Broker)
	}

	f, err := os.CreateTemp("", "srtbox-"+project+"-*.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	cleanup = append(cleanup, func() { os.Remove(f.Name()) })
	json.NewEncoder(f).Encode(settings)
	f.Close()

	srt := os.Getenv("SRTBOX_SRT")
	if srt == "" {
		if srt, err = exec.LookPath("srt"); err != nil {
			fmt.Fprintln(os.Stderr, "srtbox: srt not found on PATH (set SRTBOX_SRT to its path)")
			return 127
		}
	}
	exe, err := self()
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}

	cmd := exec.Command(srt, append([]string{"--settings", f.Name(), "--", exe, "init", "--"}, cmdArgs...)...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// The terminal delivers SIGINT and SIGQUIT to srt as well as to srtbox, so
	// those are only caught here — srtbox must outlive srt to clean up. TERM
	// and HUP arrive at srtbox alone, so they are passed on.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 127
	}
	go func() {
		for s := range sigs {
			if s == syscall.SIGTERM || s == syscall.SIGHUP {
				cmd.Process.Signal(s)
			}
		}
	}()
	cmd.Wait()
	return sandbox.ExitCode(cmd.ProcessState)
}

// Show prints the settings srt would receive for project, or for the
// working directory's project when project is empty.
func Show(project string) int {
	project, err := resolveProject(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	_, settings, err := Build(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(settings)
	return 0
}

// List prints the configured projects.
func List() int {
	names, err := config.Projects()
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	for _, n := range names {
		fmt.Println(n)
	}
	return 0
}

type options struct{ project, sshHost, sshKey string }

func parseArgs(args []string) (options, []string, error) {
	var o options
	rest := args
	for len(rest) > 0 {
		switch rest[0] {
		case "-p", "--project", "--ssh", "--key":
			if len(rest) < 2 {
				return o, nil, fmt.Errorf("%s needs a value", rest[0])
			}
			switch rest[0] {
			case "-p", "--project":
				o.project = rest[1]
			case "--ssh":
				o.sshHost = rest[1]
			default:
				o.sshKey = config.Home(rest[1])
			}
			rest = rest[2:]
			continue
		case "--":
			rest = rest[1:]
		}
		break
	}
	if len(rest) == 0 {
		return o, nil, errors.New("no command given")
	}
	return o, rest, nil
}

func resolveProject(project string) (string, error) {
	if project != "" {
		return project, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return config.ProjectFor(cwd)
}

// runBroker runs the project's broker command and waits for it. The command
// owns everything about its broker — whether one is already running, its
// socket, its idle timeout — and returns once the broker is ready.
func runBroker(argv []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	argv[0] = config.Home(argv[0])
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	if err := c.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "srtbox: warning: broker %q: %v\n", strings.Join(argv, " "), err)
	}
}

func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
