package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// fetchTimeout bounds each command Fetch runs.
const fetchTimeout = 15 * time.Second

// Fetch runs a command that prints a credential, such as an _inject entry's
// from, and returns what it printed. A quiet fetch discards the command's
// stderr.
func Fetch(argv []string, quiet bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, Home(argv[0]), argv[1:]...)
	var out bytes.Buffer
	c.Stdout = &out
	if !quiet {
		c.Stderr = os.Stderr
	}
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	v := strings.TrimSpace(out.String())
	if v == "" {
		return "", fmt.Errorf("%s printed nothing", strings.Join(argv, " "))
	}
	return v, nil
}
