package launch

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/brettatoms/srtbox/internal/config"
)

type sshSession struct {
	dir     string   // holds the agents' sockets
	allow   []string // allowedDomains entries for the hosts
	env     []string
	cleanup func()
}

// sshTarget is one --ssh host and the key named by the --key after it.
type sshTarget struct{ host, key string }

// setupSSH opens the given hosts for the session. Each host gets its own
// throwaway ssh-agent holding only its key, so a process speaking the agent
// protocol directly still cannot use one host's key for another. The ssh
// config reaches each host through srt's proxy. Key material never enters the
// sandbox — only a signing channel to each agent — and host-key checking
// stays strict.
func setupSSH(targets []sshTarget) (*sshSession, error) {
	dir, err := os.MkdirTemp("", "srtbox-ssh-")
	if err != nil {
		return nil, err
	}
	var agents []int
	s := &sshSession{dir: dir, cleanup: func() {
		for _, pid := range agents {
			syscall.Kill(pid, syscall.SIGTERM)
		}
		os.RemoveAll(dir)
	}}
	exe, err := self()
	if err != nil {
		return s, err
	}

	var conf, knownHosts bytes.Buffer
	var hosts []string
	for i, t := range targets {
		sock := filepath.Join(dir, fmt.Sprintf("agent%d.sock", i))
		agentOut, err := exec.Command("ssh-agent", "-s", "-a", sock).Output()
		if err != nil {
			return s, fmt.Errorf("ssh-agent: %w", err)
		}
		if m := regexp.MustCompile(`SSH_AGENT_PID=(\d+)`).FindSubmatch(agentOut); m != nil {
			pid, _ := strconv.Atoi(string(m[1]))
			agents = append(agents, pid)
		}
		h, err := sshHost(t, dir, i, sock, exe)
		if err != nil {
			return s, err
		}
		conf.WriteString(h.block)
		knownHosts.Write(h.knownHosts)
		s.allow = append(s.allow, h.allow...)
		hosts = append(hosts, t.host)
	}
	os.WriteFile(filepath.Join(dir, "known_hosts"), knownHosts.Bytes(), 0o600)
	confPath := filepath.Join(dir, "config")
	os.WriteFile(confPath, conf.Bytes(), 0o600)

	// srt sets its own GIT_SSH_COMMAND inside the sandbox, so `srtbox init`
	// restores this one after it.
	s.env = []string{
		"SRTBOX_SSH_CONFIG=" + confPath,
		"SRTBOX_SSH_HOST=" + strings.Join(hosts, " "),
		"SRTBOX_GIT_SSH_COMMAND=ssh -F " + confPath,
	}
	return s, nil
}

type sshHostSetup struct {
	block      string   // the host's section of the ssh config
	knownHosts []byte   // its lines from ~/.ssh/known_hosts
	allow      []string // allowedDomains entries
}

// sshHost resolves one target through ~/.ssh/config, adds its key to the
// host's agent at sock, and returns its config block, known_hosts lines and
// allowlist entries. i numbers the host's public-key file in dir.
func sshHost(t sshTarget, dir string, i int, sock, exe string) (*sshHostSetup, error) {
	out, err := exec.Command("ssh", "-G", t.host).Output()
	if err != nil {
		return nil, fmt.Errorf("ssh -G %s: %w", t.host, err)
	}
	cfg := parseSSHConfig(out)
	hostname, port, user := cfg["hostname"][0], cfg["port"][0], cfg["user"][0]
	if hostname == "" {
		return nil, fmt.Errorf("could not resolve a hostname for %s", t.host)
	}

	key := t.key
	if key == "" {
		var found []string
		for _, k := range cfg["identityfile"] {
			if k = config.Home(k); exists(k) {
				found = append(found, k)
			}
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("%d candidate keys exist for %s; pass --key <path> after its --ssh", len(found), t.host)
		}
		key = found[0]
	}
	if !exists(key) {
		return nil, fmt.Errorf("no such key: %s", key)
	}

	// known_hosts records a non-default port as [host]:port.
	lookup := hostname
	if port != "22" {
		lookup = "[" + hostname + "]:" + port
	}
	home, _ := os.UserHomeDir()
	kh, _ := exec.Command("ssh-keygen", "-F", lookup, "-f", filepath.Join(home, ".ssh", "known_hosts")).Output()
	if len(bytes.TrimSpace(kh)) == 0 {
		return nil, fmt.Errorf("no host key for %s in ~/.ssh/known_hosts; ssh to it once outside the sandbox first", lookup)
	}

	add := exec.Command("ssh-add", "-q", key)
	add.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sock)
	add.Stdin, add.Stdout, add.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := add.Run(); err != nil {
		return nil, fmt.Errorf("ssh-add %s: %w", key, err)
	}

	// IdentitiesOnly filters the agent's keys against IdentityFile, so the
	// public half has to be present even though the private key never is.
	pub, err := exec.Command("ssh-keygen", "-y", "-f", key).Output()
	if err != nil {
		return nil, fmt.Errorf("could not derive the public key from %s", key)
	}
	pubPath := filepath.Join(dir, fmt.Sprintf("id%d.pub", i))
	os.WriteFile(pubPath, pub, 0o600)

	block := fmt.Sprintf(`Host %s
  HostName %s
  Port %s
  User %s
  IdentityAgent %s
  IdentityFile %s
  IdentitiesOnly yes
  UserKnownHostsFile %s
  StrictHostKeyChecking yes
  ProxyCommand %s ssh-proxy %%h %%p

`, t.host, hostname, port, user, sock, pubPath, filepath.Join(dir, "known_hosts"), exe)

	allow := []string{net.JoinHostPort(hostname, port)}
	if ips, err := net.LookupHost(hostname); err == nil && len(ips) > 0 && ips[0] != hostname {
		allow = append(allow, net.JoinHostPort(ips[0], port))
	}
	fmt.Fprintf(os.Stderr, "srtbox: ssh to %s (%s:%s) as %s, key %s\n", t.host, hostname, port, user, filepath.Base(key))
	return &sshHostSetup{block: block, knownHosts: kh, allow: allow}, nil
}

// parseSSHConfig reads `ssh -G` output into lowercase key → values.
func parseSSHConfig(out []byte) map[string][]string {
	cfg := map[string][]string{"hostname": {""}, "port": {"22"}, "user": {""}}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		k = strings.ToLower(k)
		if !seen[k] {
			cfg[k] = nil
			seen[k] = true
		}
		cfg[k] = append(cfg[k], v)
	}
	return cfg
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
