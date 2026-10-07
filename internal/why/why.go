// Package why explains, from inside a session, why a path, host or variable is
// or is not reachable. It probes the access, then names the rule responsible
// and the file it came from.
package why

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/netproxy"
	"github.com/brettatoms/srtbox/internal/policy"
)

const usage = `usage: srtbox why [-p <project>] <target>...

A target is a path, a host (example.com, example.com:22, https://example.com),
or a variable name ($NAME, or NAME in capitals).`

// Main runs inside a session, where the policy file is readable.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	pol, err := policy.Load(os.Getenv(policy.Env))
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox why: cannot read this session's policy:", err)
		return 1
	}
	for i, a := range args {
		if i > 0 {
			fmt.Println()
		}
		switch kind(a) {
		case "env":
			Env(os.Stdout, pol, strings.TrimPrefix(a, "$"))
		case "host":
			Host(os.Stdout, pol, a)
		default:
			Path(os.Stdout, pol, a)
		}
	}
	return 0
}

var (
	envName  = regexp.MustCompile(`^\$?[A-Za-z_][A-Za-z0-9_]*$`)
	capsName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	hostName = regexp.MustCompile(`^(\[[0-9a-fA-F:]+\]|[A-Za-z0-9.-]+)(:[0-9]+)?$`)
)

func kind(a string) string {
	switch {
	case strings.HasPrefix(a, "$") && envName.MatchString(a):
		return "env"
	case strings.Contains(a, "://"):
		return "host"
	case strings.HasPrefix(a, "/"), strings.HasPrefix(a, "~"), strings.HasPrefix(a, "."):
		return "path"
	}
	if _, err := os.Lstat(a); err == nil {
		return "path"
	}
	if capsName.MatchString(a) {
		return "env"
	}
	if hostName.MatchString(a) && (strings.Contains(a, ".") || strings.Contains(a, ":") || a == "localhost") {
		return "host"
	}
	return "path"
}

// Path explains read and write access to a path.
func Path(w io.Writer, pol *policy.Policy, arg string) {
	p := config.Home(arg)
	if !filepath.IsAbs(p) {
		cwd, _ := os.Getwd()
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	fmt.Fprintln(w, p)

	denyR, allowR := deepest(pol, "denyRead", p), deepest(pol, "allowRead", p)
	allowW, denyW := deepest(pol, "allowWrite", p), deepest(pol, "denyWrite", p)
	masked := denyR != "" && allowR == ""

	info, statErr := os.Lstat(p)
	exists := statErr == nil

	// Read.
	readErr := probeRead(p, info, statErr)
	switch {
	case !exists && masked:
		line(w, "read", "no", "hidden by "+rule(pol, "denyRead", denyR)+"; inside, it looks missing")
	case !exists:
		line(w, "read", "no", "does not exist")
	case readErr == nil && allowR != "" && denyR != "":
		line(w, "read", "yes", rule(pol, "allowRead", allowR)+" re-opens "+rule(pol, "denyRead", denyR))
	case readErr == nil:
		line(w, "read", "yes", "no rule denies it")
	case masked:
		line(w, "read", "no", rule(pol, "denyRead", denyR))
	default:
		line(w, "read", "no", readErr.Error())
	}

	// Write.
	if _, err := os.Stat(filepath.Dir(p)); !exists && err != nil {
		why := "its directory does not exist here"
		if masked {
			why += ", hidden by " + rule(pol, "denyRead", denyR)
		}
		line(w, "write", "no", why)
		return
	}
	writeErr := probeWrite(p, info, exists)
	switch {
	case writeErr == nil && masked && pol.OS == "linux":
		line(w, "write", "discarded", rule(pol, "denyRead", denyR)+" masks it with a scratch filesystem: writes succeed, then vanish when the session ends")
	case writeErr == nil && allowW != "":
		line(w, "write", "yes", rule(pol, "allowWrite", allowW))
	case writeErr == nil:
		line(w, "write", "yes", "no rule denies it")
	case denyW != "":
		why := rule(pol, "denyWrite", denyW)
		if pol.Source("filesystem.denyWrite", denyW) == policy.SourceSrtbox {
			why += ": srtbox protects nested repos' git hooks and config, and tool config that runs code on the host"
		}
		line(w, "write", "no", why)
	case allowW == "":
		line(w, "write", "no", "no allowWrite rule covers it")
	case shadowedBy(pol, allowW) != "":
		r := shadowedBy(pol, allowW)
		line(w, "write", "no", rule(pol, "allowWrite", allowW)+" sits inside "+rule(pol, "allowRead", r)+
			", whose read-only grant covers it; grant writes on a path outside every allowRead entry")
	case srtProtects(p):
		line(w, "write", "no", "srt protects this name itself (shell rc files, .gitconfig, .gitmodules, .mcp.json, .vscode, .idea, .claude/commands and agents, .git/hooks and .git/config)")
	default:
		line(w, "write", "no", rule(pol, "allowWrite", allowW)+" covers it, but the write failed: "+writeErr.Error())
	}
}

func probeRead(p string, info os.FileInfo, statErr error) error {
	if statErr != nil {
		return statErr
	}
	if info.IsDir() {
		_, err := os.ReadDir(p)
		return err
	}
	f, err := os.Open(p)
	if err == nil {
		f.Close()
	}
	return err
}

// probeWrite tests write access without changing anything that exists: an
// existing file is opened for append and closed, a directory gets a scratch
// file that is removed at once, and a missing path is tested by its parent.
func probeWrite(p string, info os.FileInfo, exists bool) error {
	dir := p
	if exists && !info.IsDir() {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			f.Close()
		}
		return err
	}
	if !exists {
		dir = filepath.Dir(p)
	}
	f, err := os.CreateTemp(dir, ".srtbox-why-*")
	if err != nil {
		return err
	}
	f.Close()
	os.Remove(f.Name())
	return nil
}

