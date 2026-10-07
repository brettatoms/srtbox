package launch

import (
	"encoding/json"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brettatoms/srtbox/internal/netproxy"
)

// portfileInterval is how often the host re-reads forwarded portfiles.
const portfileInterval = time.Second

// loopback scopes srt's access to the host's loopback to the declared
// _forward ports: one "127.0.0.1:<port>" allow entry each. Portfile entries
// are re-read on every update, so the allowlist follows a REPL that restarts
// on a new port.
type loopback struct {
	entries  []string
	base     string
	settings map[string]any
	fixed    []any // allowedDomains apart from the loopback entries
	ports    []int
}

// scopeLoopback adds the allow entries for the ports declared now. It must
// run after every other change to allowedDomains, which it treats as fixed.
func scopeLoopback(entries []string, base string, settings map[string]any) *loopback {
	network, _ := settings["network"].(map[string]any)
	if network == nil {
		network = map[string]any{}
		settings["network"] = network
	}
	fixed, _ := network["allowedDomains"].([]any)
	l := &loopback{entries: entries, base: base, settings: settings, fixed: slices.Clone(fixed)}
	l.update()
	return l
}

// follows reports whether any entry is a portfile, which can change.
func (l *loopback) follows() bool {
	return slices.ContainsFunc(l.entries, func(e string) bool { return strings.HasPrefix(e, "@") })
}

// update re-reads the portfiles and rewrites allowedDomains. It reports
// whether the ports changed.
func (l *loopback) update() bool {
	set := map[int]bool{}
	for _, p := range netproxy.ResolvePorts(l.entries, l.base) {
		set[p] = true
	}
	ports := slices.Sorted(maps.Keys(set))
	if l.ports != nil && slices.Equal(ports, l.ports) {
		return false
	}
	l.ports = ports
	domains := slices.Clone(l.fixed)
	for _, p := range ports {
		domains = append(domains, "127.0.0.1:"+strconv.Itoa(p))
	}
	l.settings["network"].(map[string]any)["allowedDomains"] = domains
	return true
}

// follow sends srt the whole updated settings, one JSON line, each time the
// ports change. It returns when stop is closed or srt stops reading.
func (l *loopback) follow(w io.Writer, interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if !l.update() {
			continue
		}
		b, _ := json.Marshal(l.settings)
		if _, err := w.Write(append(b, '\n')); err != nil {
			return
		}
	}
}
