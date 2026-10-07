package netproxy

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeProxy answers one CONNECT with status, then sends extra and echoes.
// It records the request headers it received.
func fakeProxy(t *testing.T, status, extra string) (addr string, got chan []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	got = make(chan []string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var lines []string
		for {
			line, _ := br.ReadString('\n')
			if line == "\r\n" || line == "" {
				break
			}
			lines = append(lines, strings.TrimSpace(line))
		}
		got <- lines
		io.WriteString(c, status+"\r\n\r\n"+extra)
		io.Copy(c, br)
	}()
	return l.Addr().String(), got
}

func TestDialSendsAuthAndKeepsBytesAfterHeaders(t *testing.T) {
	addr, got := fakeProxy(t, "HTTP/1.1 200 Connection Established", "EARLY")
	t.Setenv("HTTP_PROXY", "http://user:pass@"+addr)
	conn, err := Dial("example.com", 22)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := <-got
	if req[0] != "CONNECT example.com:22 HTTP/1.1" {
		t.Errorf("request line: %q", req[0])
	}
	want := "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if !contains(req, want) {
		t.Errorf("missing %q in %v", want, req)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "EARLY" {
		t.Fatalf("lost bytes sent after the headers: %q %v", buf, err)
	}
}

func TestDialReportsRefusal(t *testing.T) {
	addr, _ := fakeProxy(t, "HTTP/1.1 403 Forbidden", "")
	t.Setenv("HTTP_PROXY", "http://"+addr)
	if _, err := Dial("blocked.example", 443); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want a 403 error, got %v", err)
	}
}

func TestDialWithoutProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	if _, err := Dial("x", 1); err == nil {
		t.Fatal("want an error outside a sandbox")
	}
}

func TestResolvePorts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".nrepl-port"), []byte("59176\n"), 0o644)
	got := ResolvePorts([]string{"3020", "@.nrepl-port", "@missing", "nope", "70000"}, dir)
	if !reflect.DeepEqual(got, []int{3020, 59176}) {
		t.Fatalf("got %v", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
