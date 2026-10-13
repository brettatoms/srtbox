package sandbox

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunHooksSetsPrintedVariables(t *testing.T) {
	t.Setenv("HOOK_A", "")
	t.Setenv("HOOK_B", "")
	t.Setenv("HOOK_FAILED", "")
	var warn bytes.Buffer
	runHooks([]Hook{
		{Name: "a", Argv: []string{"sh", "-c", "echo noise; echo HOOK_A=one=two; echo 'not a var=x'"}},
		{Name: "b", Argv: []string{"sh", "-c", `echo "HOOK_B=$HOOK_A"`}},
		{Name: "c", Argv: []string{"sh", "-c", "echo HOOK_FAILED=yes; exit 3"}},
	}, &warn)
	if got := os.Getenv("HOOK_A"); got != "one=two" {
		t.Errorf("HOOK_A = %q", got)
	}
	if got := os.Getenv("HOOK_B"); got != "one=two" {
		t.Errorf("a later hook should see an earlier one's variables: HOOK_B = %q", got)
	}
	if got := os.Getenv("HOOK_FAILED"); got != "" {
		t.Errorf("a failed hook's output should be discarded: HOOK_FAILED = %q", got)
	}
	if !strings.Contains(warn.String(), "_init.c: exit status 3") {
		t.Errorf("warning %q", warn.String())
	}
}

func TestRunHooksTimesOut(t *testing.T) {
	var warn bytes.Buffer
	start := time.Now()
	// The background sleep keeps stdout open: the whole group has to go.
	runHooks([]Hook{{Name: "slow", Argv: []string{"sh", "-c", "sleep 30 & sleep 30"}, Timeout: 0.2}}, &warn)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if !strings.Contains(warn.String(), "_init.slow: timed out after 0.2s") {
		t.Errorf("warning %q", warn.String())
	}
}
