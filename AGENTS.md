# srtbox

README.md covers usage, configuration and the dev loop. These are the rules
for changing the code that it does not cover.

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
- bwrap fails the whole launch if a `denyWrite` path sits under another
  `denyWrite` path that does not exist yet. `ProtectRepos` drops paths that a
  protected ancestor already covers.

## GitHub Actions

- Pin every action to a full commit SHA with a `# vX.Y.Z` comment, at the
  latest release.
- Least-privilege `permissions`, set per job, each with a comment saying why.
- `persist-credentials: false` on checkout; no setup-go cache.
- Pass `${{ }}` values to scripts through `env:`, never inline in `run:`.
- `actionlint` and `zizmor --pedantic` must both come back clean.
