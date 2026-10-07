package broker

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
)

// Environment the host side sets for the client.
const (
	EnvSocket   = "SRTBOX_BROKER"          // the session's broker socket
	EnvPrograms = "SRTBOX_BROKER_PROGRAMS" // JSON: program name → real path
)

// ClientMain runs inside the sandbox when srtbox is invoked as a brokered
// program's name. The host decides: it runs the command and relays it, or
// says to run the real program here. When the broker cannot be reached the
// command runs here, which the sandbox allows anyway.
func ClientMain(name string, args []string) int {
	var paths map[string]string
	json.Unmarshal([]byte(os.Getenv(EnvPrograms)), &paths)
	local := func() int {
		path := paths[name]
		if path == "" {
			fmt.Fprintf(os.Stderr, "srtbox: no real %s to run\n", name)
			return 127
		}
		err := syscall.Exec(path, append([]string{path}, args...), os.Environ())
		fmt.Fprintln(os.Stderr, "srtbox: exec:", err)
		return 126
	}

	conn, err := net.Dial("unix", os.Getenv(EnvSocket))
	if err != nil {
		return local()
	}
	defer conn.Close()
	cwd, _ := os.Getwd()
	rows, cols, tty := termSize(os.Stdout.Fd())
	if err := writeHeader(conn, request{Program: name, Argv: args, Cwd: cwd, TTY: tty, Rows: rows, Cols: cols}); err != nil {
		return local()
	}

	out := &frames{w: conn}
	br := bufio.NewReader(conn)
	for {
		kind, payload, err := readFrame(br)
		if err != nil {
			fmt.Fprintln(os.Stderr, "srtbox: lost the broker connection")
			return 1
		}
		switch kind {
		case kindLocal:
			conn.Close()
			return local()
		case kindStart:
			if len(payload) == 1 && payload[0] == 1 {
				go func() {
					io.Copy(stream{out, kindStdin}, os.Stdin)
					out.send(kindEOF, nil)
				}()
			}
		case kindStdout:
			os.Stdout.Write(payload)
		case kindStderr:
			os.Stderr.Write(payload)
		case kindExit:
			if len(payload) != 4 {
				return 1
			}
			return int(int32(binary.BigEndian.Uint32(payload)))
		}
	}
}
