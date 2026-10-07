// Package netproxy reaches the outside of an srt sandbox through srt's own
// proxy: host loopback ports relayed in, and TCP to remote hosts for ssh.
package netproxy

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Dial opens a TCP connection to host:port through the sandbox's HTTP proxy.
// Inside an srt sandbox that proxy is the only way out: its address and
// per-run credentials arrive in HTTP_PROXY, and CONNECT carries any TCP, not
// just HTTPS. The same mechanism works on Linux and macOS.
func Dial(host string, port int) (net.Conn, error) {
	raw := os.Getenv("HTTP_PROXY")
	if raw == "" {
		raw = os.Getenv("http_proxy")
	}
	if raw == "" {
		return nil, errors.New("no HTTP_PROXY: not inside an srt sandbox")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("HTTP_PROXY: %w", err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("proxy %s: %w", u.Host, err)
	}
	target := net.JoinHostPort(host, strconv.Itoa(port))
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if u.User != nil {
		pass, _ := u.User.Password()
		cred := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + pass))
		req += "Proxy-Authorization: Basic " + cred + "\r\n"
	}
	if _, err := io.WriteString(conn, req+"\r\n"); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy: %w", err)
	}
	if f := strings.Fields(status); len(f) < 2 || f[1] != "200" {
		conn.Close()
		return nil, fmt.Errorf("proxy refused %s: %s", target, strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("proxy: %w", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	return &bufConn{Conn: conn, r: br}, nil
}

// bufConn reads through the reader that parsed the CONNECT response, so bytes
// the server sent right after its headers are not lost.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *bufConn) CloseWrite() error {
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		return tc.CloseWrite()
	}
	return nil
}

type closeWriter interface{ CloseWrite() error }

// splice copies both ways until each side has finished sending.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if cw, ok := dst.(closeWriter); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
		done <- struct{}{}
	}
	go pipe(a, b)
	go pipe(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// ResolvePorts turns entries like "3020" or "@path/to/portfile" into port
// numbers. A relative portfile is read from base. Entries that cannot be read
// or parsed are skipped: a REPL that is not running has no port file.
func ResolvePorts(entries []string, base string) []int {
	var ports []int
	for _, e := range entries {
		s := e
		if strings.HasPrefix(e, "@") {
			p := e[1:]
			if !filepath.IsAbs(p) {
				p = filepath.Join(base, p)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			s = strings.TrimSpace(string(b))
		}
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n < 65536 {
			ports = append(ports, n)
		}
	}
	return ports
}

// Relays keeps a relay on the sandbox's loopback for each declared port the
// host is serving. Entries are port numbers or @portfile paths; Sync re-reads
// the portfiles, so a REPL restarted on a new port is followed.
type Relays struct {
	entries []string
	base    string
	live    map[int]net.Listener
}

// NewRelays returns relays for entries, with relative portfiles read from base.
func NewRelays(entries []string, base string) *Relays {
	return &Relays{entries: entries, base: base, live: map[int]net.Listener{}}
}

// Sync closes the relays for ports no longer declared and starts one for each
// declared port the host now answers on. A port is bound only once the host
// serves it, so "connection refused" inside still means nothing is running
// there. It returns the ports newly bound.
func (r *Relays) Sync() []int {
	want := map[int]bool{}
	for _, p := range ResolvePorts(r.entries, r.base) {
		want[p] = true
	}
	for p, l := range r.live {
		if !want[p] {
			l.Close()
			delete(r.live, p)
		}
	}
	var bound []int
	for _, port := range slices.Sorted(maps.Keys(want)) {
		if r.live[port] != nil {
			continue
		}
		probe, err := Dial("127.0.0.1", port)
		if err != nil {
			continue
		}
		probe.Close()
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		r.live[port] = l
		go serve(l, port)
		bound = append(bound, port)
	}
	return bound
}

// Follow calls Sync every interval, for the life of the process.
func (r *Relays) Follow(interval time.Duration) {
	for range time.Tick(interval) {
		r.Sync()
	}
}

func serve(l net.Listener, port int) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			up, err := Dial("127.0.0.1", port)
			if err != nil {
				c.Close()
				return
			}
			splice(c, up)
		}(c)
	}
}

// RelayInterval is how often relays re-check their ports.
const RelayInterval = 2 * time.Second

// SSHProxyMain is an ssh ProxyCommand: it connects stdio to host:port through
// the proxy, because the sandbox has no route to port 22 of its own.
func SSHProxyMain(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: srtbox ssh-proxy <host> <port>")
		return 2
	}
	port, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox ssh-proxy: bad port:", args[1])
		return 2
	}
	conn, err := Dial(args[0], port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox ssh-proxy:", err)
		return 1
	}
	done := make(chan struct{})
	go func() { io.Copy(os.Stdout, conn); close(done) }()
	io.Copy(conn, os.Stdin)
	if cw, ok := conn.(closeWriter); ok {
		cw.CloseWrite()
	}
	<-done
	return 0
}
