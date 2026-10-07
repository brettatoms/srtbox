package launch

import (
	"bufio"
	"bytes"
	"errors"
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
	allow   []string // allowedDomains entries for the host
	env     []string
	cleanup func()
}

// setupSSH opens one host for the session. It starts a throwaway ssh-agent
// holding only that host's key and writes an ssh config that reaches it through
// srt's proxy. Key material never enters the sandbox — only a signing channel
// for the one key — and host-key checking stays strict.
func setupSSH(host, key string) (*sshSession, error) {
	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return nil, fmt.Errorf("ssh -G %s: %w", host, err)
	}
	cfg := parseSSHConfig(out)
	hostname, port, user := cfg["hostname"][0], cfg["port"][0], cfg["user"][0]
	if hostname == "" {
		return nil, fmt.Errorf("could not resolve a hostname for %s", host)
	}

	if key == "" {
		var found []string
		for _, k := range cfg["identityfile"] {
			if k = config.Home(k); exists(k) {
				found = append(found, k)
			}
		}
		if len(found) != 1 {
			return nil, fmt.Errorf("%d candidate keys exist for %s; pass --key <path>", len(found), host)
		}
		key = found[0]
	}
	if !exists(key) {
		return nil, fmt.Errorf("no such key: %s", key)
	}

	dir, err := os.MkdirTemp("", "srtbox-ssh-")
	if err != nil {
		return nil, err
	}
	s := &sshSession{cleanup: func() { os.RemoveAll(dir) }}

	// known_hosts records a non-default port as [host]:port.
	lookup := hostname
	if port != "22" {
		lookup = "[" + hostname + "]:" + port
	}
	home, _ := os.UserHomeDir()
	kh, _ := exec.Command("ssh-keygen", "-F", lookup, "-f", filepath.Join(home, ".ssh", "known_hosts")).Output()
	if len(bytes.TrimSpace(kh)) == 0 {
		return s, fmt.Errorf("no host key for %s in ~/.ssh/known_hosts; ssh to it once outside the sandbox first", lookup)
	}
	os.WriteFile(filepath.Join(dir, "known_hosts"), kh, 0o600)

	sock := filepath.Join(dir, "agent.sock")
	agentOut, err := exec.Command("ssh-agent", "-s", "-a", sock).Output()
	if err != nil {
		return s, fmt.Errorf("ssh-agent: %w", err)
	}
	if m := regexp.MustCompile(`SSH_AGENT_PID=(\d+)`).FindSubmatch(agentOut); m != nil {
		pid, _ := strconv.Atoi(string(m[1]))
		s.cleanup = func() {
			syscall.Kill(pid, syscall.SIGTERM)
			os.RemoveAll(dir)
		}
	}
	add := exec.Command("ssh-add", "-q", key)
	add.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sock)
	add.Stdin, add.Stdout, add.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := add.Run(); err != nil {
		return s, fmt.Errorf("ssh-add %s: %w", key, err)
	}

	// IdentitiesOnly filters the agent's keys against IdentityFile, so the
	// public half has to be present even though the private key never is.
	pub, err := exec.Command("ssh-keygen", "-y", "-f", key).Output()
	if err != nil {
		return s, fmt.Errorf("could not derive the public key from %s", key)
	}
	os.WriteFile(filepath.Join(dir, "id.pub"), pub, 0o600)

	exe, err := self()
	if err != nil {
		return s, err
	}
	conf := filepath.Join(dir, "config")
	os.WriteFile(conf, []byte(fmt.Sprintf(`Host %s
  HostName %s
  Port %s
  User %s
  IdentityAgent %s
  IdentityFile %s
  IdentitiesOnly yes
  UserKnownHostsFile %s
  StrictHostKeyChecking yes
  ProxyCommand %s ssh-proxy %%h %%p
`, host, hostname, port, user, sock, filepath.Join(dir, "id.pub"), filepath.Join(dir, "known_hosts"), exe)), 0o600)

	s.allow = []string{net.JoinHostPort(hostname, port)}
	if ips, err := net.LookupHost(hostname); err == nil && len(ips) > 0 && ips[0] != hostname {
		s.allow = append(s.allow, net.JoinHostPort(ips[0], port))
	}
	// srt sets its own GIT_SSH_COMMAND inside the sandbox, so `srtbox init`
	// restores this one after it.
	s.env = []string{
		"SRTBOX_SSH_CONFIG=" + conf,
		"SRTBOX_SSH_HOST=" + host,
		"SRTBOX_GIT_SSH_COMMAND=ssh -F " + conf,
	}
	fmt.Fprintf(os.Stderr, "srtbox: ssh to %s (%s:%s) as %s, key %s\n", host, hostname, port, user, filepath.Base(key))
	return s, nil
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
	return !errors.Is(err, os.ErrNotExist)
}
