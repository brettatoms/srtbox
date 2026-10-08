package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Shell starts the user's $SHELL in the sandbox: srtbox shell [-p project] [--ssh host [--key path]]...
func Shell(args []string) int {
	opts, rest, err := parseOptions(args)
	if err == nil && len(rest) > 0 {
		err = fmt.Errorf("shell takes no command; use srtbox run -- %s", strings.Join(rest, " "))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "srtbox:", err)
		return 2
	}
	return session(opts, nil)
}

// shellCommand returns the argv and environment that start shell with its
// prompt marked [srtbox:<project>]. The sandbox hides the home directory, so
// the user's rc file usually cannot load; srtbox writes its own into dir,
// which sources the user's when it is readable and then marks the prompt.
func shellCommand(shell, project, dir string) ([]string, []string, error) {
	tag := "[srtbox:" + project + "] "
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	write := func(name, text string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600)
	}
	switch filepath.Base(shell) {
	case "zsh":
		// ZDOTDIR points zsh at dir; the user's files stay where they were.
		user := `${SRTBOX_ZDOTDIR:-$HOME}`
		err := write(".zshenv", `[[ -r "`+user+`/.zshenv" ]] && source "`+user+`/.zshenv"`+"\n")
		if err == nil {
			err = write(".zshrc", `[[ -r "`+user+`/.zshrc" ]] && source "`+user+`/.zshrc"`+"\n"+
				`PROMPT="`+strings.ReplaceAll(tag, "%", "%%")+`${PROMPT}"`+"\n")
		}
		if err != nil {
			return nil, nil, err
		}
		return []string{shell}, []string{"ZDOTDIR=" + dir, "SRTBOX_ZDOTDIR=" + os.Getenv("ZDOTDIR")}, nil
	case "bash":
		rc := filepath.Join(dir, "bashrc")
		if err := write("bashrc", `[ -r "$HOME/.bashrc" ] && . "$HOME/.bashrc"`+"\n"+
			`PS1="`+strings.ReplaceAll(tag, `\`, `\\`)+`${PS1}"`+"\n"); err != nil {
			return nil, nil, err
		}
		return []string{shell, "--rcfile", rc}, nil, nil
	}
	return []string{shell}, []string{"PS1=" + tag + "$ "}, nil
}
