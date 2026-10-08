package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brettatoms/srtbox/internal/broker"
)

func TestSrtFinding(t *testing.T) {
	for have, want := range map[string]string{"0.0.76": statusOK, "0.0.79": statusOK, "0.1.0": statusOK, "0.0.75": statusFail, "": statusFail} {
		if got := srtFinding(have).status; got != want {
			t.Errorf("srt %q: %s; want %s", have, got, want)
		}
	}
}

func TestBrokerFindingsNeedExecutables(t *testing.T) {
	dir := t.TempDir()
	run := filepath.Join(dir, "bz")
	check := filepath.Join(dir, "bz-check")
	os.WriteFile(run, []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(check, []byte("#!/bin/sh\n"), 0o644)
	fs := brokerFindings(map[string]*broker.Program{"bz": {Name: "bz", Path: run, Check: check}})
	if len(fs) != 1 || fs[0].status != statusFail || !strings.Contains(fs[0].msg, "bz-check") {
		t.Errorf("findings %+v; want the non-executable check named", fs)
	}
	os.Chmod(check, 0o755)
	if fs := brokerFindings(map[string]*broker.Program{"bz": {Name: "bz", Path: run, Check: check}}); fs[0].status != statusOK {
		t.Errorf("findings %+v; want ok", fs)
	}
}

func TestInjectFindingsNeverShowValues(t *testing.T) {
	fs := injectFindings(map[string]any{
		"GOOD":  map[string]any{"from": "echo s3cret", "hosts": []any{"a.example"}},
		"EMPTY": map[string]any{"from": "true", "hosts": []any{"a.example"}},
	})
	if len(fs) != 2 || fs[0].label != "inject EMPTY" || fs[0].status != statusWarn || fs[1].status != statusOK {
		t.Errorf("findings %+v", fs)
	}
	for _, f := range fs {
		if strings.Contains(f.msg+f.label, "s3cret") {
			t.Errorf("a value leaked: %+v", f)
		}
	}
}

func TestApparmorHint(t *testing.T) {
	if !strings.Contains(apparmorHint("1\n"), "AppArmor") {
		t.Error("no hint with the restriction on")
	}
	if apparmorHint("0\n") != "" || apparmorHint("") != "" {
		t.Error("a hint with the restriction off")
	}
}
