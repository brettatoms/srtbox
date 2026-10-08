package launch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/brettatoms/srtbox/internal/config"
	"github.com/brettatoms/srtbox/internal/policy"
)

// injectTimeout bounds each _inject command.
const injectTimeout = 15 * time.Second

var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// injection is one _inject entry: a host command that prints the value, and
// the hosts it may be sent to.
type injection struct {
	From  any      `json:"from"`
	Hosts []string `json:"hosts"`
}

// inject fetches each _inject value on the host and masks it inside: the
// sandbox sees a placeholder, which srt's proxy swaps for the real value only
// on HTTPS requests to the entry's hosts. credentials.files entries in mask
// mode work the same way for a file's contents. srt sees those requests only
// by terminating their TLS, so termination is switched on with every other
// allowed host excluded. It returns the environment to hand srt.
func inject(raw map[string]any, settings map[string]any) ([]string, error) {
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	allowed := policy.Strings(settings, "network", "allowedDomains")

	var env, hosts []string
	for _, name := range names {
		var in injection
		b, _ := json.Marshal(raw[name])
		if err := json.Unmarshal(b, &in); err != nil {
			return nil, fmt.Errorf("_inject.%s: %w", name, err)
		}
		argv := config.Command(in.From)
		switch {
		case !envVarName.MatchString(name):
			return nil, fmt.Errorf("_inject: %q is not a variable name", name)
		case len(argv) == 0:
			return nil, fmt.Errorf("_inject.%s: no from command", name)
		case len(in.Hosts) == 0:
			return nil, fmt.Errorf("_inject.%s: no hosts to send it to", name)
		}
		for _, h := range in.Hosts {
			if !slices.ContainsFunc(allowed, func(a string) bool { return coversHost(a, h) }) {
				return nil, fmt.Errorf("_inject.%s: %s is not in allowedDomains", name, h)
			}
		}
		val, err := fetch(argv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "srtbox: warning: _inject.%s: %v; the sandbox will not have it\n", name, err)
			continue
		}
		env = append(env, name+"="+val)
		config.SetEnvMask(settings, name, in.Hosts)
		hosts = append(hosts, in.Hosts...)
	}
	for _, f := range policy.CredentialFiles(settings) {
		if f.Mode != "mask" {
			continue
		}
		// Without injectHosts srt would send the file to every allowed host,
		// and every allowed host would have to be terminated.
		if len(f.InjectHosts) == 0 {
			return nil, fmt.Errorf("credentials.files %s: a mask entry needs injectHosts", f.Path)
		}
		for _, h := range f.InjectHosts {
			if !slices.ContainsFunc(allowed, func(a string) bool { return coversHost(a, h) }) {
				return nil, fmt.Errorf("credentials.files %s: %s is not in allowedDomains", f.Path, h)
			}
		}
		hosts = append(hosts, f.InjectHosts...)
	}
	if len(hosts) > 0 {
		terminateOnly(settings, allowed, hosts)
	}
	return env, nil
}

func fetch(argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), injectTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, config.Home(argv[0]), argv[1:]...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, os.Stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	v := strings.TrimSpace(out.String())
	if v == "" {
		return "", fmt.Errorf("%s printed nothing", strings.Join(argv, " "))
	}
	return v, nil
}

// coversHost reports whether allowedDomains pattern a admits every host that
// injection pattern h names.
func coversHost(a, h string) bool {
	if base, ok := strings.CutPrefix(h, "*."); ok {
		ab, wild := strings.CutPrefix(a, "*.")
		return a == "*" || (wild && (base == ab || strings.HasSuffix(base, "."+ab)))
	}
	return config.DomainMatch(h, 443, a)
}

// terminateOnly turns on srt's TLS termination and excludes every allowed
// host that receives no injected value, so other hosts keep end-to-end TLS.
// A wildcard entry that also covers an injection host stays terminated, since
// excluding it would exclude that host too.
func terminateOnly(settings map[string]any, allowed, hosts []string) {
	network := settings["network"].(map[string]any)
	tls, _ := network["tlsTerminate"].(map[string]any)
	if tls == nil {
		tls = map[string]any{}
	}
	var exclude []any
	if have, ok := tls["excludeDomains"].([]any); ok {
		exclude = have
	}
	for _, d := range allowed {
		// srt takes only domain names here: no "*", IP literals or ports.
		if d == "*" || strings.ContainsAny(d, "[:") || net.ParseIP(d) != nil {
			continue
		}
		if slices.ContainsFunc(hosts, func(h string) bool { return coversHost(d, h) || coversHost(h, d) }) {
			continue
		}
		if !slices.Contains(exclude, any(d)) {
			exclude = append(exclude, d)
		}
	}
	tls["excludeDomains"] = exclude
	network["tlsTerminate"] = tls
}
