package sandbox

import (
	"os/exec"
	"testing"
)

func TestExitCode(t *testing.T) {
	cases := map[string]int{
		"exit 7":        7,
		"exit 0":        0,
		"kill -TERM $$": 143,
		"kill -KILL $$": 137,
	}
	for script, want := range cases {
		c := exec.Command("sh", "-c", script)
		c.Run()
		if got := ExitCode(c.ProcessState); got != want {
			t.Errorf("%q: got %d, want %d", script, got, want)
		}
	}
}
