// Package broker runs selected commands on the host for a sandboxed caller.
// The host side serves a Unix socket for the life of one session; inside the
// sandbox, srtbox runs under the program's name and acts as the client.
package broker

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// Frame kinds. After the request header, each side sends frames: one kind
// byte, a four-byte big-endian length, then the payload.
const (
	kindStdin  byte = iota // client → host: input for the command
	kindStdout             // host → client
	kindStderr             // host → client
	kindExit               // host → client: int32 exit status, last frame
	kindWinch              // client → host: uint16 rows, uint16 cols
	kindEOF                // client → host: input is finished
	kindLocal              // host → client: run the program in the sandbox instead
	kindStart              // host → client: running on the host; payload 1 if input is wanted
)

const maxFrame = 1 << 20

// request is the header the client sends first.
type request struct {
	Program string   `json:"program"`
	Argv    []string `json:"argv"`
	Cwd     string   `json:"cwd"`
	TTY     bool     `json:"tty"`
	Rows    uint16   `json:"rows"`
	Cols    uint16   `json:"cols"`
}

func writeHeader(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	_, err = w.Write(append(n[:], b...))
	return err
}

func readHeader(r io.Reader, v any) error {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > maxFrame {
		return errors.New("request header too large")
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(h[1:])
	if size > maxFrame {
		return 0, nil, errors.New("frame too large")
	}
	p := make([]byte, size)
	_, err := io.ReadFull(r, p)
	return h[0], p, err
}

// frames serializes writes to one connection from several goroutines.
type frames struct {
	mu sync.Mutex
	w  io.Writer
}

func (f *frames) send(kind byte, p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var h [5]byte
	h[0] = kind
	binary.BigEndian.PutUint32(h[1:], uint32(len(p)))
	_, err := f.w.Write(append(h[:], p...))
	return err
}

func (f *frames) exit(code int) error {
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], uint32(int32(code)))
	return f.send(kindExit, p[:])
}

// stream is an io.Writer that sends each write as one frame of a kind.
type stream struct {
	f    *frames
	kind byte
}

func (s stream) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		n := min(len(p), maxFrame)
		if err := s.f.send(s.kind, p[:n]); err != nil {
			return total - len(p), err
		}
		p = p[n:]
	}
	return total, nil
}
