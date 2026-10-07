package broker

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	p := &Program{
		Host:    [][]string{{"aws", "logs"}, {"wt"}, {"db", "drop", "table"}},
		Approve: [][]string{{"wt", "remove"}, {"aws", "logs"}, {"db", "drop"}},
	}
	cases := []struct {
		argv []string
		want verdict
	}{
		{[]string{"aws", "logs", "--env", "stg"}, runApproved}, // in both: approve wins
		{[]string{"wt", "list"}, runHost},
		{[]string{"wt", "remove", "--name", "x"}, runApproved},
		{[]string{"db", "drop", "table", "users"}, runApproved}, // a longer host rule does not remove the approval
		{[]string{"--region", "aws", "logs"}, runLocal},         // a flag value cannot pose as a command word
		{[]string{"wt", "--force", "remove"}, runHost},          // words must lead: this is `wt`, not `wt remove`
		{[]string{"db", "reset"}, runLocal},
		{nil, runLocal},
	}
	for _, c := range cases {
		if got, _ := p.classify(c.argv); got != c.want {
			t.Errorf("%v: got %v, want %v", c.argv, got, c.want)
		}
	}
}

func TestDisplayQuotesWhatAShellWouldInterpret(t *testing.T) {
	got := display("bz", []string{"db", "connect", "--", "-c", "select 1; drop", "a\nb"})
	want := `bz db connect -- -c "select 1; drop" "a\nb"`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

// result is what a client saw.
type result struct {
	local          bool
	stdout, stderr string
	code           int
}

// call speaks the protocol as the client does, sending input when the host
// asks for it.
func call(t *testing.T, sock string, req request, input string) result {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := writeHeader(c, req); err != nil {
		t.Fatal(err)
	}
	out := &frames{w: c}
	br := bufio.NewReader(c)
	var r result
	for {
		kind, p, err := readFrame(br)
		if err != nil {
			t.Fatalf("connection ended without an exit: %v (so far %+v)", err, r)
		}
		switch kind {
		case kindLocal:
			r.local = true
			return r
		case kindStart:
			if p[0] == 1 {
				out.send(kindStdin, []byte(input))
				out.send(kindEOF, nil)
			}
		case kindStdout:
			r.stdout += string(p)
		case kindStderr:
			r.stderr += string(p)
		case kindExit:
			r.code = int(int32(binary.BigEndian.Uint32(p)))
			return r
		}
	}
}

func script(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func serve(t *testing.T, s *Server) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "b.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go s.Serve(l)
	return sock
}

func TestServerRunsChecksAndRelays(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	os.Mkdir(root, 0o755)
	prog := &Program{
		Name: "tool",
		Path: script(t, dir, "tool", `
case "$1" in
  echo) shift; echo "out:$*"; echo "err" >&2; exit 3 ;;
  pwd) pwd ;;
  cat) cat ;;
  tty) [ -t 1 ] && echo tty || echo notty ;;
esac
`),
		Check: script(t, dir, "check", `case "$*" in *forbidden*) echo "no forbidden"; exit 1 ;; esac`),
		Host:  [][]string{{"echo"}, {"pwd"}, {"cat"}, {"tty"}, {"cat-in"}},
		Stdin: [][]string{{"cat"}},
	}
	sock := serve(t, &Server{Programs: map[string]*Program{"tool": prog}, Root: root, Env: os.Environ()})

	if r := call(t, sock, request{Program: "tool", Argv: []string{"echo", "a", "b"}}, ""); r.stdout != "out:a b\n" || r.stderr != "err\n" || r.code != 3 {
		t.Errorf("echo: %+v", r)
	}
	if r := call(t, sock, request{Program: "tool", Argv: []string{"local", "thing"}}, ""); !r.local {
		t.Errorf("unmatched command should run locally: %+v", r)
	}
	if r := call(t, sock, request{Program: "tool", Argv: []string{"echo", "forbidden"}}, ""); r.code != 126 || !strings.Contains(r.stderr, "no forbidden") {
		t.Errorf("check hook: %+v", r)
	}
	if r := call(t, sock, request{Program: "tool", Argv: []string{"pwd"}, Cwd: "/"}, ""); strings.TrimSpace(r.stdout) != root {
		t.Errorf("cwd outside root should become root: %+v", r)
	}
	if r := call(t, sock, request{Program: "tool", Argv: []string{"cat"}}, "piped"); r.stdout != "piped" {
		t.Errorf("stdin rule: %+v", r)
	}
	if r := call(t, sock, request{Program: "other"}, ""); r.code != 126 {
		t.Errorf("unknown program: %+v", r)
	}
	if r := call(t, sock, request{Program: "tool", Argv: []string{"tty"}, TTY: true, Rows: 24, Cols: 80}, ""); strings.TrimSpace(r.stdout) != "tty" {
		t.Errorf("pty: %+v", r)
	}
}

func TestServerNeverReadsInputWithoutAStdinRule(t *testing.T) {
	dir := t.TempDir()
	prog := &Program{Name: "tool", Path: script(t, dir, "tool", "cat; echo done"), Host: [][]string{{"x"}}}
	sock := serve(t, &Server{Programs: map[string]*Program{"tool": prog}, Env: os.Environ()})
	if r := call(t, sock, request{Program: "tool", Argv: []string{"x"}}, "input"); r.stdout != "done\n" {
		t.Errorf("got %+v", r)
	}
}

func TestApprovals(t *testing.T) {
	dir := t.TempDir()
	prog := &Program{Name: "tool", Path: script(t, dir, "tool", "echo ran"), Approve: [][]string{{"rm"}}}
	var asked atomic.Int32
	answer := "session"
	a := &Approver{Project: "p", Dir: filepath.Join(dir, "approvals"), Timeout: 2 * time.Second,
		Notify: func(ctx context.Context, title, body string) string { asked.Add(1); return answer }}
	sock := serve(t, &Server{Programs: map[string]*Program{"tool": prog}, Env: os.Environ(), Approver: a})
	req := request{Program: "tool", Argv: []string{"rm", "x"}}

	if r := call(t, sock, req, ""); r.stdout != "ran\n" || !strings.Contains(r.stderr, "waiting for approval") {
		t.Fatalf("approved run: %+v", r)
	}
	call(t, sock, req, "")
	if asked.Load() != 1 {
		t.Errorf("session approval asked %d times", asked.Load())
	}

	// A fresh approver: the notification denies.
	a.granted, answer = nil, "deny"
	if r := call(t, sock, req, ""); r.code != 126 || !strings.Contains(r.stderr, "denied") {
		t.Errorf("deny: %+v", r)
	}

	// No notifier: `srtbox approve` answers through the pending file.
	a.Notify = nil
	go func() {
		for i := 0; i < 50; i++ {
			files, _ := filepath.Glob(filepath.Join(a.Dir, "*.json"))
			if len(files) == 1 {
				os.WriteFile(strings.TrimSuffix(files[0], ".json")+".answer", []byte("once"), 0o600)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	if r := call(t, sock, req, ""); r.stdout != "ran\n" {
		t.Errorf("file answer: %+v", r)
	}

	// Nobody answers.
	a.Timeout = 200 * time.Millisecond
	if r := call(t, sock, req, ""); r.code != 126 || !strings.Contains(r.stderr, "no answer") {
		t.Errorf("timeout: %+v", r)
	}
	if left, _ := filepath.Glob(filepath.Join(a.Dir, "*")); len(left) != 0 {
		t.Errorf("pending files left behind: %v", left)
	}
}

func TestParseRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "../bz", "srtbox", "a/b"} {
		if _, err := Parse(map[string]any{name: map[string]any{"path": "/bin/sh"}}); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	got, err := Parse(map[string]any{"sh": map[string]any{"host": []any{[]any{"x"}}}})
	if err != nil || got["sh"].Path == "" || got["sh"].Host[0][0] != "x" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestApprovalOfALongCommandSkipsTheNotification(t *testing.T) {
	dir := t.TempDir()
	notified := false
	a := &Approver{Project: "p", Dir: dir, Timeout: 2 * time.Second,
		Notify: func(context.Context, string, string) string { notified = true; return "once" }}
	long := strings.Repeat("x", notifyLimit) + "TAIL"
	go func() {
		for i := 0; i < 50; i++ {
			files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
			if len(files) == 1 {
				b, _ := os.ReadFile(files[0])
				if strings.Contains(string(b), "TAIL") && strings.Contains(string(b), `with any arguments`) {
					os.WriteFile(strings.TrimSuffix(files[0], ".json")+".answer", []byte("deny"), 0o600)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	ok, why := a.Ask("tool", []string{"rm"}, []string{"rm", long}, "/")
	if ok || why != "denied" {
		t.Fatalf("got %v %q; the pending file should hold the whole command and its session scope", ok, why)
	}
	if notified {
		t.Error("a command too long to show was offered in a notification")
	}
}

func TestAPendingApprovalDoesNotBlockGrantedCommands(t *testing.T) {
	a := &Approver{Project: "p", Dir: t.TempDir(), Timeout: time.Second,
		granted: map[string]bool{"tool\x00ok": true}}
	go a.Ask("tool", []string{"slow"}, []string{"slow"}, "/") // waits for its timeout
	time.Sleep(50 * time.Millisecond)
	done := make(chan bool)
	go func() { ok, _ := a.Ask("tool", []string{"ok"}, []string{"ok"}, "/"); done <- ok }()
	select {
	case ok := <-done:
		if !ok {
			t.Error("granted rule refused")
		}
	case <-time.After(300 * time.Millisecond):
		t.Error("a granted command waited on another command's approval")
	}
}