// deepest returns the longest entry of filesystem.<key> that covers p.
func deepest(pol *policy.Policy, key, p string) string {
	best := ""
	for _, e := range policy.Strings(pol.Settings, "filesystem", key) {
		if covers(config.Home(e), p) && len(e) > len(best) {
			best = e
		}
	}
	return best
}

// covers reports whether rule path r applies to p: p is r or inside it. A
// rule with glob characters matches p or one of its parents.
func covers(r, p string) bool {
	if strings.ContainsAny(r, "*?[") {
		for q := p; ; q = filepath.Dir(q) {
			if ok, _ := filepath.Match(r, q); ok {
				return true
			}
			if q == "/" || q == "." {
				return false
			}
		}
	}
	r = filepath.Clean(r)
	return p == r || r == "/" || strings.HasPrefix(p, r+"/")
}

// shadowedBy returns an allowRead entry that re-opens a denyRead path and
// strictly contains the allowWrite entry w. srt binds such a path read-only
// after the write grants, so that bind covers w.
func shadowedBy(pol *policy.Policy, w string) string {
	wp := filepath.Clean(config.Home(w))
	for _, r := range policy.Strings(pol.Settings, "filesystem", "allowRead") {
		rp := filepath.Clean(config.Home(r))
		if wp != rp && covers(rp, wp) && deepest(pol, "denyRead", rp) != "" {
			return r
		}
	}
	return ""
}

var (
	srtFiles = []string{".gitconfig", ".gitmodules", ".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile", ".ripgreprc", ".mcp.json"}
	srtDirs  = []string{"/.vscode", "/.idea", "/.claude/commands", "/.claude/agents", "/.git/hooks", "/.git/config"}
)

// srtProtects reports whether p is one of the names srt write-protects on
// its own.
func srtProtects(p string) bool {
	if slices.Contains(srtFiles, filepath.Base(p)) {
		return true
	}
	for _, d := range srtDirs {
		if strings.HasSuffix(p, d) || strings.Contains(p, d+"/") {
			return true
		}
	}
	return false
}

// Host explains whether host[:port] is reachable through srt's proxy.
func Host(w io.Writer, pol *policy.Policy, arg string) {
	host, port, err := splitTarget(arg)
	if err != nil {
		fmt.Fprintf(w, "%s\n", arg)
		line(w, "reach", "?", err.Error())
		return
	}
	fmt.Fprintln(w, net.JoinHostPort(host, strconv.Itoa(port)))

	if loopback(host) {
		explainLoopback(w, pol, host, port)
		return
	}
	var allowed, denied string
	for _, d := range policy.Strings(pol.Settings, "network", "deniedDomains") {
		if config.DomainMatch(host, port, d) {
			denied = d
		}
	}
	for _, d := range policy.Strings(pol.Settings, "network", "allowedDomains") {
		if config.DomainMatch(host, port, d) && (allowed == "" || len(d) > len(allowed)) {
			allowed = d
		}
	}
	probe := dialProbe(host, port)
	switch {
	case denied != "":
		line(w, "reach", "no", rule(pol, "deniedDomains", denied))
	case allowed == "" && probe != nil:
		line(w, "reach", "no", "no allowedDomains entry matches it")
	case allowed == "":
		line(w, "reach", "yes", "reachable, though no allowedDomains entry matches it")
	case probe == nil:
		line(w, "reach", "yes", rule(pol, "allowedDomains", allowed))
	default:
		line(w, "reach", "no", rule(pol, "allowedDomains", allowed)+" allows it, but the connection failed: "+probe.Error())
	}
}

