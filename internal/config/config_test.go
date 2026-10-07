package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMerge(t *testing.T) {
	a := decode(t, `{"network":{"allowedDomains":["a","b"],"allowLocalBinding":false},"x":1}`)
	b := decode(t, `{"network":{"allowedDomains":["b","c"],"allowLocalBinding":true},"y":2}`)
	got := Merge(a, b).(map[string]any)
	want := decode(t, `{"network":{"allowedDomains":["a","b","c"],"allowLocalBinding":true},"x":1,"y":2}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestExpandDropsArrayEntriesWithUnsetVars(t *testing.T) {
	env := map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	got := Expand(decode(t, `{"p":["${XDG_RUNTIME_DIR}/x","${MISSING}/y","plain"],"s":"${MISSING}"}`), lookup)
	want := decode(t, `{"p":["/run/user/1000/x","plain"],"s":"${MISSING}"}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestSplitStripsMetaAtEveryDepth(t *testing.T) {
	meta, settings := Split(decode(t, `{
		"_root": "/proj", "_forward": ["3020", "@.nrepl-port"], "_broker": {"bz": {"host": [["aws", "logs"]]}},
		"_denyEnv": ["*TOKEN*"], "_mkdir": ["/tmp/a"],
		"network": {"_comment": "gone", "allowedDomains": ["a"]}}`))
	if meta.Root != "/proj" || len(meta.Forward) != 2 || meta.Broker["bz"] == nil ||
		meta.DenyEnv[0] != "*TOKEN*" || meta.Mkdir[0] != "/tmp/a" {
		t.Fatalf("meta: %+v", meta)
	}
	if !reflect.DeepEqual(settings, decode(t, `{"network":{"allowedDomains":["a"]}}`)) {
		t.Fatalf("settings: %v", settings)
	}
}

func TestDenyEnvMatchesCaseInsensitively(t *testing.T) {
	env := []string{"MAGIT_FORGE_GITHUB_TOKEN=x", "brave_api_key=y", "PATH=/bin", "HOME=/h"}
	got := DenyEnv([]string{"*TOKEN*", "*KEY*"}, nil, env)
	if !reflect.DeepEqual(got, []string{"MAGIT_FORGE_GITHUB_TOKEN", "brave_api_key"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDenyEnvSparesExactAllowedNames(t *testing.T) {
	env := []string{"FIGMA_TOKEN=x", "FIGMA_TOKEN_2=y", "figma_token=z"}
	got := DenyEnv([]string{"*TOKEN*"}, []string{"FIGMA_TOKEN", "FIGMA*"}, env)
	if !reflect.DeepEqual(got, []string{"FIGMA_TOKEN_2", "figma_token"}) {
		t.Fatalf("got %v", got)
	}
}

func TestProtectReposFindsNestedReposWorktreesAndHooksPath(t *testing.T) {
	root := t.TempDir()
	mk := func(p string) { os.MkdirAll(filepath.Join(root, p), 0o755) }
	mk(".git/hooks")     // the workspace repo
	mk("app/.git/hooks") // a nested repo using husky
	mk("app/.husky/_")
	mk("app/worktrees/wt1") // a worktree: .git is a file
	os.WriteFile(filepath.Join(root, "app/worktrees/wt1/.git"), []byte("gitdir: ../../.git/worktrees/wt1\n"), 0o644)
	mk("app/node_modules/dep/.git") // must be skipped

	hooks := func(repo string) string {
		if filepath.Base(repo) == "app" || filepath.Base(repo) == "wt1" {
			return ".husky/_"
		}
		return ""
	}
	got := ProtectRepos(root, 5, hooks)
	for _, want := range []string{
		".git/hooks", ".git/config",
		"app/.git/hooks", "app/.git/config", "app/.husky",
		"app/worktrees/wt1/.git", "app/worktrees/wt1/.husky",
	} {
		if !slices.Contains(got, filepath.Join(root, want)) {
			t.Errorf("missing %s", want)
		}
	}
	for _, p := range got {
		if filepath.Base(filepath.Dir(p)) == "dep" || filepath.Base(p) == "dep" {
			t.Errorf("searched inside node_modules: %s", p)
		}
	}
	if slices.Contains(got, filepath.Join(root, "app/.husky/_")) {
		t.Error("kept .husky/_ although .husky already covers it")
	}
}

func TestProtectReposSkipsMissingRepoFiles(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git/hooks"), 0o755)
	os.MkdirAll(filepath.Join(root, ".claude/commands"), 0o755)
	os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{}"), 0o644)

	got := ProtectRepos(root, 5, func(string) string { return "" })
	for _, want := range []string{".mcp.json", ".claude/commands"} {
		if !slices.Contains(got, filepath.Join(root, want)) {
			t.Errorf("missing %s", want)
		}
	}
	for _, absent := range []string{".gitmodules", ".vscode", ".idea", ".claude/agents"} {
		if slices.Contains(got, filepath.Join(root, absent)) {
			t.Errorf("protected %s, which does not exist", absent)
		}
	}
}

func TestDropCovered(t *testing.T) {
	got := dropCovered([]string{"/r/.husky", "/r/.husky-x", "/r/.husky/_", "/r/.git/hooks"})
	want := []string{"/r/.husky", "/r/.husky-x", "/r/.git/hooks"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestAddEnvDenyIsIdempotent(t *testing.T) {
	s := map[string]any{}
	AddEnvDeny(s, "A", "B")
	AddEnvDeny(s, "B", "C")
	if n := len(s["credentials"].(map[string]any)["envVars"].([]any)); n != 3 {
		t.Fatalf("got %d entries", n)
	}
}

func TestWithin(t *testing.T) {
	for path, want := range map[string]bool{"/a/b": true, "/a/b/c": true, "/a": false, "/a/bc": false} {
		if got := Within(path, "/a/b"); got != want {
			t.Errorf("Within(%q): %v", path, got)
		}
	}
}

func TestProjectForPicksTheDeepestRoot(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("SRTBOX_CONFIG_DIR", conf)
	t.Setenv("SRTBOX_TEST_ROOT", "/w")
	os.WriteFile(filepath.Join(conf, "base.json"), []byte(`{}`), 0o600)
	os.WriteFile(filepath.Join(conf, "outer.json"), []byte(`{"_root":"${SRTBOX_TEST_ROOT}"}`), 0o600)
	os.WriteFile(filepath.Join(conf, "inner.json"), []byte(`{"_root":"/w/inner"}`), 0o600)
	os.WriteFile(filepath.Join(conf, "rootless.json"), []byte(`{}`), 0o600)

	for dir, want := range map[string]string{"/w": "outer", "/w/x": "outer", "/w/inner/y": "inner"} {
		if got, err := ProjectFor(dir); err != nil || got != want {
			t.Errorf("ProjectFor(%q) = %q, %v; want %q", dir, got, err, want)
		}
	}
	if _, err := ProjectFor("/elsewhere"); err == nil {
		t.Error("no error outside every root")
	}
}

func TestFollowLinksAddsTargetsButNotPlantableOnes(t *testing.T) {
	// Link targets come back resolved; on macOS the temporary directory sits
	// behind a link.
	home, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(home, "cloud", ".claude"), 0o755)
	os.Symlink(filepath.Join(home, "cloud", ".claude"), filepath.Join(home, ".claude"))
	os.MkdirAll(filepath.Join(home, "proj"), 0o755)
	os.MkdirAll(filepath.Join(home, "secret"), 0o755)
	os.Symlink(filepath.Join(home, "secret"), filepath.Join(home, "proj", "planted"))

	settings := map[string]any{"filesystem": map[string]any{
		"allowRead":  []any{filepath.Join(home, ".claude"), filepath.Join(home, "proj", "planted")},
		"allowWrite": []any{filepath.Join(home, "proj")},
	}}
	var warnings []string
	FollowLinks(settings, func(m string) { warnings = append(warnings, m) })
	reads := strs(get(settings, "filesystem", "allowRead"))
	if !slices.Contains(reads, filepath.Join(home, "cloud", ".claude")) {
		t.Errorf("link target not added: %v", reads)
	}
	if slices.Contains(reads, filepath.Join(home, "secret")) {
		t.Errorf("followed a link inside a writable path: %v", reads)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "planted") {
		t.Errorf("warnings %v", warnings)
	}
}
