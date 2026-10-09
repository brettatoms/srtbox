package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/brettatoms/srtbox/internal/broker"
	"github.com/brettatoms/srtbox/internal/config"
)

// MinSrt is the oldest srt srtbox works with. From 0.0.76 srt exits after the
// command when --control-fd is a pipe, which following a portfile relies on.
const MinSrt = "0.0.76"

// apparmorSysctl is 1 on Ubuntu 23.10 and later, where a program without an
// AppArmor profile, such as a bubblewrap from nix, cannot create the user
// namespaces srt needs.
const apparmorSysctl = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"

// probeTimeout bounds the trial session doctor starts.
const probeTimeout = time.Minute

const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
	statusSkip = "skip"
)

type finding struct{ status, label, msg string }

// Doctor checks that a project's sessions can start: srtbox doctor [-p project].
func Doctor(args []string) int {
	opts, rest, err := parseOptions(args)
	if err == nil && (len(rest) > 0 || len(opts.ssh) > 0) {
		err = errors.New("usage: srtbox doctor [-p <project>]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 2
	}
	var fs []finding
	defer func() { report(fs) }()

	project, err := resolveProject(opts.project)
	if err != nil {
		fs = append(fs, finding{statusFail, "project", err.Error()})
		return 1
	}
	layers, err := config.Layers(project)
	var meta config.Meta
	if err == nil {
		meta, _, err = Build(project)
	}
	if err != nil {
		fs = append(fs, finding{statusFail, "config " + project, err.Error()})
		return 1
	}
	var names []string
	for _, l := range layers {
		names = append(names, l.Name)
	}
	fs = append(fs, finding{statusOK, "config " + project, strings.Join(names, ", ")})

	srt := srtFinding(srtVersion())
	fs = append(fs, srt)
	if programs, err := broker.Parse(meta.Broker); err != nil {
		fs = append(fs, finding{statusFail, "broker", err.Error()})
	} else {
		fs = append(fs, brokerFindings(programs)...)
	}
	fs = append(fs, injectFindings(meta.Inject)...)

	if srt.status == statusOK {
		fs = append(fs, probe(project))
	}
	for _, f := range fs {
		if f.status == statusFail {
			return 1
		}
	}
	return 0
}

func report(fs []finding) {
	glyph := map[string]string{statusOK: "✓", statusWarn: "⚠", statusFail: "✗", statusSkip: "–"}
	for _, f := range fs {
		line := "  " + glyph[f.status] + " " + f.label
		if f.msg != "" {
			line += " — " + f.msg
		}
		fmt.Println(line)
	}
}

// srtVersion is the version the srt that srtbox would run reports, or "".
func srtVersion() string {
	srt := os.Getenv("SRTBOX_SRT")
	if srt == "" {
		var err error
		if srt, err = exec.LookPath("srt"); err != nil {
			return ""
		}
	}
	out, err := exec.Command(srt, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func srtFinding(have string) finding {
	switch {
	case have == "":
		return finding{statusFail, "srt", "not found on PATH (or set SRTBOX_SRT)"}
	case !config.VersionAtLeast(have, MinSrt):
		return finding{statusFail, "srt " + have, "srtbox needs " + MinSrt + " or later"}
	}
	return finding{statusOK, "srt " + have, ""}
}

// brokerFindings checks that each brokered program and its check can run.
func brokerFindings(programs map[string]*broker.Program) []finding {
	var names []string
	for n := range programs {
		names = append(names, n)
	}
	sort.Strings(names)
	var fs []finding
	for _, n := range names {
		p := programs[n]
		f := finding{statusOK, "broker " + n, ""}
		for _, path := range []string{p.Path, p.Check} {
			if path == "" {
				continue
			}
			if st, err := os.Stat(path); err != nil || st.Mode()&0o111 == 0 {
				f = finding{statusFail, "broker " + n, path + " is not executable"}
				break
			}
		}
		fs = append(fs, f)
	}
	return fs
}

// injectFindings runs each _inject source and reports whether it printed a
// value, never the value itself.
func injectFindings(raw map[string]any) []finding {
	var names []string
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	var fs []finding
	for _, n := range names {
		spec, _ := raw[n].(map[string]any)
		argv := config.Command(spec["from"])
		if len(argv) == 0 {
			fs = append(fs, finding{statusFail, "inject " + n, "no from command"})
			continue
		}
		optional, _ := spec["optional"].(bool)
		if _, err := fetch(argv, optional); err != nil {
			if optional {
				fs = append(fs, finding{statusSkip, "inject " + n, "not set; optional"})
			} else {
				fs = append(fs, finding{statusWarn, "inject " + n, err.Error() + "; sessions go without it"})
			}
			continue
		}
		fs = append(fs, finding{statusOK, "inject " + n, ""})
	}
	return fs
}

// probe starts a session that runs `true`. It is the one check that shows
// srt can build the sandbox on this machine.
func probe(project string) finding {
	exe, err := self()
	if err != nil {
		return finding{statusFail, "sandbox starts", err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "run", "-p", project, "--", "true").CombinedOutput()
	if err == nil {
		return finding{statusOK, "sandbox starts", ""}
	}
	msg := lastLines(string(out), 3)
	if runtime.GOOS == "linux" {
		if b, rerr := os.ReadFile(apparmorSysctl); rerr == nil {
			if hint := apparmorHint(string(b)); hint != "" {
				msg += "\n    " + hint
			}
		}
	}
	return finding{statusFail, "sandbox starts", msg}
}

func apparmorHint(sysctl string) string {
	if strings.TrimSpace(sysctl) != "1" {
		return ""
	}
	return "AppArmor restricts unprivileged user namespaces (" + apparmorSysctl + " = 1); " +
		"give srt's bwrap an AppArmor profile that allows userns"
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n    ")
}
