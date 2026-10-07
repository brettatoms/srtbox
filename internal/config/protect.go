package config

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// skipDirs are never searched for repos: large, generated, and never a repo.
var skipDirs = map[string]bool{
	"node_modules": true, ".devenv": true, ".direnv": true, "target": true,
	".cpcache": true, ".shadow-cljs": true, ".gradle": true, ".venv": true,
}

// hookManagers are files a git hook manager reads from the working tree, so a
// hook that is itself protected still runs whatever they say.
var hookManagers = []string{
	".husky", ".githooks", "lefthook.yml", ".lefthook.yml", "lefthook-local.yml",
	".pre-commit-config.yaml",
}

// ProtectRepos returns the paths under root that run code on the host without
// anyone choosing to run them: git hooks wherever core.hooksPath points, git
// config (core.fsmonitor, filter drivers, aliases), a worktree's gitdir
// pointer, and the editor and agent config that tools load when a repo is
// opened. srt protects these itself only where its own scan finds them, and
// that scan skips whatever the enclosing repo ignores — in a workspace of
// checked-out repos, that is every repo but the outer one.
//
// hooksPath reports a repo's core.hooksPath; nil uses git.
func ProtectRepos(root string, maxDepth int, hooksPath func(repo string) string) []string {
	if hooksPath == nil {
		hooksPath = GitHooksPath
	}
	var out []string
	add := func(p string) { out = append(out, p) }

	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() && (skipDirs[d.Name()] || depth > maxDepth) {
			return filepath.SkipDir
		}
		if d.Name() != ".git" {
			return nil
		}
		repo := filepath.Dir(p)
		if d.IsDir() {
			add(filepath.Join(p, "hooks"))
			add(filepath.Join(p, "config"))
			wt, _ := filepath.Glob(filepath.Join(p, "worktrees", "*", "config.worktree"))
			out = append(out, wt...)
		} else {
			add(p) // a worktree's pointer to its gitdir
		}
		protectRepoFiles(repo, hooksPath(repo), add)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})

	sort.Strings(out)
	return dropCovered(dedupe(out))
}

// dropCovered removes paths inside another path in the list. Protecting a
// directory already covers everything under it, and srt cannot bind a path
// that does not exist yet inside a directory it has already made read-only.
func dropCovered(paths []string) []string {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	var out []string
	for _, p := range paths {
		covered := false
		for dir := filepath.Dir(p); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			if set[dir] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// repoFiles are working-tree files that tools load from a repo. Only those
// present at launch are protected: srt blocks creating a missing path by
// mounting over it, which leaves an empty placeholder file in the working tree
// for as long as the session runs.
var repoFiles = []string{".mcp.json", ".vscode", ".idea", ".gitmodules", ".claude/commands", ".claude/agents"}

func protectRepoFiles(repo, hooks string, add func(string)) {
	for _, f := range append(repoFiles, hookManagers...) {
		if p := filepath.Join(repo, f); exists(p) {
			add(p)
		}
	}
	// Hook locations are protected whether or not they exist yet: git runs
	// whatever appears there, with no one choosing to.
	if hooks == "" {
		return
	}
	if !filepath.IsAbs(hooks) {
		// A relative hooksPath is resolved against the working tree. When it
		// starts in a dot-directory (husky's ".husky/_"), the hooks there run
		// scripts elsewhere in that directory, so the whole of it is protected.
		if first := strings.SplitN(filepath.ToSlash(hooks), "/", 2)[0]; strings.HasPrefix(first, ".") && first != "." && first != ".." {
			add(filepath.Join(repo, first))
		}
		hooks = filepath.Join(repo, hooks)
	}
	add(filepath.Clean(hooks))
}

// GitHooksPath reports core.hooksPath for a repo or worktree, or "".
func GitHooksPath(repo string) string {
	b, err := exec.Command("git", "-C", repo, "config", "--get", "core.hooksPath").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// DenyEnv returns the names in env (KEY=value pairs) matching any pattern,
// compared case-insensitively, except those listed exactly in allow. srt
// accepts exact names only, so the matching happens here, against the
// environment the sandbox is about to inherit.
func DenyEnv(patterns, allow, env []string) []string {
	var names []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(allow, name) {
			continue
		}
		for _, p := range patterns {
			if ok, _ := path.Match(strings.ToUpper(p), strings.ToUpper(name)); ok {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	return dedupe(names)
}

// AddEnvDeny records names as srt credentials.envVars entries in deny mode.
func AddEnvDeny(settings map[string]any, names ...string) {
	creds, _ := settings["credentials"].(map[string]any)
	if creds == nil {
		creds = map[string]any{}
		settings["credentials"] = creds
	}
	vars, _ := creds["envVars"].([]any)
	have := map[string]bool{}
	for _, v := range vars {
		if m, ok := v.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				have[n] = true
			}
		}
	}
	for _, n := range names {
		if !have[n] {
			vars = append(vars, map[string]any{"name": n, "mode": "deny"})
			have[n] = true
		}
	}
	creds["envVars"] = vars
}

// SetEnvMask records name as a masked credential: the sandbox sees a
// placeholder, and srt's proxy substitutes the real value only on requests to
// hosts. It replaces any deny entry for the same name.
func SetEnvMask(settings map[string]any, name string, hosts []string) {
	creds, _ := settings["credentials"].(map[string]any)
	if creds == nil {
		creds = map[string]any{}
		settings["credentials"] = creds
	}
	vars, _ := creds["envVars"].([]any)
	kept := vars[:0]
	for _, v := range vars {
		if m, ok := v.(map[string]any); !ok || m["name"] != name {
			kept = append(kept, v)
		}
	}
	h := make([]any, len(hosts))
	for i, x := range hosts {
		h[i] = x
	}
	creds["envVars"] = append(kept, map[string]any{"name": name, "mode": "mask", "injectHosts": h})
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func dedupe(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
