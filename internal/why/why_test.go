package why

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/policy"
)

func TestKind(t *testing.T) {
	cases := map[string]string{
		"$FIGMA_TOKEN": "env", "FIGMA_TOKEN": "env", "/etc": "path", "~/.ssh": "path",
		"./x": "path", "github.com": "host", "github.com:22": "host", "https://a.b/c": "host",
		"localhost": "host", "127.0.0.1:3020": "host", "notes": "path",
	}
	for arg, want := range cases {
		if got := kind(arg); got != want {
			t.Errorf("kind(%q) = %s, want %s", arg, got, want)
		}
	}
}

func TestDomainMatch(t *testing.T) {
	cases := []struct {
		host    string
		port    int
		pattern string
		want    bool
	}{
		{"github.com", 443, "github.com", true},
		{"GitHub.com", 443, "github.com", true},
		{"api.github.com", 443, "*.github.com", true},
		{"github.com", 443, "*.github.com", false},
		{"github.com", 22, "github.com:22", true},
		{"github.com", 443, "github.com:22", false},
		{"127.0.0.1", 3020, "127.0.0.1:3020", true},
		{"127.0.0.1", 3021, "127.0.0.1:3020", false},
		{"1.2.3.4", 443, "*.4", false},
		{"anything", 1, "*", true},
		{"::1", 80, "[::1]", true},
	}
	for _, c := range cases {
		if got := config.DomainMatch(c.host, c.port, c.pattern); got != c.want {
			t.Errorf("domainMatch(%s, %d, %s) = %v", c.host, c.port, c.pattern, got)
		}
	}
}

func TestSplitTarget(t *testing.T) {
	cases := map[string]string{
		"github.com": "github.com 443", "github.com:22": "github.com 22",
		"http://a.b/x": "a.b 80", "ssh://git@a.b": "a.b 22", "https://a.b:8443": "a.b 8443",
	}
	for arg, want := range cases {
		h, p, err := splitTarget(arg)
		if got := h + " " + strconv.Itoa(p); err != nil || got != want {
			t.Errorf("splitTarget(%q) = %s, %v", arg, got, err)
		}
	}
}

func TestCoversAndShadowing(t *testing.T) {
	for _, c := range []struct {
		rule, path string
		want       bool
	}{
		{"/a/b", "/a/b", true}, {"/a/b", "/a/b/c", true}, {"/a/b", "/a/bc", false},
		{"/", "/x", true}, {"/a/*/c", "/a/b/c/d", true}, {"/a/*/c", "/a/b/d", false},
	} {
		if got := covers(c.rule, c.path); got != c.want {
			t.Errorf("covers(%s, %s) = %v", c.rule, c.path, got)
		}
	}
	pol := &policy.Policy{Settings: map[string]any{"filesystem": map[string]any{
		"denyRead": []any{"/home/u"}, "allowRead": []any{"/home/u/.aws", "/srv"},
		"allowWrite": []any{"/home/u/.aws/cache", "/tmp", "/srv/cache"},
	}}}
	if shadowedBy(pol, "/home/u/.aws/cache") != "/home/u/.aws" {
		t.Error("write grant inside a read grant not detected")
	}
	if shadowedBy(pol, "/tmp") != "" || shadowedBy(pol, "/srv/cache") != "" {
		t.Error("blamed a read grant that re-opens nothing")
	}
}

func TestSrtProtects(t *testing.T) {
	for p, want := range map[string]bool{
		"/r/.bashrc": true, "/r/.git/hooks/pre-commit": true, "/r/.git/config": true,
		"/r/.claude/commands/x.md": true, "/r/.claude/settings.json": false, "/r/src/a.go": false,
	} {
		if got := srtProtects(p); got != want {
			t.Errorf("srtProtects(%s) = %v", p, got)
		}
	}
}

func TestPathAndEnvExplanations(t *testing.T) {
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	os.Mkdir(ro, 0o555)
	pol := &policy.Policy{OS: "linux", DenyEnv: []string{"*TOKEN*"}, Settings: map[string]any{
		"filesystem":  map[string]any{"allowWrite": []any{dir}, "denyWrite": []any{ro}},
		"credentials": map[string]any{"envVars": []any{map[string]any{"name": "GH_TOKEN", "mode": "deny"}}},
	}, Sources: map[string]map[string]string{"filesystem.allowWrite": {dir: "p.json"}}}

	var b strings.Builder
	Path(&b, pol, dir)
	Path(&b, pol, filepath.Join(dir, "missing", "x"))
	Env(&b, pol, "GH_TOKEN")
	Env(&b, pol, "SRTBOX_WHY_UNSET")
	out := b.String()
	for _, want := range []string{
		`write: yes       allowWrite "` + dir + `" (p.json)`,
		"its directory does not exist here",
		`matches _denyEnv "*TOKEN*"`,
		"not set when the session started",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
