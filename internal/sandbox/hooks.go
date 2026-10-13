package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// HooksEnv names the variable that carries the _init hooks to srtbox init, as
// a JSON array of Hook.
const HooksEnv = "SRTBOX_INIT"

// Hook is one _init entry: a command run inside the sandbox before the user's
// command. Timeout is in seconds; zero means none.
type Hook struct {
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Timeout float64  `json:"timeout"`
}

var envLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// runHooks runs each hook in order and sets every KEY=value line it prints on
// stdout in this process's environment, so later hooks and the command see
// it. A hook that fails or times out is reported to warn, its output
// discarded, and the rest still run.
func runHooks(hooks []Hook, warn io.Writer) {
	for _, h := range hooks {
		out, err := runHook(h)
		if err != nil {
			fmt.Fprintf(warn, "srtbox: warning: _init.%s: %v\n", h.Name, err)
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			line := strings.TrimSuffix(sc.Text(), "\r")
			if envLine.MatchString(line) {
				k, v, _ := strings.Cut(line, "=")
				os.Setenv(k, v)
			}
		}
	}
}

// runHook runs h in its own process group, so a timeout kills everything it
// started rather than leaving a child holding its output open.
func runHook(h Hook) ([]byte, error) {
	ctx := context.Background()
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(h.Timeout*float64(time.Second)))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, h.Argv[0], h.Argv[1:]...)
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("timed out after %gs", h.Timeout)
	}
	return out, err
}
