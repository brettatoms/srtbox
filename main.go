// Command srtbox runs a command in an srt sandbox under a per-project policy.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/brettatoms/srtbox/internal/broker"
	"github.com/brettatoms/srtbox/internal/launch"
	"github.com/brettatoms/srtbox/internal/netproxy"
	"github.com/brettatoms/srtbox/internal/sandbox"
)

// version is set at release build time.
var version = "dev"

const usage = `srtbox runs a command in an srt sandbox under a per-project policy.

Usage:
  srtbox run [-p <project>] [--ssh <host>] [--key <path>] [--] <command> [args...]
  srtbox list                       list configured projects
  srtbox show [<project>]           print the settings srt would receive
  srtbox approve                    answer commands waiting for approval
  srtbox version

Policy lives in $XDG_CONFIG_HOME/srtbox, by default ~/.config/srtbox:
base.json applies to every project, and <project>.json overlays it.
Without -p, run and show use the project whose _root contains the working
directory.

--ssh opens one host for the session through a throwaway ssh-agent holding
only that host's key. Without it there is no SSH: the login agent is withheld.
Inside, use: ssh -F "$SRTBOX_SSH_CONFIG" <host>. git picks it up on its own.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// Inside a session, srtbox is linked under each brokered program's name.
	if name := filepath.Base(os.Args[0]); os.Getenv(broker.EnvSocket) != "" && name != selfName() {
		return broker.ClientMain(name, args)
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "version", "--version":
		fmt.Println(version)
		return 0
	case "run":
		return launch.Main(args[1:])
	case "list":
		return launch.List()
	case "show":
		if len(args) > 2 {
			fmt.Fprintln(os.Stderr, "usage: srtbox show [<project>]")
			return 2
		}
		project := ""
		if len(args) == 2 {
			project = args[1]
		}
		return launch.Show(project)
	case "approve":
		return broker.ApproveMain(args[1:])
	case "init":
		return sandbox.Init(args[1:])
	case "ssh-proxy":
		return netproxy.SSHProxyMain(args[1:])
	}
	fmt.Fprintf(os.Stderr, "srtbox: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// selfName is the file name of the running binary, whatever it was invoked as.
func selfName() string {
	exe, err := os.Executable()
	if err != nil {
		return "srtbox"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Base(exe)
}
