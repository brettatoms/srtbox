package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestProtectAgentConfig(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(home, ".claude")
	os.MkdirAll(filepath.Join(dir, "hooks"), 0o755)
	os.MkdirAll(filepath.Join(home, "repo", "skills"), 0o755)
	os.Symlink(filepath.Join(home, "repo", "skills"), filepath.Join(dir, "skills"))
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o644)
	settings := func(writable ...any) map[string]any {
		return map[string]any{"filesystem": map[string]any{
			"allowWrite": writable,
			"denyWrite":  []any{filepath.Join(dir, "rules")},
		}}
	}
	at := func(n string) string { return filepath.Join(dir, n) }

	if c := ProtectAgentConfig(dir, settings(filepath.Join(home, "other")), "linux"); len(c.Deny) > 0 {
		t.Errorf("protected a directory the sandbox cannot write: %+v", c)
	}
	if c := ProtectAgentConfig(at("missing"), settings(home), "linux"); len(c.Deny)+len(c.Mkdir) > 0 {
		t.Errorf("protected a config directory that does not exist: %+v", c)
	}

	linux := ProtectAgentConfig(dir, settings(dir, at("commands")), "linux")
	for _, p := range []string{at("hooks"), at("skills"), at("settings.json"), at("agents")} {
		if !slices.Contains(linux.Deny, p) {
			t.Errorf("Linux: %s not denied: %v", p, linux.Deny)
		}
	}
	for _, p := range []string{at("commands"), at("rules"), at("loop.md")} {
		if slices.Contains(linux.Deny, p) {
			t.Errorf("Linux: %s denied, though granted, already denied, or a missing file: %v", p, linux.Deny)
		}
	}
	if !slices.Contains(linux.Mkdir, at("agents")) || slices.Contains(linux.Mkdir, at("hooks")) {
		t.Errorf("Linux mkdir: %v", linux.Mkdir)
	}
	if !slices.Contains(linux.Missing, at("loop.md")) || slices.Contains(linux.Missing, at("settings.json")) {
		t.Errorf("Linux missing: %v", linux.Missing)
	}
	if !slices.Equal(linux.Links, []string{at("skills")}) {
		t.Errorf("Linux links: %v", linux.Links)
	}

	mac := ProtectAgentConfig(dir, settings(home), "darwin")
	if !slices.Contains(mac.Deny, at("loop.md")) || !slices.Contains(mac.Deny, at("agents")) || len(mac.Mkdir)+len(mac.Missing) > 0 {
		t.Errorf("macOS denies missing paths as they are: %+v", mac)
	}
}
