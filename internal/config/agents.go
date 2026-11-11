package config

import (
	"os"
	"path/filepath"
	"slices"
)

// Claude Code loads or runs these entries of its config directory outside the
// sandbox. They follow the paths Claude Code's own sandbox protects, less the
// ones it writes while it runs, such as projects, jobs and daemon.
var (
	claudeDirs = []string{
		"agents", "commands", "cowork_plugins", "dev-mods", "hooks", "local", "output-styles",
		"plugins", "routines", "rules", "seed-admin", "skills", "workflows",
	}
	claudeFiles = []string{
		"CLAUDE.md", "daemon.json", "launch.json", "loop.md", "scheduled_tasks.json", "settings.json",
	}
)

// ClaudeConfigDir returns Claude Code's config directory.
func ClaudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Clean(Home(d))
	}
	return Home("~/.claude")
}

// AgentConfig is what the sandbox has to protect in an agent's config
// directory.
type AgentConfig struct {
	Deny    []string // paths to deny writes to
	Mkdir   []string // missing directories to create first, so they can be denied
	Missing []string // missing files left writable: on Linux srt can only deny one by creating it
	Links   []string // denied paths that are symbolic links: on Linux srt protects only the target
}

// ProtectAgentConfig returns what to protect in Claude Code's config
// directory dir when it exists and an allowWrite entry makes it writable. A
// path that an allowWrite entry names, or lies inside, stays writable, as
// does one that a denyWrite entry already covers. goos decides how a missing
// path is handled: macOS denies it as it is, and on Linux srt would create an
// empty file in its place on the host for the length of the session.
func ProtectAgentConfig(dir string, settings map[string]any, goos string) AgentConfig {
	var c AgentConfig
	writable, readOnly := writeRules(settings)
	grants, denies := forms(writable), forms(readOnly)
	if _, err := os.Stat(dir); err != nil {
		return c
	}
	dirs := forms([]string{dir})
	if !slices.ContainsFunc(dirs, func(d string) bool {
		return slices.ContainsFunc(grants, func(w string) bool { return under(d, w) })
	}) {
		return c
	}
	add := func(name string, isDir bool) {
		p := filepath.Join(dir, name)
		if slices.ContainsFunc(grants, func(w string) bool { return under(w, p) }) ||
			slices.ContainsFunc(denies, func(d string) bool { return under(p, d) }) {
			return
		}
		fi, err := os.Lstat(p)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			c.Links = append(c.Links, p)
		case err != nil && goos == "linux" && isDir:
			c.Mkdir = append(c.Mkdir, p)
		case err != nil && goos == "linux":
			c.Missing = append(c.Missing, p)
			return
		}
		c.Deny = append(c.Deny, p)
	}
	for _, n := range claudeDirs {
		add(n, true)
	}
	for _, n := range claudeFiles {
		add(n, false)
	}
	return c
}
