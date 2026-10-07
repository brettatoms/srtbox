package launch

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func domains(s map[string]any) []any {
	return s["network"].(map[string]any)["allowedDomains"].([]any)
}

func TestLoopbackScopesAndFollowsPortfiles(t *testing.T) {
	dir := t.TempDir()
	portfile := filepath.Join(dir, ".nrepl-port")
	os.WriteFile(portfile, []byte("7888\n"), 0o644)
	settings := map[string]any{"network": map[string]any{"allowedDomains": []any{"example.com"}}}

	l := scopeLoopback([]string{"3020", "@.nrepl-port", "@missing"}, dir, settings)
	if !l.follows() {
		t.Error("a portfile entry should be followed")
	}
	want := []any{"example.com", "127.0.0.1:3020", "127.0.0.1:7888"}
	if !reflect.DeepEqual(domains(settings), want) {
		t.Fatalf("got %v", domains(settings))
	}
	if l.update() {
		t.Error("reported a change with no change")
	}

	r, w := io.Pipe()
	stop := make(chan struct{})
	defer close(stop)
	go l.follow(w, 10*time.Millisecond, stop)
	os.WriteFile(portfile, []byte("7999\n"), 0o644)

	line, err := bufio.NewReader(r).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal(line, &sent); err != nil {
		t.Fatal(err)
	}
	want = []any{"example.com", "127.0.0.1:3020", "127.0.0.1:7999"}
	if !reflect.DeepEqual(domains(sent), want) {
		t.Fatalf("sent %v", domains(sent))
	}
}

func TestLoopbackWithFixedPortsOnlyIsNotFollowed(t *testing.T) {
	settings := map[string]any{}
	if scopeLoopback([]string{"3020"}, "", settings).follows() {
		t.Error("fixed ports never change")
	}
	if !reflect.DeepEqual(domains(settings), []any{"127.0.0.1:3020"}) {
		t.Fatalf("got %v", domains(settings))
	}
}
