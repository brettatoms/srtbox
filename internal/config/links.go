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

// plantable returns the first symbolic link on the way to p whose directory
// a writable entry covers, or "".
func plantable(p string, writable []string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	cur := "/"
	for _, part := range parts {
		next := filepath.Join(cur, part)
		if fi, err := os.Lstat(next); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			for _, w := range writable {
				if under(cur, filepath.Clean(Home(w))) {
					return next
				}
			}
			target, err := os.Readlink(next)
			if err != nil {
				return next
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(cur, target)
			}
			next = filepath.Clean(target)
		}
		cur = next
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
