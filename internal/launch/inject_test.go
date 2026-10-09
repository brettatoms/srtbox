package launch

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCoversHost(t *testing.T) {
	for _, c := range []struct {
		allowed, host string
		want          bool
	}{
		{"github.com", "github.com", true},
		{"*.github.com", "api.github.com", true},
		{"*.github.com", "*.api.github.com", true},
		{"*.github.com", "*.github.com", true},
		{"api.github.com", "*.github.com", false},
		{"*.github.com", "github.com", false},
		{"*", "*.example.com", true},
	} {
		if got := coversHost(c.allowed, c.host); got != c.want {
			t.Errorf("coversHost(%s, %s) = %v", c.allowed, c.host, got)
		}
	}
}

func TestInjectMasksAndTerminatesOnlyItsHosts(t *testing.T) {
	settings := map[string]any{
		"network": map[string]any{"allowedDomains": []any{
			"github.com", "*.github.com", "api.anthropic.com", "127.0.0.1:3020", "[::1]", "*",
		}},
		"credentials": map[string]any{"envVars": []any{map[string]any{"name": "GH_TOKEN", "mode": "deny"}}},
	}
	env, err := inject(map[string]any{
		"GH_TOKEN": map[string]any{"from": "printf ' secret\\n'", "hosts": []any{"api.github.com", "github.com"}},
		"BROKEN":   map[string]any{"from": []any{"false"}, "hosts": []any{"github.com"}},
	}, settings)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(env, []string{"GH_TOKEN=secret"}) {
		t.Errorf("env %v", env)
	}
	vars := settings["credentials"].(map[string]any)["envVars"].([]any)
	if len(vars) != 1 || vars[0].(map[string]any)["mode"] != "mask" {
		t.Errorf("GH_TOKEN should be masked, not denied: %v", vars)
	}
	exclude := settings["network"].(map[string]any)["tlsTerminate"].(map[string]any)["excludeDomains"]
	if !reflect.DeepEqual(exclude, []any{"api.anthropic.com"}) {
		t.Errorf("excludeDomains %v", exclude)
	}
}

func TestMaskedFilesTerminateOnlyTheirHosts(t *testing.T) {
	settings := map[string]any{
		"network": map[string]any{"allowedDomains": []any{"api.example.com", "github.com"}},
		"credentials": map[string]any{"files": []any{
			map[string]any{"path": "~/.token", "mode": "mask", "injectHosts": []any{"api.example.com"}},
			map[string]any{"path": "~/.secret", "mode": "deny"},
		}},
	}
	if _, err := inject(nil, settings); err != nil {
		t.Fatal(err)
	}
	exclude := settings["network"].(map[string]any)["tlsTerminate"].(map[string]any)["excludeDomains"]
	if !reflect.DeepEqual(exclude, []any{"github.com"}) {
		t.Errorf("excludeDomains %v", exclude)
	}
}

func TestTerminatingEveryAllowedHostExcludesNone(t *testing.T) {
	settings := map[string]any{"network": map[string]any{"allowedDomains": []any{"api.example.com"}}}
	if _, err := inject(map[string]any{
		"T": map[string]any{"from": "echo x", "hosts": []any{"api.example.com"}},
	}, settings); err != nil {
		t.Fatal(err)
	}
	// srt rejects a null excludeDomains.
	b, _ := json.Marshal(settings["network"].(map[string]any)["tlsTerminate"])
	if string(b) != `{"excludeDomains":[]}` {
		t.Errorf("tlsTerminate %s", b)
	}
}

func TestMaskedFilesNeedAllowedInjectHosts(t *testing.T) {
	for want, hosts := range map[string][]any{"needs injectHosts": nil, "evil.example": {"evil.example"}} {
		entry := map[string]any{"path": "~/.token", "mode": "mask"}
		if hosts != nil {
			entry["injectHosts"] = hosts
		}
		settings := map[string]any{
			"network":     map[string]any{"allowedDomains": []any{"github.com"}},
			"credentials": map[string]any{"files": []any{entry}},
		}
		_, err := inject(nil, settings)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestInjectRefusesHostsOutsideTheAllowlist(t *testing.T) {
	settings := map[string]any{"network": map[string]any{"allowedDomains": []any{"github.com"}}}
	_, err := inject(map[string]any{
		"T": map[string]any{"from": "echo x", "hosts": []any{"evil.example"}},
	}, settings)
	if err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("got %v", err)
	}
}

func TestOptionalInjectIsSkippedQuietly(t *testing.T) {
	var warnings []string
	old := warnf
	warnf = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnf = old })
	settings := map[string]any{"network": map[string]any{"allowedDomains": []any{"github.com"}}}
	env, err := inject(map[string]any{
		"OPTIONAL": map[string]any{"from": "echo noise >&2; false", "hosts": []any{"github.com"}, "optional": true},
		"EMPTY":    map[string]any{"from": "true", "hosts": []any{"github.com"}, "optional": true},
		"REQUIRED": map[string]any{"from": "false", "hosts": []any{"github.com"}},
	}, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Errorf("env %v; want nothing injected", env)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "_inject.REQUIRED") {
		t.Errorf("warnings %q; want one, for REQUIRED", warnings)
	}
}
