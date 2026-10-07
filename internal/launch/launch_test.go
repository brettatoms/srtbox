package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args    []string
		project string
		ssh     []sshTarget
		cmd     []string
		wantErr bool
	}{
		{[]string{"claude"}, "", nil, []string{"claude"}, false},
		{[]string{"-p", "p", "claude"}, "p", nil, []string{"claude"}, false},
		{[]string{"--project", "p", "--", "claude", "-x"}, "p", nil, []string{"claude", "-x"}, false},
		{[]string{"--ssh", "h", "-p", "p", "--", "claude"}, "p", []sshTarget{{host: "h"}}, []string{"claude"}, false},
		{[]string{"--ssh", "a", "--key", "/k", "--ssh", "b", "x"}, "", []sshTarget{{"a", "/k"}, {"b", ""}}, []string{"x"}, false},
		{[]string{"--", "-p", "p"}, "", nil, []string{"-p", "p"}, false},
		{[]string{"--key", "/k", "--ssh", "a", "x"}, "", nil, nil, true}, // --key before any --ssh
		{[]string{"--ssh", "a", "--ssh", "a", "x"}, "", nil, nil, true},  // same host twice
		{[]string{"-p"}, "", nil, nil, true},
		{[]string{"-p", "p"}, "", nil, nil, true},
	}
	for _, c := range cases {
		o, cmd, err := parseArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err %v", c.args, err)
			continue
		}
		if !c.wantErr && (o.project != c.project || !reflect.DeepEqual(o.ssh, c.ssh) || !reflect.DeepEqual(cmd, c.cmd)) {
			t.Errorf("%v: got %q %v %q", c.args, o.project, o.ssh, cmd)
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

func TestBuildFillsTheListsSrtRequires(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("SRTBOX_CONFIG_DIR", conf)
	t.Setenv("SSH_AUTH_SOCK", "")
	os.WriteFile(filepath.Join(conf, "p.json"), []byte(`{}`), 0o600)
	_, s, err := Build("p")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	for _, want := range []string{`"allowedDomains":[]`, `"deniedDomains":[]`, `"denyWrite":[]`, `"allowWrite":[]`, `"denyRead":[]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
}

func TestScopeUnixSocketsOnMacOSOnly(t *testing.T) {
	mac := map[string]any{"network": map[string]any{"allowAllUnixSockets": true, "allowUnixSockets": []any{"/var/run/docker.sock"}}}
	scopeUnixSockets(mac, "darwin", "/tmp/session", "/tmp/ssh")
	n := mac["network"].(map[string]any)
	if n["allowAllUnixSockets"] != false {
		t.Error("macOS kept every socket allowed")
	}
	if !reflect.DeepEqual(n["allowUnixSockets"], []any{"/var/run/docker.sock", "/tmp/session", "/tmp/ssh"}) {
		t.Errorf("allowUnixSockets %v", n["allowUnixSockets"])
	}
	linux := map[string]any{"network": map[string]any{"allowAllUnixSockets": true}}
	scopeUnixSockets(linux, "linux", "/tmp/session")
	if linux["network"].(map[string]any)["allowAllUnixSockets"] != true {
		t.Error("changed Linux settings")
	}
}
