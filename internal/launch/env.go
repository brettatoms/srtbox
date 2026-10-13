package launch

import (
	"fmt"
	"sort"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/sandbox"
)

// setEnv turns _env into KEY=value pairs for the sandbox. A leading ~ in a
// value is expanded. A name that _denyEnv would withhold is refused: srt
// strips it anyway when the launch environment has it, and a credential
// belongs in _inject.
func setEnv(meta config.Meta) ([]string, error) {
	names := make([]string, 0, len(meta.Env))
	for n := range meta.Env {
		names = append(names, n)
	}
	sort.Strings(names)

	var env []string
	for _, name := range names {
		val, ok := meta.Env[name].(string)
		switch {
		case !envVarName.MatchString(name):
			return nil, fmt.Errorf("_env: %q is not a variable name", name)
		case !ok:
			return nil, fmt.Errorf("_env.%s: the value must be a string", name)
		case len(config.DenyEnv(meta.DenyEnv, meta.AllowEnv, []string{name + "="})) > 0:
			return nil, fmt.Errorf("_env.%s: matches _denyEnv; pass a credential with _inject", name)
		}
		env = append(env, name+"="+config.Home(val))
	}
	return env, nil
}

// hookTimeout is an _init hook's timeout in seconds unless the entry sets one.
const hookTimeout = 15

// initHooks turns _init into the hooks srtbox init runs, in name order. An
// entry is a command, as a shell string or an argv array, or an object with
// "command" and "timeout" in seconds, where 0 means no timeout.
func initHooks(raw map[string]any) ([]sandbox.Hook, error) {
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)

	var hooks []sandbox.Hook
	for _, name := range names {
		h := sandbox.Hook{Name: name, Timeout: hookTimeout}
		cmd := raw[name]
		if m, ok := cmd.(map[string]any); ok {
			cmd = m["command"]
			if t, set := m["timeout"]; set {
				n, ok := t.(float64)
				if !ok || n < 0 {
					return nil, fmt.Errorf("_init.%s: timeout must be a number of seconds, 0 for none", name)
				}
				h.Timeout = n
			}
		}
		if h.Argv = config.Command(cmd); len(h.Argv) == 0 {
			return nil, fmt.Errorf("_init.%s: no command", name)
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}
