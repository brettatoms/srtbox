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
	os.WriteFile(filepath.Join(conf, "p.json"), []byte(`{"filesystem":{"allowRead":["/shared","${SRTBOX_TEST_DIR}"]},
		"credentials":{"files":[{"path":"~/.token","mode":"mask"}]}}`), 0o600)
	settings := map[string]any{
		"filesystem": map[string]any{
			"denyRead": []any{"~"}, "allowRead": []any{"/shared", "/data", "/added"},
		},
		"credentials": map[string]any{"files": []any{
			map[string]any{"path": "~/.secret", "mode": "deny"},
			map[string]any{"path": "~/.token", "mode": "mask"},
		}},
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
		{"credentials.files", "~/.token", "p.json"},
	} {
		if got := p.Source(c.section, c.value); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.section, c.value, got, c.want)
		}
	}
}

func TestSourcesNameTheDefaultProject(t *testing.T) {
	t.Setenv("SRTBOX_CONFIG_DIR", t.TempDir())
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Chdir(dir)
	settings := map[string]any{"filesystem": map[string]any{"allowWrite": []any{dir}}}

	p, err := New(config.DefaultProject, config.Meta{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Source("filesystem.allowWrite", dir); got != SourceDefault {
		t.Errorf("got %q, want %q", got, SourceDefault)
	}
}

func TestSourcesNameIncludedFiles(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("SRTBOX_CONFIG_DIR", conf)
	os.MkdirAll(filepath.Join(conf, "include"), 0o700)
	os.WriteFile(filepath.Join(conf, "include", "team.json"), []byte(`{"network":{"allowedDomains":["team.example","both.example"]}}`), 0o600)
	os.WriteFile(filepath.Join(conf, "p.json"), []byte(`{"_include":["include/team.json"],"network":{"allowedDomains":["both.example"]}}`), 0o600)
	settings := map[string]any{"network": map[string]any{"allowedDomains": []any{"team.example", "both.example"}}}

	p, err := New("p", config.Meta{}, settings)
	if err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]string{"team.example": "include/team.json", "both.example": "p.json"} {
		if got := p.Source("network.allowedDomains", value); got != want {
			t.Errorf("%s: got %q, want %q", value, got, want)
		}
	}
}
