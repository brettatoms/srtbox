package launch

import (
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

func TestInjectRefusesHostsOutsideTheAllowlist(t *testing.T) {
	settings := map[string]any{"network": map[string]any{"allowedDomains": []any{"github.com"}}}
	_, err := inject(map[string]any{
		"T": map[string]any{"from": "echo x", "hosts": []any{"evil.example"}},
	}, settings)
	if err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("got %v", err)
	}
}