func explainLoopback(w io.Writer, pol *policy.Policy, host string, port int) {
	declared := slices.Contains(netproxy.ResolvePorts(pol.Forward, pol.Root), port)
	if pol.OS != "linux" {
		if declared {
			line(w, "reach", "direct", "the sandbox shares the host's loopback on this platform; port is in _forward")
		} else {
			line(w, "reach", "direct", "the sandbox shares the host's loopback on this platform, so _forward does not limit it")
		}
		return
	}
	if !declared {
		line(w, "reach", "no", "the host's loopback is reachable only on _forward ports, and this is not one")
		return
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		line(w, "reach", "no", "port is in _forward, but nothing on the host is serving it yet; srtbox relays it within a few seconds of the host starting it")
		return
	}
	c.Close()
	line(w, "reach", "yes", "relayed from the host: port is in _forward")
}

func loopback(h string) bool {
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}

func splitTarget(arg string) (string, int, error) {
	if strings.Contains(arg, "://") {
		u, err := url.Parse(arg)
		if err != nil {
			return "", 0, err
		}
		port := map[string]int{"http": 80, "https": 443, "ssh": 22, "git": 9418}[u.Scheme]
		if p := u.Port(); p != "" {
			port, _ = strconv.Atoi(p)
		}
		if port == 0 {
			return "", 0, fmt.Errorf("no port for scheme %q", u.Scheme)
		}
		return u.Hostname(), port, nil
	}
	h, p, err := net.SplitHostPort(arg)
	if err != nil {
		return strings.Trim(arg, "[]"), 443, nil
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("bad port %q", p)
	}
	return h, port, nil
}

func dialProbe(host string, port int) error {
	done := make(chan error, 1)
	go func() {
		c, err := netproxy.Dial(host, port)
		if err == nil {
			c.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		return errors.New("timed out")
	}
}

// Env explains whether a variable reaches the sandbox.
func Env(w io.Writer, pol *policy.Policy, name string) {
	fmt.Fprintln(w, "$"+name)
	if creds, ok := pol.Settings["credentials"].(map[string]any); ok {
		vars, _ := creds["envVars"].([]any)
		for _, v := range vars {
			if m, ok := v.(map[string]any); ok && m["name"] == name && m["mode"] == "mask" {
				var hosts []string
				for _, h := range m["injectHosts"].([]any) {
					hosts = append(hosts, fmt.Sprint(h))
				}
				line(w, "env", "masked", "the sandbox sees a placeholder; srt sends the real value only to "+strings.Join(hosts, ", "))
				return
			}
		}
	}
	if _, ok := os.LookupEnv(name); ok {
		line(w, "env", "set", "visible in the sandbox")
		return
	}
	withheld := false
	if creds, ok := pol.Settings["credentials"].(map[string]any); ok {
		vars, _ := creds["envVars"].([]any)
		for _, v := range vars {
			if m, ok := v.(map[string]any); ok && m["name"] == name {
				withheld = true
			}
		}
	}
	switch {
	case name == "SSH_AUTH_SOCK":
		line(w, "env", "withheld", "srtbox always withholds the login ssh-agent; run with --ssh <host> to reach one host")
	case withheld:
		for _, p := range pol.DenyEnv {
			if ok, _ := path.Match(strings.ToUpper(p), strings.ToUpper(name)); ok {
				line(w, "env", "withheld", fmt.Sprintf("matches _denyEnv %q; list it in _allowEnv to pass it through", p))
				return
			}
		}
		line(w, "env", "withheld", "listed in credentials.envVars")
	case slices.Contains(pol.AllowEnv, name):
		line(w, "env", "unset", "listed in _allowEnv, but it was not set when the session started")
	default:
		line(w, "env", "unset", "it was not set when the session started")
	}
}

func rule(pol *policy.Policy, key, value string) string {
	section := "filesystem." + key
	if strings.HasSuffix(key, "Domains") {
		section = "network." + key
	}
	return fmt.Sprintf("%s %q (%s)", key, value, pol.Source(section, value))
}

func line(w io.Writer, what, verdict, why string) {
	fmt.Fprintf(w, "  %-6s %-9s %s\n", what+":", verdict, why)
}
