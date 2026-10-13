package launch

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/sandbox"
)

func TestSetEnv(t *testing.T) {
	home, _ := os.UserHomeDir()
	env, err := setEnv(config.Meta{
		Env:     map[string]any{"MAVEN_HOME": "~/maven", "B": "/x"},
		DenyEnv: []string{"*TOKEN*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"B=/x", "MAVEN_HOME=" + home + "/maven"}; !reflect.DeepEqual(env, want) {
		t.Errorf("got %v, want %v", env, want)
	}
}

func TestSetEnvRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		meta config.Meta
		want string
	}{
		"bad name": {config.Meta{Env: map[string]any{"A-B": "x"}}, "not a variable name"},
		"not text": {config.Meta{Env: map[string]any{"A": 1.0}}, "must be a string"},
		"withheld": {config.Meta{Env: map[string]any{"GH_TOKEN": "x"}, DenyEnv: []string{"*token*"}}, "matches _denyEnv"},
	} {
		if _, err := setEnv(c.meta); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
	if _, err := setEnv(config.Meta{
		Env:      map[string]any{"GH_TOKEN": "x"},
		DenyEnv:  []string{"*TOKEN*"},
		AllowEnv: []string{"GH_TOKEN"},
	}); err != nil {
		t.Errorf("an _allowEnv name is not withheld: %v", err)
	}
}

func TestInitHooks(t *testing.T) {
	hooks, err := initHooks(map[string]any{
		"b": "echo B=1",
		"a": []any{"printf", "A=1"},
		"c": map[string]any{"command": "sleep 1", "timeout": 0.0},
		"d": map[string]any{"command": "true", "timeout": 60.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []sandbox.Hook{
		{Name: "a", Argv: []string{"printf", "A=1"}, Timeout: hookTimeout},
		{Name: "b", Argv: []string{"sh", "-c", "echo B=1"}, Timeout: hookTimeout},
		{Name: "c", Argv: []string{"sh", "-c", "sleep 1"}, Timeout: 0},
		{Name: "d", Argv: []string{"sh", "-c", "true"}, Timeout: 60},
	}
	if !reflect.DeepEqual(hooks, want) {
		t.Errorf("got %+v\nwant %+v", hooks, want)
	}
}

func TestInitHooksRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		raw  any
		want string
	}{
		"no command":       {map[string]any{"timeout": 5.0}, "no command"},
		"empty":            {"", "no command"},
		"negative timeout": {map[string]any{"command": "true", "timeout": -1.0}, "timeout must be"},
		"text timeout":     {map[string]any{"command": "true", "timeout": "5s"}, "timeout must be"},
	} {
		if _, err := initHooks(map[string]any{"x": c.raw}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
}
