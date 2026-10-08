package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestJSONC(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"{\"a\": 1} // trailing", `{"a": 1}`},
		{"{\n  // line\n  \"a\": 1 /* block */\n}", `{"a": 1}`},
		{"{/* across\nlines */\"a\": 1}", `{"a": 1}`},
		{`{"u": "https://x/*y*/"}`, `{"u": "https://x/*y*/"}`},
		{`{"q": "a\"//b", "b": "c\\"} // x`, `{"q": "a\"//b", "b": "c\\"}`},
		{`{"a": [1, 2,], "b": {"c": 3,},}`, `{"a": [1, 2], "b": {"c": 3}}`},
		{"{\"a\": [1, // last\n], \"s\": \",]\"}", `{"a": [1], "s": ",]"}`},
	} {
		out, err := jsonc([]byte(c.in))
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(out) != len(c.in) {
			t.Errorf("%q: length %d, want %d", c.in, len(out), len(c.in))
		}
		var got, want any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Errorf("%q: %v in %q", c.in, err, out)
			continue
		}
		json.Unmarshal([]byte(c.want), &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", c.in, got, want)
		}
	}
	if _, err := jsonc([]byte("{\n\"a\": 1 /* open")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("unterminated comment: %v", err)
	}
}

func TestReadJSONNamesTheLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(path, []byte("{\n  // fine\n  \"a\": 1,\n  \"b\" 2\n}\n"), 0o600)
	if _, err := readJSON(path); err == nil || !strings.Contains(err.Error(), path+": line 4:") {
		t.Errorf("got %v", err)
	}
}
