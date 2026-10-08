# srtbox

srtbox runs a command, such as a coding agent, in an
[srt](https://github.com/anthropics/sandbox-runtime) sandbox under a
per-project policy. srt limits the filesystem and network access of one process
tree. srtbox adds layered config, protection for nested git repos, withheld
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

## Getting started

1. Create the config directory, and copy the examples into it. The project
   file's name is the project's name:

   ```
   mkdir -p ~/.config/srtbox && cd ~/.config/srtbox
   curl -fLo base.json https://raw.githubusercontent.com/brettatoms/srtbox/main/examples/base.json
   curl -fLo myproject.json https://raw.githubusercontent.com/brettatoms/srtbox/main/examples/project.json
   ```

2. In `myproject.json`, set `_root` to your project's directory, and use the
   same path in `allowRead` and `allowWrite`.
3. Check that srtbox finds the project, and review the settings that srt
   receives:

   ```
   srtbox list
   srtbox show myproject
   ```

4. From inside the project, run a command in the sandbox:

   ```
   cd ~/src/myproject && srtbox run -- ls
   ```

5. When the sandbox blocks a path, host, or environment variable that the
   command needs, run `srtbox why` on it to find the responsible rule. See
   [Why is it blocked?](#why-is-it-blocked).

## Usage

```
srtbox run [-p <project>] [--ssh <host>] [--key <path>] [--] <command> [args...]
srtbox list                       list configured projects
srtbox show [<project>]           print the settings srt would receive
srtbox why [-p <project>] <target>...   explain access to a path, host, or environment variable
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

Reads are allowed everywhere unless denied: `denyRead` is a blocklist, and
`allowRead` re-opens paths inside it. With `denyRead: ["~"]` the sandbox still
reads `/etc`, `/tmp` and anything else outside your home directory. Writes are
the reverse: denied everywhere except `allowWrite`.

Keep this directory outside every project you sandbox. A policy file inside a
project tree is writable from inside the sandbox, and the next launch would
honour whatever the sandboxed process wrote.

`${VAR}` in any string is replaced with the value of the environment variable
`VAR`. An array entry that names an unset environment variable is dropped, so
`${XDG_RUNTIME_DIR}` entries disappear on macOS rather than becoming broken
paths.

The files are plain JSON, without comments. For a note, add a key that starts
with `_`, such as `"_comment"`. srtbox ignores `_` keys that it doesn't know,
and never passes them to srt.

Keys starting with `_` are read by srtbox and never passed to srt:

| Key | Meaning |
|---|---|
| `_root` | The project tree. `run` picks the project from it when `-p` is omitted, nested repos are found under it, and srtbox warns when launched outside it. |
| `_forward` | Host loopback ports to relay in: `"3000"`, or `"@path"` for a file holding a port number, relative to `_root`, such as `.nrepl-port`. |
| `_broker` | Programs whose matching commands run on the host instead of in the sandbox. See [Broker](#broker). |
| `_mkdir` | Directories to create before launch. srt can only grant write access to a path that exists. |
| `_denyEnv` | Patterns for environment variable names (`*TOKEN*`), matched case-insensitively against the launch environment, to withhold from the sandbox. |
| `_allowEnv` | Exact environment variable names to pass through even when a `_denyEnv` pattern matches. Not patterns, and case-sensitive. |
| `_inject` | Environment variables whose values srtbox fetches on the host and gives to the sandbox as placeholders. See [Credentials](#credentials). |

[examples/](examples) has a starting `base.json` and project file. The
`base.json` follows the recipe in [Claude Code's own login](#credentials), so
create and store the token it injects before you use it. `srtbox show
<project>` prints the settings that srt receives, including what srtbox adds at
launch.

## Using srtbox with Claude Code

Claude Code needs its own files and its API hosts. In `base.json`:

```json
"network": {"allowedDomains": ["api.anthropic.com", "claude.ai"]},
"filesystem": {
  "allowRead": ["~/.claude", "~/.claude.json"],
  "allowWrite": ["~/.claude", "~/.claude.json"]
}
```

Claude Code's login doesn't reach the sandbox on macOS, and on Linux the
sandbox can read it. To give Claude a token that the sandbox never sees, follow
[Claude Code's own login](#credentials). With that token, Claude Code can't
start Remote Control sessions or use claude.ai connectors.

Start Claude from inside the project's `_root`. Outside it, srtbox warns that
the sandbox grants a different tree, and on macOS Claude exits with
`error: An unknown error occurred (Unexpected)`.

A launcher script saves retyping the flags. This one opens github.com for
pushes, and starts Claude directly when it already runs inside a session:

```sh
#!/usr/bin/env bash
set -e
cd ~/src/myproject
if [ -n "${SRTBOX_PROJECT:-}" ]; then
  exec claude "$@"
fi
exec srtbox run --ssh github.com -- claude "$@"
```

Claude Code's hooks and MCP servers run inside the sandbox too, so the
programs that they call must be readable there, for example in `~/.local/bin`.

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
masked, because the path alone is enough to use it. `--ssh` opens chosen hosts
instead. See [SSH](#ssh).

**Matching environment variables are withheld,** per `_denyEnv` less
`_allowEnv`.

**srtbox's own binary is made readable,** because it runs again inside as the
sandbox's first process.

**Each session gets its own `TMPDIR`,** a new directory that is writable inside
and removed when the session ends.

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

These environment variables are set inside a session, for scripts that need
to know where they run:

| Environment variable | Value |
|---|---|
| `SRTBOX_PROJECT` | The project's name. Set in every session, so a script can test it to tell whether it runs inside one. |
| `SRTBOX_ROOT` | The project's `_root`. |
| `SRTBOX_SSH_CONFIG` | The ssh config for the hosts that `--ssh` opened. Set only with `--ssh`. |

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

A target is a path, a host (`example.com`, `example.com:22`, a URL), or an
environment variable (`'$NAME'`, or a name in capitals). Inside a session it explains that
session; on the host it starts a session for the project (`-p`, or the one
whose `_root` holds the working directory) and asks from there. It also
recognises the cases that look like something else: a path hidden by
`denyRead` reads as missing, a write under a masked directory succeeds and is
discarded, a write grant inside a re-opened read grant stays read-only, and a
file under `credentials.files` is hidden or reads as a placeholder.

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
environment variable out, with a warning, and the launch continues.

To see inside those requests srt terminates their TLS with a per-session CA,
and points the sandbox's trust environment variables (`SSL_CERT_FILE`,
`NODE_EXTRA_CA_CERTS` and others) at it. srtbox excludes every other allowed
host from termination, so they keep end-to-end TLS. A wildcard entry that also
covers an injection host, such as `*.github.com`, stays terminated.

The placeholder is replaced where it appears literally, in a header or body.
That suits API tokens sent as `Authorization: token …` or `Bearer …`, but not
HTTP Basic auth, which base64-encodes the token first: git over HTTPS cannot
authenticate with an injected token. Use `--ssh` for git.

**Credential files.** srt's `credentials.files` protects a file. A `deny`
entry makes it unreadable and unwritable. A `mask` entry does for a file what
`_inject` does for an environment variable: on Linux the sandbox reads a
read-only copy that holds a placeholder, which srt's proxy swaps for the real
contents only in requests to `injectHosts`:

```json
"credentials": {"files": [
  {"path": "~/.config/example/token", "mode": "mask", "injectHosts": ["api.example.com"]}
]}
```

srtbox requires `injectHosts` on a `mask` entry, each host in
`allowedDomains`, and terminates TLS only for those hosts, as for `_inject`.
The placeholder replaces the whole file, which suits a file that holds only a
token; srt's `extract` option masks part of a structured file instead. srt
skips a `mask` entry whose file is missing or is a directory, and on macOS it
treats `mask` as `deny`.

**Claude Code's own login.** On macOS Claude Code keeps its login in the
keychain, which the sandbox cannot reach, so Claude inside is not logged in. On
Linux it keeps it in `~/.claude/.credentials.json`, which any sandbox that can
read `~/.claude` can read. On both, a long-lived token injected as a
placeholder works instead:

1. Run `claude setup-token` on the host in a wide terminal. A narrow one wraps
   the token across lines, and the copy picks up the breaks. The token is one
   line of 108 characters starting with `sk-ant-oat01-`. Store it in the
   desktop keyring or the keychain; each command prompts for the token, so it
   stays out of your shell history:

   ```sh
   secret-tool store --label='srtbox claude token' service srtbox-claude-token  # Linux
   security add-generic-password -a "$USER" -s srtbox-claude-token -w           # macOS
   ```

   To check it without printing it, compare its prefix and length:

   ```sh
   T="$(secret-tool lookup service srtbox-claude-token)"; [[ $T == sk-ant-oat01-* ]] && echo "prefix ok, length ${#T}"; unset T
   ```

   On macOS, use the `security find-generic-password` command below in place
   of `secret-tool lookup`.
2. Inject it, and on Linux hide the credentials file. `allowRead` wins over
   `denyRead`, so `credentials.files` is the way to hide one file inside an
   allowed directory:

   ```json
   "_inject": {
     "CLAUDE_CODE_OAUTH_TOKEN": {
       "from": "secret-tool lookup service srtbox-claude-token",
       "hosts": ["api.anthropic.com"]
     }
   },
   "credentials": {"files": [{"path": "~/.claude/.credentials.json", "mode": "deny"}]}
   ```

   On macOS, `from` is `security find-generic-password -a "$USER" -s
   srtbox-claude-token -w`.

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

A rule is a list of command words that must be the first arguments, with
nothing before them: `["aws", "logs"]` matches `bz aws logs --env stg` but not
`bz --env stg aws logs`. srtbox cannot know which of a program's flags take a
value, so it does not skip any. `host` rules run immediately. `approve` rules
ask first, and any matching `approve` rule wins over `host` rules. Anything else
runs the real program inside the sandbox as usual.

- `path` is the real program, found on `PATH` when omitted.
- `check`, if set, runs on the host with the command's arguments before
  anything else. A non-zero exit refuses the command, and the check's output
  says why. Use it for limits the command words cannot express. A rule
  without one hands the sandbox everything that program can do with those
  leading words, using your host credentials.
- `stdin` rules get the caller's input. Every other command reads `/dev/null`,
  so a brokered command never consumes input meant for something else.

Inside, a directory first on `PATH` holds a link named after each program, so
`bz …` reaches the broker while `./bin/bz …` runs the real program directly.
On Linux the link reaches the broker over a Unix socket, which needs
`"allowAllUnixSockets": true` in `network`. Without it every brokered command
runs inside the sandbox, with a warning.
Brokered commands run with the full host environment, tokens included, in the
caller's directory (kept within `_root`), on a pseudo-terminal when the caller
has one. The broker is part of the `srtbox` process that launched the session,
so it stops when the session ends.

**Approvals.** An `approve` command waits for your answer, given through a
desktop notification (`notify-send` on Linux, a dialog on macOS) or by running
`srtbox approve` in a terminal outside the sandbox. "Allow for session" covers
later commands matching the same rule with any arguments, and both prompts say
so. A command too long to show whole in a notification is offered only through
`srtbox approve`. With no answer within two minutes the command is refused. The
directory holding pending requests is denied to the sandbox, which could
otherwise answer its own.

## SSH

There is no SSH inside by default. `--ssh <host>` opens a host for the
session, and can be repeated:

```
srtbox run --ssh github.com --ssh build-box --key ~/.ssh/build claude
ssh -F "$SRTBOX_SSH_CONFIG" build-box       # inside
```

srtbox resolves each host through `~/.ssh/config`, starts a throwaway
ssh-agent per host holding only that host's key, adds each `host:port` to the
allowed domains, and writes an ssh config that reaches them through srt's
proxy. The keys never enter the sandbox, only a signing channel to each agent,
and host-key checking stays strict: each host has to be in
`~/.ssh/known_hosts` already. git uses this config automatically.
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
not only the `_forward` ones. Seatbelt fixes its rules at launch and srt has no
per-port loopback setting, so srtbox cannot narrow it. Linux needs no such
setting.

On macOS srtbox turns on srt's `allowPty` unless the config sets it: without
it Seatbelt refuses the terminal controls a full-screen program needs, and its
screen fills with the terminal's replies. Keystroke injection into your
terminal (`TIOCSTI`) is refused either way.

On macOS srtbox also turns `allowAllUnixSockets` off and allows only its own
sockets (the broker's and the `--ssh` agents'), sockets in the session's
`TMPDIR`, and `allowUnixSockets`.
Seatbelt lets a sandbox connect to a socket whose path it cannot read, so with
every socket allowed the login ssh-agent would stay usable. List any other
socket a project needs, such as Docker's, in `allowUnixSockets`.

To use `examples/base.json` on macOS, change these entries:

- Fetch Claude Code's token with `security find-generic-password -a "$USER" -s
  srtbox-claude-token -w` instead of `secret-tool`.
- Tools keep their caches under `~/Library/Caches` rather than `~/.cache`, for
  example `~/Library/Caches/go-build`. Allow the macOS paths.
- The `${XDG_RUNTIME_DIR}` entries drop out on their own, because macOS doesn't
  set that environment variable.

Some tools on macOS ignore `TMPDIR` and write to the per-user temporary
directory under `/var/folders`, which the sandbox can't write. babashka is one.
On some macOS versions, such as the macOS 26 image GitHub's runners use, so is
`mktemp` without a template; `mktemp "$TMPDIR/name.XXXXXX"` works everywhere.
Allowing that directory would expose every host process's temporary files, so
configure the tool to use `TMPDIR` instead.

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
- srt masks a denied home directory with a writable tmpfs, so a write there
  appears to succeed and is discarded when the command exits.

## Development

```
devenv shell        # Go and srt
go test ./...
tests/e2e/run.sh    # srtbox under the real sandbox, from outside a session
```

`tests/e2e/run.sh` builds srtbox, runs it with throwaway configs, and checks
what sandboxed commands see. Name files in `tests/e2e` to run only those, such
as `tests/e2e/run.sh broker`. The checks that reach hosts on the internet run
only with `SRTBOX_E2E_NETWORK=1`.

To release, push a `vX.Y.Z` tag. The release workflow builds each platform,
writes `SHA256SUMS`, attests provenance and publishes the GitHub release.

## License

MIT. See [LICENSE](LICENSE).
