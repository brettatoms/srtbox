package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Answers to an approval request.
const (
	answerOnce    = "once"
	answerSession = "session"
	answerDeny    = "deny"
)

// ApprovalTimeout is how long a request waits for an answer before it is
// denied.
const ApprovalTimeout = 2 * time.Minute

// ApprovalDir holds pending requests and their answers, for `srtbox approve`.
// srtbox denies the sandbox any access to it: an answer written from inside
// would approve itself.
func ApprovalDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "srtbox", "approvals")
	}
	return filepath.Join(os.TempDir(), "srtbox-"+strconv.Itoa(os.Getuid()), "approvals")
}

// pending is what a waiting request records for `srtbox approve`.
type pending struct {
	Project string `json:"project"`
	Command string `json:"command"`
	Scope   string `json:"scope"` // what "for this session" would allow
	Cwd     string `json:"cwd"`
	PID     int    `json:"pid"`
}

// notifyLimit is the longest command a desktop notification shows. A longer
// one is not offered there, since notifications cut text off and the part
// hidden could be what matters; `srtbox approve` shows it whole.
const notifyLimit = 300

// Approver asks the user about commands through a desktop notification and a
// pending file that `srtbox approve` answers; the first answer wins. "Allow
// for session" covers later calls matching the same rule, with any arguments.
type Approver struct {
	Project string
	Dir     string
	Timeout time.Duration
	// Notify shows the request and returns an answer, or "" for none.
	Notify func(ctx context.Context, title, body string) string

	mu      sync.Mutex
	granted map[string]bool
	seq     int
}

// NewApprover returns an approver using ApprovalDir and the desktop notifier.
func NewApprover(project string) *Approver {
	return &Approver{Project: project, Dir: ApprovalDir(), Timeout: ApprovalTimeout, Notify: desktopNotify}
}

// Ask reports whether the user allows program to run argv. rule is the
// approve rule that matched.
func (a *Approver) Ask(program string, rule, argv []string, cwd string) (bool, string) {
	key := program + "\x00" + strings.Join(rule, "\x00")
	// The lock covers the grants and the counter, not the wait, so one
	// unanswered request does not hold up the session's other commands.
	a.mu.Lock()
	granted := a.granted[key]
	a.seq++
	seq := a.seq
	a.mu.Unlock()
	if granted {
		return true, ""
	}

	command := display(program, argv)
	scope := display(program, rule) + " with any arguments, until the session ends"
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return false, err.Error()
	}
	base := filepath.Join(a.Dir, fmt.Sprintf("%d-%d", os.Getpid(), seq))
	b, _ := json.Marshal(pending{Project: a.Project, Command: command, Scope: scope, Cwd: cwd, PID: os.Getpid()})
	if err := os.WriteFile(base+".json", b, 0o600); err != nil {
		return false, err.Error()
	}
	defer os.Remove(base + ".json")
	defer os.Remove(base + ".answer")

	ctx, cancel := context.WithTimeout(context.Background(), a.Timeout)
	defer cancel()
	answers := make(chan string, 2)
	if a.Notify != nil && len(command) <= notifyLimit {
		go func() {
			body := command + "\nin " + cwd + "\n\n“Allow for session” allows " + scope + "."
			if ans := a.Notify(ctx, "srtbox: "+a.Project+" wants to run", body); ans != "" {
				answers <- ans
			}
		}()
	}
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if b, err := os.ReadFile(base + ".answer"); err == nil {
				answers <- strings.TrimSpace(string(b))
				return
			}
		}
	}()

	select {
	case ans := <-answers:
		switch ans {
		case answerSession:
			a.mu.Lock()
			if a.granted == nil {
				a.granted = map[string]bool{}
			}
			a.granted[key] = true
			a.mu.Unlock()
			return true, ""
		case answerOnce:
			return true, ""
		}
		return false, "denied"
	case <-ctx.Done():
		return false, "no answer within " + a.Timeout.String()
	}
}

// display renders a command for a person to judge, quoting any argument a
// shell would split or interpret.
func display(program string, argv []string) string {
	parts := []string{program}
	for _, a := range argv {
		if a == "" || strings.ContainsFunc(a, func(r rune) bool {
			return r <= ' ' || r > '~' || strings.ContainsRune(`"'\$;&|<>(){}*?!#~`+"`", r)
		}) {
			a = strconv.Quote(a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// desktopNotify asks through notify-send on Linux and a dialog on macOS. It
// returns "" when no notifier is available or the user dismisses it.
func desktopNotify(ctx context.Context, title, body string) string {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		// GNOME renders the body as markup.
		cmd = exec.CommandContext(ctx, "notify-send", "--app-name=srtbox", "--urgency=critical",
			"--icon=dialog-question", "--action=once=Allow once", "--action=session=Allow for session",
			"--action=deny=Deny", title, html.EscapeString(body))
	case "darwin":
		secs := strconv.Itoa(int(time.Until(deadline(ctx)).Seconds()))
		cmd = exec.CommandContext(ctx, "osascript",
			"-e", "on run argv",
			"-e", `set r to display dialog (item 2 of argv) with title (item 1 of argv) buttons {"Deny", "Allow for session", "Allow once"} default button "Deny" cancel button "Deny" giving up after `+secs,
			"-e", "return button returned of r",
			"-e", "end run", title, body)
	default:
		return ""
	}
	out, err := cmd.Output()
	if err != nil && !errors.As(err, new(*exec.ExitError)) {
		return ""
	}
	switch strings.TrimSpace(string(out)) {
	case "once", "Allow once":
		return answerOnce
	case "session", "Allow for session":
		return answerSession
	case "deny", "Deny":
		return answerDeny
	}
	if runtime.GOOS == "darwin" && err != nil {
		return answerDeny // the cancel button exits non-zero
	}
	return ""
}

func deadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(ApprovalTimeout)
}

// ApproveMain is `srtbox approve`: it lists waiting requests from every
// session and asks about each on the terminal.
func ApproveMain(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: srtbox approve")
		return 2
	}
	dir := ApprovalDir()
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	in := bufio.NewReader(os.Stdin)
	asked := 0
	for _, f := range files {
		base := strings.TrimSuffix(f, ".json")
		var p pending
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, &p) != nil {
			continue
		}
		if syscall.Kill(p.PID, 0) != nil {
			os.Remove(f)
			continue
		}
		if _, err := os.Stat(base + ".answer"); err == nil {
			continue
		}
		asked++
		fmt.Printf("%s wants to run:\n  %s\n  in %s\nFor this session would allow %s.\nAllow [o]nce, for this [s]ession, [d]eny, or Enter to skip: ", p.Project, p.Command, p.Cwd, p.Scope)
		line, _ := in.ReadString('\n')
		var ans string
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "o", "once":
			ans = answerOnce
		case "s", "session":
			ans = answerSession
		case "d", "deny":
			ans = answerDeny
		default:
			continue
		}
		tmp := base + ".answer.tmp"
		if err := os.WriteFile(tmp, []byte(ans), 0o600); err == nil {
			os.Rename(tmp, base+".answer")
		}
	}
	if asked == 0 {
		fmt.Println("srtbox: nothing is waiting for approval")
	}
	return 0
}
