package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brettatoms/srtbox/internal/config"
)

func TestSourcesNameTheFileEachRuleCameFrom(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("SRTBOX_CONFIG_DIR", conf)
	t.Setenv("SRTBOX_TEST_DIR", "/data")
	os.WriteFile(filepath.Join(conf, "base.json"), []byte(`{"filesystem":{"denyRead":["~"],"allowRead":["/shared"]},
		"credentials":{"files":[{"path":"~/.secret","mode":"deny"}]}}`), 0o600)
	os.WriteFile(filepath.Join(conf, "p.json"), []byte(`{"filesystem":{"allowRead":["/shared","${SRTBOX_TEST_DIR}"]}}`), 0o600)
	settings := map[string]any{
		"filesystem": map[string]any{
			"denyRead": []any{"~"}, "allowRead": []any{"/shared", "/data", "/added"},
		},
		"credentials": map[string]any{"files": []any{map[string]any{"path": "~/.secret", "mode": "deny"}}},
	}

	p, err := New("p", config.Meta{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ section, value, want string }{
		{"filesystem.denyRead", "~", "base.json"},
		{"filesystem.allowRead", "/shared", "p.json"}, // in both: the project file wins
		{"filesystem.allowRead", "/data", "p.json"},   // matched after ${VAR} expansion
		{"filesystem.allowRead", "/added", SourceSrtbox},
		{"credentials.files", "~/.secret", "base.json"},
	} {
		if got := p.Source(c.section, c.value); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.section, c.value, got, c.want)
		}
	}
}
