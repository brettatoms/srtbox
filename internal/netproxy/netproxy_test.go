package netproxy

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
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

// echoProxy accepts any number of CONNECTs. It refuses ports for which serving
// reports false, and otherwise echoes, standing in for the host's server.
func echoProxy(t *testing.T, serving func(port int) bool) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				first, _ := br.ReadString('\n')
				for {
					line, _ := br.ReadString('\n')
					if line == "\r\n" || line == "" {
						break
					}
				}
				var port int
				fmt.Sscanf(first, "CONNECT 127.0.0.1:%d", &port)
				if !serving(port) {
					io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					return
				}
				io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
				io.Copy(c, br)
			}(c)
		}
	}()
	return l.Addr().String()
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func echoes(t *testing.T, port int) bool {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	defer c.Close()
	io.WriteString(c, "ping")
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	return err == nil && string(buf) == "ping"
}

func TestRelaysFollowServingAndPortfileChanges(t *testing.T) {
	a, b := freePort(t), freePort(t)
	var mu sync.Mutex
	up := map[int]bool{}
	setUp := func(p int) { mu.Lock(); up[p] = true; mu.Unlock() }
	t.Setenv("HTTP_PROXY", "http://"+echoProxy(t, func(p int) bool { mu.Lock(); defer mu.Unlock(); return up[p] }))

	dir := t.TempDir()
	portfile := filepath.Join(dir, ".nrepl-port")
	os.WriteFile(portfile, []byte(strconv.Itoa(a)), 0o644)
	r := NewRelays([]string{"@.nrepl-port"}, dir)

	if got := r.Sync(); len(got) != 0 {
		t.Fatalf("bound %v before the host served it", got)
	}
	setUp(a)
	if got := r.Sync(); !reflect.DeepEqual(got, []int{a}) || !echoes(t, a) {
		t.Fatalf("not relaying %d once served: %v", a, got)
	}

	os.WriteFile(portfile, []byte(strconv.Itoa(b)), 0o644)
	setUp(b)
	if got := r.Sync(); !reflect.DeepEqual(got, []int{b}) || !echoes(t, b) {
		t.Fatalf("did not follow the portfile to %d: %v", b, got)
	}
	if echoes(t, a) {
		t.Errorf("still relaying %d after the portfile moved", a)
	}
}

func TestResolvePortsExpandsRanges(t *testing.T) {
	got := ResolvePorts([]string{"3020-3022", "7888", "5-3"}, t.TempDir())
	if !reflect.DeepEqual(got, []int{3020, 3021, 3022, 7888}) {
		t.Fatalf("got %v", got)
	}
}

func TestCheckForward(t *testing.T) {
	if err := CheckForward([]string{"3020", "3020-3039", "@.nrepl-port", "@/abs/port"}); err != nil {
		t.Errorf("valid entries refused: %v", err)
	}
	for _, bad := range []string{"nope", "0", "70000", "3039-3020", "1-65535", "3020-x", "@"} {
		if err := CheckForward([]string{bad}); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: err %v; want one naming the entry", bad, err)
		}
	}
}
