// Package sandbox holds the code that runs inside the srt sandbox.
package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/brettatoms/srtbox/internal/netproxy"
)

// Init is the first process inside the sandbox. It does what srt cannot do from
// outside, then runs the command:
//
//   - relays declared host loopback ports in, on Linux, where the sandbox has
//     its own network namespace
//   - relays terminal resizes, on Linux, where srt's bwrap --new-session means
//     the kernel never delivers SIGWINCH inside
//   - restores GIT_SSH_COMMAND for --ssh, which srt overwrites with its own
//   - forwards termination signals and reports a signal death as 128+N
//
// When none of that applies it execs the command, leaving no extra process.
func Init(args []string) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: srtbox init -- <command> [args...]")
		return 2
	}
	if v := os.Getenv("SRTBOX_GIT_SSH_COMMAND"); v != "" {
		os.Setenv("GIT_SSH_COMMAND", v)
	}

	relays := 0
	if runtime.GOOS == "linux" {
		var entries []string
		json.Unmarshal([]byte(os.Getenv("SRTBOX_FORWARD")), &entries)
		if bound := netproxy.StartRelays(netproxy.ResolvePorts(entries, os.Getenv("SRTBOX_ROOT"))); len(bound) > 0 {
			relays = len(bound)
			fmt.Fprintf(os.Stderr, "srtbox: relaying host ports %v\n", bound)
		}
	}
	tty := resizeTTY()

	if relays == 0 && tty < 0 {
		path, err := exec.LookPath(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "srtbox:", err)
			return 127
		}
		err = syscall.Exec(path, args, os.Environ())
		fmt.Fprintln(os.Stderr, "srtbox: exec:", err)
		return 126
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 127
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT)
	go func() {
		for s := range sigs {
			cmd.Process.Signal(s)
		}
	}()
	if tty >= 0 {
		go relayResizes(cmd.Process, tty)
	}
	cmd.Wait()
	return ExitCode(cmd.ProcessState)
}

// ExitCode reports how a process ended in shell convention: its exit status,
// or 128+N when signal N killed it.
func ExitCode(ps *os.ProcessState) int {
	if ps == nil {
		return 1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
