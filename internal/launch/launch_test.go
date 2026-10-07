package launch

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args    []string
		project string
		host    string
		cmd     []string
		wantErr bool
	}{
		{[]string{"p", "claude"}, "p", "", []string{"claude"}, false},
		{[]string{"p", "--", "claude", "-x"}, "p", "", []string{"claude", "-x"}, false},
		{[]string{"p", "--ssh", "h", "--", "claude"}, "p", "h", []string{"claude"}, false},
		{[]string{"p", "--", "--ssh", "h"}, "p", "", []string{"--ssh", "h"}, false},
		{[]string{"p", "--ssh"}, "", "", nil, true},
		{[]string{"p"}, "", "", nil, true},
	}
	for _, c := range cases {
		project, o, cmd, err := parseArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err %v", c.args, err)
			continue
		}
		if !c.wantErr && (project != c.project || o.sshHost != c.host || !reflect.DeepEqual(cmd, c.cmd)) {
			t.Errorf("%v: got %q %q %q", c.args, project, o.sshHost, cmd)
		}
	}
}

func TestParseSSHConfig(t *testing.T) {
	cfg := parseSSHConfig([]byte("user root\nhostname 10.0.0.1\nport 2222\nidentityfile ~/.ssh/a\nidentityfile ~/.ssh/b\n"))
	if cfg["hostname"][0] != "10.0.0.1" || cfg["port"][0] != "2222" || cfg["user"][0] != "root" {
		t.Errorf("got %v", cfg)
	}
	if !reflect.DeepEqual(cfg["identityfile"], []string{"~/.ssh/a", "~/.ssh/b"}) {
		t.Errorf("identityfile: %v", cfg["identityfile"])
	}
}

func TestWithin(t *testing.T) {
	for path, want := range map[string]bool{"/a/b": true, "/a/b/c": true, "/a": false, "/a/bc": false} {
		if got := within(path, "/a/b"); got != want {
			t.Errorf("within(%q): %v", path, got)
		}
	}
}

// Build is the whole policy pipeline: merge, expansion, repo protection,
// withheld variables, and srtbox's own binary made readable.
func TestBuild(t *testing.T) {
	conf := t.TempDir()
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, "nested/.git/hooks"), 0o755)
	t.Setenv("SRTBOX_CONFIG_DIR", conf)
	t.Setenv("SRTBOX_TEST_TOKEN", "secret")
	t.Setenv("SSH_AUTH_SOCK", "")
	os.WriteFile(filepath.Join(conf, "base.json"),
		[]byte(`{"_denyEnv":["*TEST_TOKEN"],"network":{"allowedDomains":["a.example"]}}`), 0o600)
	os.WriteFile(filepath.Join(conf, "p.json"),
		[]byte(`{"_root":"`+proj+`","network":{"allowedDomains":["b.example"]},"_note":"x"}`), 0o600)

	meta, s, err := Build("p")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Root != proj {
		t.Errorf("root %q", meta.Root)
	}
	if _, ok := s["_note"]; ok {
		t.Error("meta key reached srt")
	}
	domains := s["network"].(map[string]any)["allowedDomains"].([]any)
	if !reflect.DeepEqual(domains, []any{"a.example", "b.example"}) {
		t.Errorf("domains %v", domains)
	}
	deny := strs(s["filesystem"].(map[string]any)["denyWrite"])
	if !slices.Contains(deny, filepath.Join(proj, "nested/.git/hooks")) {
		t.Errorf("nested hooks not protected: %v", deny)
	}
	vars := s["credentials"].(map[string]any)["envVars"].([]any)
	if vars[0].(map[string]any)["name"] != "SRTBOX_TEST_TOKEN" {
		t.Errorf("envVars %v", vars)
	}
	exe, _ := self()
	if !slices.Contains(strs(s["filesystem"].(map[string]any)["allowRead"]), exe) {
		t.Error("srtbox's own binary is not readable inside")
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
