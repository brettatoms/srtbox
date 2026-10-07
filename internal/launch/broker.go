package launch

import (
	"net"
	"os"
	"path/filepath"

	"github.com/brettatoms/srtbox/internal/broker"
	"github.com/brettatoms/srtbox/internal/config"
)

// startBroker serves the project's brokered programs for this session. It
// returns the environment the sandbox needs to reach them: a directory first
// on PATH holding a link named after each program, and the socket. The
// approval directory is denied to the sandbox, which could otherwise answer
// its own requests.
func startBroker(project string, meta config.Meta, settings map[string]any) ([]string, func(), error) {
	programs, err := broker.Parse(meta.Broker)
	if err != nil {
		return nil, nil, err
	}
	exe, err := self()
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "srtbox-broker-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		cleanup()
		return nil, nil, err
	}
	paths := map[string]string{}
	for name, p := range programs {
		if err := os.Symlink(exe, filepath.Join(bin, name)); err != nil {
			cleanup()
			return nil, nil, err
		}
		paths[name] = p.Path
	}
	sock := filepath.Join(dir, "broker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	approvals := broker.ApprovalDir()
	os.MkdirAll(approvals, 0o700)
	config.Append(settings, []string{"filesystem", "denyRead"}, approvals)
	config.Append(settings, []string{"filesystem", "denyWrite"}, approvals)
	config.Append(settings, []string{"filesystem", "allowRead"}, dir)

	srv := &broker.Server{Programs: programs, Root: meta.Root, Env: os.Environ(), Approver: broker.NewApprover(project)}
	go srv.Serve(l)

	env := []string{
		broker.EnvSocket + "=" + sock,
		broker.EnvPrograms + "=" + jsonString(paths),
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	return env, func() { l.Close(); cleanup() }, nil
}
