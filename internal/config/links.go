package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FollowLinks adds to allowRead and allowWrite the targets of entries that
// are symbolic links, or that pass through one. Both sandboxes apply rules to
// the path a link resolves to, so a grant on the link alone gives nothing. A
// link inside a writable path is not followed: an earlier session could have
// planted it to point anywhere, and following it would grant that place.
func FollowLinks(settings map[string]any, warn func(string)) {
	writable := strs(get(settings, "filesystem", "allowWrite"))
	for _, key := range []string{"allowRead", "allowWrite"} {
		for _, e := range strs(get(settings, "filesystem", key)) {
			p := filepath.Clean(Home(e))
			real, err := filepath.EvalSymlinks(p)
			if err != nil || real == p {
				continue
			}
			if link := plantable(p, writable); link != "" {
				warn(fmt.Sprintf("not following %s in %s %q: it sits in a writable path", link, key, e))
				continue
			}
			Append(settings, []string{"filesystem", key}, real)
		}
	}
}

// ResolveRoot returns root with symbolic links resolved, the form the
// sandbox's paths take. A root that does not exist yet has the links in its
// existing ancestors resolved. It refuses a root reached through a link in a
// writable path, which an earlier session could have planted to move the root
// anywhere.
func ResolveRoot(root string, settings map[string]any) (string, error) {
	p := filepath.Clean(root)
	real := resolvePath(p)
	if real == p {
		return p, nil
	}
	if link := plantable(p, strs(get(settings, "filesystem", "allowWrite"))); link != "" {
		return "", fmt.Errorf("_root %s passes through %s, a symbolic link in a writable path; write _root as the path it resolves to", root, link)
	}
	return real, nil
}

// resolvePath resolves the links in p's longest existing ancestor and keeps
// the rest of p as written.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// plantable returns the first symbolic link on the way to p whose directory
// a writable entry covers, or "". It follows each link through every
// component of its target, and checks writable entries both as written and
// resolved, since links are checked in their resolved directories.
func plantable(p string, writable []string) string {
	var dirs []string
	for _, w := range writable {
		w = filepath.Clean(Home(w))
		dirs = append(dirs, w)
		if real := resolvePath(w); real != w {
			dirs = append(dirs, real)
		}
	}
	rest := strings.Split(p, "/")
	cur := "/"
	for links := 0; len(rest) > 0; {
		part := rest[0]
		rest = rest[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, part)
		if fi, err := os.Lstat(next); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		for _, d := range dirs {
			if under(cur, d) {
				return next
			}
		}
		target, err := os.Readlink(next)
		if links++; err != nil || links > 255 {
			return next
		}
		if filepath.IsAbs(target) {
			cur = "/"
		}
		rest = append(strings.Split(target, "/"), rest...)
	}
	return ""
}

func under(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}

func get(doc map[string]any, a, b string) any {
	m, _ := doc[a].(map[string]any)
	return m[b]
}
