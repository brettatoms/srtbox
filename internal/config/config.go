// Package config loads a project's sandbox policy: base.json merged with
// <project>.json from the config directory. Keys starting with "_" are read by
// srtbox itself and stripped before the settings reach srt.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Dir is where base.json and the project overlays live.
func Dir() string {
	if d := os.Getenv("SRTBOX_CONFIG_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "srtbox")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "srtbox")
}

// Projects lists the overlays in Dir, without their .json suffix.
func Projects() ([]string, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") || n == "base.json" {
			continue
		}
		names = append(names, strings.TrimSuffix(n, ".json"))
	}
	sort.Strings(names)
	return names, nil
}

// ProjectFor returns the project whose _root contains dir. When roots nest,
// the deepest one wins.
func ProjectFor(dir string) (string, error) {
	names, err := Projects()
	if err != nil {
		return "", err
	}
	best, bestRoot := "", ""
	for _, n := range names {
		doc, err := Load(n)
		if err != nil {
			return "", err
		}
		m, _ := Split(Expand(doc, os.LookupEnv).(map[string]any))
		if m.Root == "" || !Within(dir, m.Root) {
			continue
		}
		if len(m.Root) > len(bestRoot) {
			best, bestRoot = n, m.Root
		}
	}
	if best == "" {
		return "", fmt.Errorf("no project's _root contains %s; pass -p <project>", dir)
	}
	return best, nil
}

// Within reports whether path is root or below it.
func Within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Meta holds the "_" keys: settings srtbox acts on, never passed to srt.
type Meta struct {
	Root     string   // the project tree; protected repos are found under it
	Forward  []string // host loopback ports to relay in: "3020", or "@path/to/portfile"
	Broker   []string // command that ensures the project's host-side broker is running
	Mkdir    []string // directories to create before launch, so srt can bind them
	DenyEnv  []string // variable-name patterns to withhold from the sandbox
	AllowEnv []string // exact variable names exempt from DenyEnv
}

// Load merges base.json (optional) with <project>.json.
func Load(project string) (map[string]any, error) {
	overlay, err := readJSON(filepath.Join(Dir(), project+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			names, _ := Projects()
			return nil, fmt.Errorf("no such project: %s (have: %s)", project, strings.Join(names, " "))
		}
		return nil, err
	}
	base, err := readJSON(filepath.Join(Dir(), "base.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return Merge(base, overlay).(map[string]any), nil
}

func readJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// Merge combines two JSON values: objects merge key by key, arrays union with
// order kept and duplicates dropped, and anything else takes b unless b is nil.
func Merge(a, b any) any {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			break
		}
		out := map[string]any{}
		for k, v := range av {
			out[k] = v
		}
		for k, v := range bv {
			out[k] = Merge(av[k], v)
		}
		return out
	case []any:
		bv, ok := b.([]any)
		if !ok {
			break
		}
		return union(av, bv)
	}
	if b == nil {
		return a
	}
	return b
}

func union(a, b []any) []any {
	seen := map[string]bool{}
	var out []any
	for _, v := range append(append([]any{}, a...), b...) {
		k, _ := json.Marshal(v)
		if !seen[string(k)] {
			seen[string(k)] = true
			out = append(out, v)
		}
	}
	return out
}

var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand substitutes ${VAR} in every string value. An array entry naming a
// variable that is unset is dropped rather than kept as a half-formed path —
// ${XDG_RUNTIME_DIR} does not exist on macOS.
func Expand(v any, lookup func(string) (string, bool)) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			out[k] = Expand(x, lookup)
		}
		return out
	case []any:
		out := []any{}
		for _, x := range t {
			if s, ok := x.(string); ok {
				if e, ok := expandString(s, lookup); ok {
					out = append(out, e)
				}
				continue
			}
			out = append(out, Expand(x, lookup))
		}
		return out
	case string:
		if e, ok := expandString(t, lookup); ok {
			return e
		}
		return t
	}
	return v
}

func expandString(s string, lookup func(string) (string, bool)) (string, bool) {
	ok := true
	out := varRef.ReplaceAllStringFunc(s, func(m string) string {
		v, found := lookup(varRef.FindStringSubmatch(m)[1])
		if !found || v == "" {
			ok = false
		}
		return v
	})
	return out, ok
}

// Split separates the meta keys from the settings srt receives, stripping every
// "_"-prefixed key at any depth.
func Split(doc map[string]any) (Meta, map[string]any) {
	m := Meta{
		Root:     Home(str(doc["_root"])),
		Forward:  strs(doc["_forward"]),
		Broker:   cmd(doc["_broker"]),
		DenyEnv:  strs(doc["_denyEnv"]),
		AllowEnv: strs(doc["_allowEnv"]),
	}
	for _, d := range strs(doc["_mkdir"]) {
		m.Mkdir = append(m.Mkdir, Home(d))
	}
	return m, strip(doc).(map[string]any)
}

func strip(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if !strings.HasPrefix(k, "_") {
				out[k] = strip(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = strip(x)
		}
		return out
	}
	return v
}

// Home expands a leading ~ to the user's home directory.
func Home(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return h + p[1:]
	}
	return p
}

func str(v any) string { s, _ := v.(string); return s }

func strs(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = append(out, t)
	}
	return out
}

// cmd accepts a command as a shell string or an argv array.
func cmd(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{"sh", "-c", t}
	case []any:
		return strs(t)
	}
	return nil
}

// Append adds values to a string array at path (e.g. "filesystem", "denyWrite"),
// creating intermediate objects and skipping values already present.
func Append(doc map[string]any, path []string, values ...string) {
	node := doc
	for _, k := range path[:len(path)-1] {
		next, ok := node[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			node[k] = next
		}
		node = next
	}
	last := path[len(path)-1]
	cur, _ := node[last].([]any)
	have := map[string]bool{}
	for _, x := range cur {
		if s, ok := x.(string); ok {
			have[s] = true
		}
	}
	for _, v := range values {
		if !have[v] {
			cur = append(cur, v)
			have[v] = true
		}
	}
	node[last] = cur
}
