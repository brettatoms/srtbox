// Package launch runs on the host: it turns a project's policy into srt
// settings, starts what the project needs, and runs the command under srt.
package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/policy"
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
	deny := config.DenyEnv(meta.DenyEnv, meta.AllowEnv, os.Environ())
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		deny = append(deny, "SSH_AUTH_SOCK")
		if _, err := os.Stat(sock); err == nil {
			config.Append(settings, []string{"filesystem", "denyRead"}, sock)
		}
	}
	if len(deny) > 0 {
		config.AddEnvDeny(settings, deny...)
	}

	// srt refuses settings that lack any of these lists, so a config can leave
	// out the ones it has nothing to put in.
	for _, k := range [][2]string{
		{"network", "allowedDomains"}, {"network", "deniedDomains"},
		{"filesystem", "denyRead"}, {"filesystem", "allowWrite"}, {"filesystem", "denyWrite"},
	} {
		config.Append(settings, k[:])
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

	if len(opts.ssh) > 0 {
		s, err := setupSSH(opts.ssh)
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

	lb := scopeLoopback(meta.Forward, meta.Root, settings)

	if len(meta.Inject) > 0 {
		ienv, err := inject(meta.Inject, settings)
		if err != nil {
			fmt.Fprintln(os.Stderr, "srtbox:", err)
			return 1
		}
		env = append(env, ienv...)
	}

	// The session directory is private to this launch and readable inside:
	// it holds the policy for `srtbox why` and the broker's socket and links.
	sess, err := os.MkdirTemp("", "srtbox-session-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	cleanup = append(cleanup, func() { os.RemoveAll(sess) })
	config.Append(settings, []string{"filesystem", "allowRead"}, sess)

	if len(meta.Broker) > 0 {
		benv, stop, err := startBroker(project, meta, settings, sess)
		if err != nil {
			fmt.Fprintln(os.Stderr, "srtbox:", err)
			return 1
		}
		cleanup = append(cleanup, stop)
		env = append(env, benv...)
	}

	pol, err := policy.New(project, meta, settings)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	polPath := filepath.Join(sess, "policy.json")
	if err := pol.Write(polPath); err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	env = append(env, policy.Env+"="+polPath)

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

	// When a forwarded port can move, srt reads allowlist updates from a pipe
	// it inherits as fd 3, and srtbox keeps the write end.
	srtArgs := []string{"--settings", f.Name()}
	var controlR, controlW *os.File
	if lb.follows() {
		if controlR, controlW, err = os.Pipe(); err != nil {
			fmt.Fprintln(os.Stderr, "srtbox:", err)
			return 1
		}
		cleanup = append(cleanup, func() { controlW.Close() })
		srtArgs = append(srtArgs, "--control-fd", "3")
	}
	cmd := exec.Command(srt, append(append(srtArgs, "--", exe, "init", "--"), cmdArgs...)...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if controlR != nil {
		cmd.ExtraFiles = []*os.File{controlR}
	}

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
	if controlR != nil {
		controlR.Close()
		stop := make(chan struct{})
		defer close(stop)
		go lb.follow(controlW, portfileInterval, stop)
	}
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
	meta, settings, err := Build(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 1
	}
	scopeLoopback(meta.Forward, meta.Root, settings)
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

type options struct {
	project string
	ssh     []sshTarget
}

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
				if slices.ContainsFunc(o.ssh, func(t sshTarget) bool { return t.host == rest[1] }) {
					return o, nil, fmt.Errorf("--ssh %s given twice", rest[1])
				}
				o.ssh = append(o.ssh, sshTarget{host: rest[1]})
			default:
				// --key names the key for the --ssh before it.
				if len(o.ssh) == 0 {
					return o, nil, errors.New("--key must follow the --ssh it is for")
				}
				o.ssh[len(o.ssh)-1].key = config.Home(rest[1])
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
