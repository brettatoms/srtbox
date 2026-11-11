package broker

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"

	"github.com/brettatoms/srtbox/internal/config"
)

// Program is one brokered program and its rules. A rule is a list of leading
// command words: ["aws", "logs"] matches `bz aws logs --env stg`.
type Program struct {
	Name    string     `json:"-"`
	Path    string     `json:"path"`    // the real program; found on PATH when empty
	Check   string     `json:"check"`   // host program that vets argv before it runs
	Host    [][]string `json:"host"`    // run on the host
	Approve [][]string `json:"approve"` // run on the host once the user approves
	Stdin   [][]string `json:"stdin"`   // forward the caller's input to these
	// Env holds credentials only this program gets: each variable's from
	// command runs on the host for every call, and its value never enters
	// the sandbox.
	Env map[string]Credential `json:"env"`
}

// Credential is a host command that prints a value, such as "gh auth token".
type Credential struct {
	From any `json:"from"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse reads the _broker config: program name to Program.
func Parse(raw map[string]any) (map[string]*Program, error) {
	out := map[string]*Program{}
	for _, name := range sortedKeys(raw) {
		if name == "" || name != filepath.Base(name) || name == "srtbox" {
			return nil, fmt.Errorf("_broker: %q is not a usable program name", name)
		}
		b, _ := json.Marshal(raw[name])
		p := &Program{Name: name}
		if err := json.Unmarshal(b, p); err != nil {
			return nil, fmt.Errorf("_broker.%s: %w", name, err)
		}
		if p.Path == "" {
			path, err := exec.LookPath(name)
			if err != nil {
				return nil, fmt.Errorf("_broker.%s: no path given and %s is not on PATH", name, name)
			}
			p.Path = path
		}
		p.Path = config.Home(p.Path)
		if p.Check != "" {
			p.Check = config.Home(p.Check)
		}
		for _, v := range sortedKeys(p.Env) {
			if !envName.MatchString(v) {
				return nil, fmt.Errorf("_broker.%s.env: %q is not a variable name", name, v)
			}
			if len(config.Command(p.Env[v].From)) == 0 {
				return nil, fmt.Errorf("_broker.%s.env.%s: no from command", name, v)
			}
		}
		for _, f := range []string{p.Path, p.Check} {
			if f == "" {
				continue
			}
			if !filepath.IsAbs(f) {
				return nil, fmt.Errorf("_broker.%s: %s is not an absolute path", name, f)
			}
			if _, err := os.Stat(f); err != nil {
				return nil, fmt.Errorf("_broker.%s: %w", name, err)
			}
		}
		out[name] = p
	}
	return out, nil
}

type verdict int

const (
	runLocal verdict = iota
	runHost
	runApproved
)

// classify decides where argv runs. Any matching approve rule wins, so a
// longer host rule cannot remove an approval; otherwise the longest matching
// host rule applies.
func (p *Program) classify(argv []string) (verdict, []string) {
	var best []string
	for _, r := range p.Approve {
		if leads(argv, r) && len(r) > len(best) {
			best = r
		}
	}
	if best != nil {
		return runApproved, best
	}
	for _, r := range p.Host {
		if leads(argv, r) && len(r) > len(best) {
			best = r
		}
	}
	if best != nil {
		return runHost, best
	}
	return runLocal, nil
}

// environ returns env with the program's credentials added. They are
// fetched for each call, so a value that expires stays current and none is
// kept between calls.
func (p *Program) environ(env []string) ([]string, error) {
	out := slices.Clip(env)
	for _, name := range sortedKeys(p.Env) {
		v, err := config.Fetch(config.Command(p.Env[name].From), false)
		if err != nil {
			return nil, fmt.Errorf("could not get %s: %w", name, err)
		}
		out = append(out, name+"="+v)
	}
	return out, nil
}

func (p *Program) wantsStdin(argv []string) bool {
	return slices.ContainsFunc(p.Stdin, func(r []string) bool { return leads(argv, r) })
}

// leads reports whether argv begins with exactly the rule's words. Nothing may
// come before them: srtbox cannot know which of a program's flags take a
// value, so `prog --region aws logs` must not pass for `aws logs`.
func leads(argv, rule []string) bool {
	return len(rule) > 0 && len(rule) <= len(argv) && slices.Equal(argv[:len(rule)], rule)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
