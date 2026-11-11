# srtbox

README.md covers usage, configuration and the dev loop. These are the rules
for changing the code that it does not cover.

## What it guards against

srtbox keeps a well-intentioned agent, process or bot from reaching further
than it should. It is not proof against a malicious actor. Close an escape
when you find one, but never describe srtbox, in docs, messages or comments,
as stopping a determined attacker.

## Standard library only

No third-party modules. A dependency enters the trust path of every sandbox
this tool launches.

## No project- or user-specific content

Personal config, brokers, and helpers for particular projects live in the
user's config directory, never in this repo. `examples/` holds generic
examples only.

## One binary, two roles

srtbox is the launcher on the host and the sandbox's first process inside it
(`srtbox init`). Inside, it also runs as the broker client when invoked under a
brokered program's name.

| Package | Runs |
|---|---|
| `internal/config`, `internal/launch` | host |
| `internal/sandbox`, `internal/netproxy` | inside the sandbox |
| `internal/broker` | both: the server on the host, `ClientMain` inside |
| `internal/policy` | both: written at launch, read inside |
| `internal/why` | inside the sandbox |

The launcher hands state to `srtbox init` through `SRTBOX_*` environment
variables. Code that runs inside has no host loopback and reaches the network
only through `$HTTP_PROXY`. Anything the sandbox sends the broker is untrusted
input to code running unsandboxed on the host.

## srt behaviour the code relies on

- srt sets its own `GIT_SSH_COMMAND` inside the sandbox, overwriting the
  caller's. `--ssh` passes ours as `SRTBOX_GIT_SSH_COMMAND`, and `srtbox init`
  restores it.
- srt sets the sandbox's `TMPDIR` to `$CLAUDE_CODE_TMPDIR`, or else to
  `/tmp/claude`, which it never creates. srtbox passes a per-session directory
  as `CLAUDE_CODE_TMPDIR`.
- On Linux, srt binds an `allowWrite` path under a `denyRead` tmpfs back from
  the host, readable, if the path exists at launch; on macOS such a path is
  write-only. `srtbox why` checks `/proc/self/mountinfo` for that bind.
- srt write-protects its own names (shell rc files, `.mcp.json`, `.vscode`,
  `.idea`, `.claude/commands`, `.claude/agents`, `.git/hooks`, `.git/config`)
  differently on each platform. On macOS it denies them by pattern anywhere
  beneath the directory it started in, including paths that do not exist yet,
  so a new git worktree there cannot be checked out. On Linux it protects only
  the ones that exist at launch. On macOS srtbox therefore starts srt in the
  session directory and passes the working directory as `SRTBOX_CWD`, which
  `srtbox init` returns to. `srtbox why` reads the start directory from the
  policy's `cwd`.
- bwrap fails the whole launch if a `denyWrite` path sits under another
  `denyWrite` path that does not exist yet. `ProtectRepos` drops paths that a
  protected ancestor already covers.
- On Linux, srt denies a `denyWrite` path that does not exist by mounting
  over it, which creates an empty file at that path on the host for the whole
  session. `ProtectRepos` skips missing repo files; `ProtectAgentConfig`
  creates missing directories first and leaves missing files writable.
- On Linux, srt applies a `denyWrite` entry that is a symbolic link to where
  the link leads, not to the link, so the link can be replaced from a
  writable directory. `plantable` counts a `denyWrite` entry only when it
  covers the directory holding a link, and `ProtectAgentConfig` warns about
  linked entries.
- srt's per-command network lists (`registerCommandNetworkLists`) are in its
  library API only, and do not separate programs within one sandbox: the
  proxy username that selects a list is the client's to choose. A credential
  for one program goes through the broker's `env` instead, and never enters
  the sandbox.

## GitHub Actions

- Pin every action to a full commit SHA with a `# vX.Y.Z` comment, at the
  latest release.
- Least-privilege `permissions`, set per job, each with a comment saying why.
- `persist-credentials: false` on checkout; no setup-go cache.
- Pass `${{ }}` values to scripts through `env:`, never inline in `run:`.
- `actionlint` and `zizmor --pedantic` must both come back clean.
