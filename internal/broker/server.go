package broker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// checkTimeout bounds a program's check hook.
const checkTimeout = 10 * time.Second

// Server runs brokered commands for one session.
type Server struct {
	Programs map[string]*Program
	Root     string   // callers' working directories are kept within it
	Env      []string // the environment commands run with, before each program's own credentials
	Approver *Approver
}

// Serve accepts connections until l is closed.
func (s *Server) Serve(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	out := &frames{w: conn}
	refuse := func(msg string) {
		out.send(kindStderr, []byte("srtbox: "+msg+"\n"))
		out.exit(126)
	}

	var req request
	if err := readHeader(br, &req); err != nil {
		return
	}
	p := s.Programs[req.Program]
	if p == nil {
		refuse(req.Program + " is not brokered")
		return
	}
	v, rule := p.classify(req.Argv)
	if v == runLocal {
		out.send(kindLocal, nil)
		return
	}
	cwd, err := s.workdir(req.Cwd)
	if err != nil {
		refuse(fmt.Sprintf("%s was not run: %v", display(p.Name, req.Argv), err))
		return
	}
	if p.Check != "" {
		if msg, ok := s.check(p, req.Argv, cwd); !ok {
			out.send(kindStderr, msg)
			out.exit(126)
			return
		}
	}
	if v == runApproved {
		msg := "srtbox: waiting for approval: answer the notification, or run `srtbox approve` on the host\n"
		if len(display(p.Name, req.Argv)) > notifyLimit {
			msg = "srtbox: waiting for approval: the command is too long for a notification; run `srtbox approve` on the host\n"
		}
		out.send(kindStderr, []byte(msg))
		if ok, why := s.Approver.Ask(p.Name, rule, req.Argv, cwd); !ok {
			refuse(display(p.Name, req.Argv) + " was not approved: " + why)
			return
		}
	}
	env, err := p.environ(s.Env)
	if err != nil {
		refuse(fmt.Sprintf("%s was not run: %v", display(p.Name, req.Argv), err))
		return
	}
	s.run(conn, br, out, p, req, cwd, env)
}

// workdir returns dir resolved, or an error when it is unknown, missing on
// the host, or outside Root, which is itself resolved. A caller outside Root
// is refused rather than moved, since a command run elsewhere could act on a
// repo the caller didn't choose.
func (s *Server) workdir(dir string) (string, error) {
	if s.Root == "" {
		if dir != "" {
			return dir, nil
		}
		d, _ := os.Getwd()
		return d, nil
	}
	if dir == "" {
		return "", errors.New("its working directory is unknown; it may have been removed")
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("its working directory %q does not exist on the host", dir)
	}
	if rel, err := filepath.Rel(s.Root, real); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("its working directory %q is outside the project root %s", dir, s.Root)
	}
	return real, nil
}

// check runs the program's check hook with argv. A non-zero exit refuses the
// command, and the hook's output says why.
func (s *Server) check(p *Program, argv []string, cwd string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, p.Check, argv...)
	c.Dir, c.Env = cwd, s.Env
	msg, err := c.CombinedOutput()
	if err == nil {
		return nil, true
	}
	if len(bytes.TrimSpace(msg)) == 0 {
		msg = []byte("srtbox: " + p.Name + "'s check refused " + display(p.Name, argv) + ": " + err.Error() + "\n")
	}
	return msg, false
}

// run executes the command and relays it over the connection. With a
// terminal on the caller's side it gets a pseudo-terminal here. Input is
// forwarded only when a stdin rule matches; otherwise the command reads
// /dev/null, so a brokered command never consumes the caller's input.
func (s *Server) run(conn net.Conn, br *bufio.Reader, out *frames, p *Program, req request, cwd string, env []string) {
	wantIn := p.wantsStdin(req.Argv)
	cmd := exec.Command(p.Path, req.Argv...)
	cmd.Dir, cmd.Env = cwd, env

	var (
		input   io.WriteCloser
		ptmx    *os.File
		copied  = make(chan struct{})
		started bool
	)
	if req.TTY {
		m, t, err := openPTY()
		if err == nil {
			ptmx = m
			setTermSize(m.Fd(), req.Rows, req.Cols)
			cmd.Stdout, cmd.Stderr = t, t
			if wantIn {
				cmd.Stdin = t
				input = m
			}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1}
			started = cmd.Start() == nil
			t.Close()
			if started {
				go func() { io.Copy(stream{out, kindStdout}, m); close(copied) }()
			}
		}
	}
	if ptmx == nil {
		cmd.Stdout, cmd.Stderr = stream{out, kindStdout}, stream{out, kindStderr}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if wantIn {
			input, _ = cmd.StdinPipe()
		}
		started = cmd.Start() == nil
		close(copied)
	}
	if !started {
		out.send(kindStderr, []byte("srtbox: could not start "+p.Path+"\n"))
		out.exit(127)
		return
	}
	in := byte(0)
	if wantIn {
		in = 1
	}
	out.send(kindStart, []byte{in})

	// The caller hanging up ends the command: the agent that ran it has
	// stopped waiting.
	go func() {
		for {
			kind, payload, err := readFrame(br)
			if err != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				return
			}
			switch {
			case kind == kindStdin && input != nil:
				input.Write(payload)
			case kind == kindEOF && input != nil:
				if ptmx != nil {
					input.Write([]byte{4}) // ^D, end of input on a terminal
				} else {
					input.Close()
				}
			case kind == kindWinch && ptmx != nil && len(payload) == 4:
				setTermSize(ptmx.Fd(), binary.BigEndian.Uint16(payload), binary.BigEndian.Uint16(payload[2:]))
			}
		}
	}()

	err := cmd.Wait()
	if ptmx != nil {
		// A background child can hold the terminal open after the command
		// exits; its output past this point is dropped.
		select {
		case <-copied:
		case <-time.After(500 * time.Millisecond):
		}
		ptmx.Close()
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
	}
	out.exit(code)
}
