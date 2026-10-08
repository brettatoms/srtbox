package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestShellCommand(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		shell    string
		wantArgv []string
		wantEnv  string
	}{
		{"/bin/zsh", []string{"/bin/zsh"}, "ZDOTDIR=" + dir},
		{"/usr/bin/bash", []string{"/usr/bin/bash", "--rcfile", filepath.Join(dir, "bashrc")}, ""},
		{"/usr/bin/fish", []string{"/usr/bin/fish"}, "PS1=[srtbox:p] $ "},
	} {
		argv, env, err := shellCommand(c.shell, "p", dir)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(argv, c.wantArgv) {
			t.Errorf("%s: argv %v; want %v", c.shell, argv, c.wantArgv)
		}
		if c.wantEnv != "" && !slices.Contains(env, c.wantEnv) {
			t.Errorf("%s: env %v; want %s", c.shell, env, c.wantEnv)
		}
	}
}

// A real shell loads the user's own rc file when it can read it, then marks
// the prompt.
func TestShellPromptsWithRealShells(t *testing.T) {
	for _, c := range []struct{ shell, rc, setPrompt, print string }{
		{"bash", ".bashrc", `PS1='mine$ '`, `echo "$PS1"`},
		{"zsh", ".zshrc", `PROMPT='mine%# '`, `print -r -- "$PROMPT"`},
	} {
		t.Run(c.shell, func(t *testing.T) {
			path, err := exec.LookPath(c.shell)
			if err != nil {
				t.Skip(c.shell, "not installed")
			}
			home := t.TempDir()
			os.WriteFile(filepath.Join(home, c.rc), []byte(c.setPrompt+"\n"), 0o644)
			argv, env, err := shellCommand(path, "p", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(argv[0], append(argv[1:], "-i", "-c", c.print)...)
			cmd.Env = append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, env...)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); !strings.HasPrefix(got, "[srtbox:p] mine") {
				t.Errorf("prompt %q; want the user's prompt marked", got)
			}
		})
	}
}

func TestParseShellArgs(t *testing.T) {
	if _, _, err := parseOptions([]string{"-p", "x", "--ssh", "h"}); err != nil {
		t.Errorf("options refused: %v", err)
	}
	if code := Shell([]string{"-p", "x", "--", "ls"}); code != 2 {
		t.Errorf("shell with a command exited %d; want 2", code)
	}
}
