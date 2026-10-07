# srtbox

srtbox runs a command in an [srt](https://github.com/anthropics/sandbox-runtime)
sandbox under a per-project policy. srt enforces filesystem and network limits
on one process tree. srtbox adds what running a coding agent in it day to day
needs around that: layered config, protection for nested git repos, withheld
credentials, access to host dev servers, terminal resizing, and scoped SSH.

```
cd ~/src/myproject && srtbox run claude
```

## Install

srtbox needs srt on `PATH`. On Linux, srt also needs bubblewrap and socat; the
nixpkgs `sandbox-runtime` package includes both.

```
npm install -g @anthropic-ai/sandbox-runtime    # or nixpkgs#sandbox-runtime
```

Then install srtbox, either a release binary or from source.

**Release binary.** Each release has a static binary per platform, `SHA256SUMS`,
and a signed build-provenance attestation:

```
curl -fLO https://github.com/brettatoms/srtbox/releases/download/v0.1.0/srtbox-linux-amd64
gh attestation verify srtbox-linux-amd64 --repo brettatoms/srtbox
install -m 755 srtbox-linux-amd64 ~/.local/bin/srtbox
```

Builds are reproducible: with the Go version on the `toolchain` line of
`go.mod`, `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X
main.version=<tag>"` at the tag gives a byte-identical binary.

**From source:** `go install github.com/brettatoms/srtbox@latest`.

## Usage

```
srtbox run [-p <project>] [--ssh <host>] [--key <path>] [--] <command> [args...]
srtbox list                       list configured projects
srtbox show [<project>]           print the settings srt would receive
srtbox why [-p <project>] <target>...   explain access to a path, host or variable
srtbox approve                    answer brokered commands waiting for approval
srtbox version
```

Without `-p`, `run` and `show` use the project whose `_root` contains the
working directory, the deepest one if roots nest.

## Configuration

Policy lives in `$XDG_CONFIG_HOME/srtbox`, by default `~/.config/srtbox`
(`SRTBOX_CONFIG_DIR` overrides it). `base.json` applies to every project and
`<project>.json` overlays it: objects merge key by key, arrays are combined, and
other values from the project win. The result is srt's own
[settings format](https://github.com/anthropics/sandbox-runtime#configuration),
so everything srt supports can be set here.

Keep this directory outside every project you sandbox. A policy file inside a
project tree is writable from inside the sandbox, and the next launch would
honour whatever the sandboxed process wrote.

`${VAR}` in any string is replaced from the environment. An array entry naming
an unset variable is dropped, so `${XDG_RUNTIME_DIR}` entries disappear on macOS
rather than becoming broken paths.

Keys starting with `_` are read by srtbox and never passed to srt:

| Key | Meaning |
|---|---|
| `_root` | The project tree. `run` picks the project from it when `-p` is omitted, nested repos are found under it, and srtbox warns when launched outside it. |
| `_forward` | Host loopback ports to relay in: `"3000"`, or `"@path"` for a file holding a port number, relative to `_root`, such as `.nrepl-port`. |
| `_broker` | Programs whose matching commands run on the host instead of in the sandbox. See [Broker](#broker). |
| `_mkdir` | Directories to create before launch. srt can only grant write access to a path that exists. |
| `_denyEnv` | Variable-name patterns (`*TOKEN*`), matched case-insensitively against the launch environment, to withhold from the sandbox. |
| `_allowEnv` | Exact variable names to pass through even when a `_denyEnv` pattern matches. Not patterns, and case-sensitive. |
| `_inject` | Variables fetched on the host and given to the sandbox as placeholders. See [Credentials](#credentials). |

[examples/](examples) has a starting `base.json` and project file. `srtbox
show <project>` prints exactly what srt will receive, including what srtbox
adds at launch.

## What srtbox adds at launch

**Nested repos are protected.** srt write-protects git hooks, git config and
editor config, but only where its own scan finds them, and the scan skips
anything the enclosing repo ignores. In a workspace of checked-out repos, that
is every repo but the outer one. srtbox finds each repo and worktree under
`_root` and protects its `.git/hooks`, `.git/config`, `config.worktree`, a
worktree's `.git` pointer, the directory `core.hooksPath` names (husky's
`.husky/_` protects all of `.husky`), hook-manager config such as
`lefthook.yml`, and `.mcp.json`, `.vscode`, `.idea`, `.gitmodules`,
`.claude/commands` and `.claude/agents`. Each of these runs code on the host the
next time git, an editor or an agent opens the repo, without anyone choosing to
run it.

Hook locations are protected even before they exist. The other files are
protected only if present at launch, so a session can create one where the
repo has none. srt blocks creating a missing path by mounting over it, which
leaves an empty placeholder in the working tree for the whole session: it shows
up in `git status` and stops the directory being removed.

**The login ssh-agent is withheld.** `SSH_AUTH_SOCK` is unset and its socket
masked, since the path alone is enough to use it. `--ssh` opens chosen hosts
instead (below).

**Matching variables are withheld,** per `_denyEnv` less `_allowEnv`.

**srtbox's own binary is made readable,** because it runs again inside as the
sandbox's first process.

## Inside the sandbox

The command runs under `srtbox init`, which:

- relays the `_forward` ports from the host's loopback. On Linux the sandbox has
  its own network namespace, so the host's dev servers and REPLs are otherwise
  invisible. A port is relayed once the host is serving it, so "connection
  refused" inside still means nothing is running there, and a server started
  later is picked up within a few seconds. An `@file` entry is re-read the same
  way, so a REPL restarted on a new port stays reachable.
- relays terminal resizes. srt starts the sandbox in a new session on Linux, so
  the kernel never delivers `SIGWINCH` inside and full-screen programs keep
  drawing at their starting size.
- passes termination signals on and reports a signal death as `128+N`.

Relays go through srt's own proxy using HTTP CONNECT, which carries any TCP.
srtbox allows `127.0.0.1:<port>` for each `_forward` port and nothing else on
the host's loopback, so leave bare `127.0.0.1` and `localhost` out of
`allowedDomains`: either would open every host port, databases and daemons
included. When an `@file` port changes, srtbox sends srt the new allowlist over
its control channel (`--control-fd`) and drops the old port.

## Why is it blocked?

`srtbox why` probes access from inside the sandbox and names the rule
responsible, with the file it came from:

```
$ srtbox why ~/.aws/config example.com 127.0.0.1:8384 '$FIGMA_TOKEN'
/home/me/.aws/config
  read:  yes       allowRead "~/.aws/config" (myproject.json) re-opens denyRead "~" (base.json)
  write: no        no allowWrite rule covers it

example.com:443
  reach: no        no allowedDomains entry matches it

127.0.0.1:8384
  reach: no        the host's loopback is reachable only on _forward ports, and this is not one

$FIGMA_TOKEN
  env:   withheld  matches _denyEnv "*TOKEN*"; list it in _allowEnv to pass it through
```

A target is a path, a host (`example.com`, `example.com:22`, a URL), or a
variable (`'$NAME'`, or a name in capitals). Inside a session it explains that
session; on the host it starts a session for the project (`-p`, or the one
whose `_root` holds the working directory) and asks from there. It also
recognises the cases that look like something else: a path hidden by
`denyRead` reads as missing, a write under a masked directory succeeds and is
discarded, and a write grant inside a re-opened read grant stays read-only.

At launch srtbox records the session's merged policy, with each rule's source,
in a private session directory that the sandbox can read.

## Credentials

A token in the sandbox's environment can be read and sent anywhere the sandbox
can reach. `_inject` keeps the real value on the host:

```json
"_inject": {
  "GH_TOKEN": {"from": "gh auth token", "hosts": ["api.github.com", "github.com", "uploads.github.com"]}
}
```

At launch srtbox runs `from` on the host (a shell string or an argv array) and
hands the value to srt as a masked credential. The sandbox sees a per-session
placeholder; srt's proxy replaces it with the real value only in requests to
`hosts`, which must be in `allowedDomains`. A command that fails leaves the
variable out, with a warning.

To see inside those requests srt terminates their TLS with a per-session CA,
and points the sandbox's trust variables (`SSL_CERT_FILE`,
`NODE_EXTRA_CA_CERTS` and others) at it. srtbox excludes every other allowed
host from termination, so they keep end-to-end TLS. A wildcard entry that also
covers an injection host, such as `*.github.com`, stays terminated.

The placeholder is replaced where it appears literally, in a header or body.
That suits API tokens sent as `Authorization: token …` or `Bearer …`, but not
HTTP Basic auth, which base64-encodes the token first: git over HTTPS cannot
authenticate with an injected token. Use `--ssh` for git.

On Linux the desktop keyring is reachable over the D-Bus session bus whenever
`allowAllUnixSockets` is on. Deny the bus socket to close it, and inject what
tools used to fetch from the keyring:

```json
"filesystem": {"denyRead": ["${XDG_RUNTIME_DIR}/bus", "${XDG_RUNTIME_DIR}/keyring"]}
```

Anything inside that needs the session bus stops working, such as
`notify-send` or `secret-tool`.

## Broker

Some commands cannot work inside: a static binary with no route through the
proxy, or a command that has to change something the sandbox protects. `_broker`
names programs whose matching commands run on the host instead:

```json
"_broker": {
  "bz": {
    "path": "~/src/myproject/bin/bz",
    "check": "~/.config/srtbox/brokers/bz-check",
    "host": [["aws", "logs"], ["db", "connect"]],
    "approve": [["wt", "remove"]],
    "stdin": [["db", "connect"]]
  }
}
```

A rule is a list of leading command words; flags are skipped, and `--` ends the
words. `host` rules run straight away. `approve` rules ask first. Anything else
runs the real program inside the sandbox as usual. When rules overlap the
longest wins, and an `approve` rule wins a tie.

- `path` is the real program, found on `PATH` when omitted.
- `check`, if set, runs on the host with the command's arguments before
  anything else. A non-zero exit refuses the command, and the check's output
  says why. Use it for limits the command words cannot express.
- `stdin` rules get the caller's input. Every other command reads `/dev/null`,
  so a brokered command never consumes input meant for something else.

Inside, a directory first on `PATH` holds a link named after each program, so
`bz …` reaches the broker while `./bin/bz …` runs the real program directly.
Brokered commands run with the host environment, in the caller's directory
(kept within `_root`), on a pseudo-terminal when the caller has one. The
broker is part of the `srtbox` process that launched the session, so it lives
exactly as long as the session.

**Approvals.** An `approve` command waits for your answer, given through a
desktop notification (`notify-send` on Linux, a dialog on macOS) or by running
`srtbox approve` in a terminal outside the sandbox. "Allow for session" covers
later commands matching the same rule. With no answer within two minutes the
command is refused. The directory holding pending requests is denied to the
sandbox, which could otherwise answer its own.

## SSH

There is no SSH inside by default. `--ssh <host>` opens a host for the
session, and can be repeated:

```
srtbox run --ssh github.com --ssh build-box --key ~/.ssh/build claude
ssh -F "$SRTBOX_SSH_CONFIG" build-box       # inside
```

srtbox resolves each host through `~/.ssh/config`, starts one throwaway
ssh-agent holding only those hosts' keys, adds each `host:port` to the allowed
domains, and writes an ssh config that reaches them through srt's proxy, each
host offered only its own key. The keys never enter the sandbox, only a
signing channel to the agent, and host-key checking stays strict: each host has
to be in `~/.ssh/known_hosts` already. git uses this config automatically.
`--key` picks the identity for the `--ssh` before it, when that host has
several.

On Linux the sandbox reaches the agent over a Unix socket, which needs
`"allowAllUnixSockets": true` in `network`.

## Platforms

Linux and macOS. srt uses bubblewrap on Linux and Seatbelt on macOS, and the
two differ in ways srtbox accounts for: on macOS the sandbox shares the host's
loopback and stays attached to the terminal, so `init` does not relay ports or
resizes there.

On macOS a host dev server or REPL is reachable only with
`"allowLocalBinding": true` in `network`, and that opens every loopback port,
not just the `_forward` ones. Seatbelt fixes its rules at launch and srt has no
per-port loopback setting, so srtbox cannot narrow it. Linux needs no such
setting.

## Limits

- srt's proxy is the only way out, and only programs that honour `HTTP_PROXY`
  use it. Static binaries that resolve names and connect on their own have no
  network inside — babashka, for one. A `_broker` can run such commands on the
  host instead.
- A sandbox protects the session. It does not protect you from code the session
  wrote that you later run yourself: a test, a build script, a package
  manifest. srtbox covers what runs without anyone choosing to run it.
- With `allowAllUnixSockets`, the D-Bus session bus is reachable on Linux, and
  with it any secret stored in the desktop keyring, unless its socket is denied
  (see [Credentials](#credentials)).
- On macOS, Go programs such as `gh` verify TLS with the system keychain and
  ignore `SSL_CERT_FILE`, so an injected token reaches them only if the
  session's CA is trusted there. Untested.
- srt masks a denied home directory with a writable tmpfs, so a write there
  appears to succeed and is discarded when the command exits.

## Development

```
devenv shell        # Go and srt
go test ./...
```

To release, push a `vX.Y.Z` tag. The release workflow builds each platform,
writes `SHA256SUMS`, attests provenance and publishes the GitHub release.

## License

MIT. See [LICENSE](LICENSE).
