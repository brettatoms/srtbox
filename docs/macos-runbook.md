# srtbox on macOS: test runbook

Instructions for an agent verifying srtbox on a Mac. Work through the sections
in order, record what you observe for every check, and write the results to
`~/srtbox-test/results.md` (format at the end). The checks have been run on
Linux; macOS uses Seatbelt instead of bubblewrap, so several are expected to
differ, and finding out how is the point.

## Ground rules

- **Run unsandboxed.** You start sandboxes with `srtbox run` and inspect them
  from outside, so you must not be inside one yourself.
- **Never print a secret.** Tokens, keychain passwords and key material must
  not appear in your output or in results.md. Print only whether a value is
  set, its first 8 characters when the check says so, or an exit code. Never
  run `security dump-keychain`, `security find-generic-password -w` or
  `gh auth token` with output to the terminal.
- **Leave the user's setup alone.** Every check uses the throwaway config in
  `~/srtbox-test/config`, selected with `SRTBOX_CONFIG_DIR`. Do not edit
  `~/.config/srtbox`, `~/.ssh` or the keychain.
- **Don't fix srtbox.** If something fails, record the exact command and output
  and move on. Stop only if setup itself fails.
- Some steps need the user (a dialog to click, a window to resize). Ask them,
  and record their answer.

## 0. Setup

Record the environment:

```sh
sw_vers; uname -m
go version
node --version; npm ls -g @anthropic-ai/sandbox-runtime
```

Install srt and srtbox. `go install` fetches the Go toolchain srtbox pins if
the local one is older. `GOPROXY=direct` skips the module proxy, whose
`@latest` can lag the repository by a commit or more.

```sh
npm install -g @anthropic-ai/sandbox-runtime
GOPROXY=direct go install github.com/brettatoms/srtbox@main
export PATH="$(go env GOPATH)/bin:$PATH"
srtbox version
```

Create the test tree. The project sits under `~` so that `denyRead: ["~"]`
applies the way it does in real use:

```sh
mkdir -p ~/srtbox-test/config/brokers ~/srtbox-test/proj/bin ~/srtbox-test/outside
cd ~/srtbox-test/proj
git init -q . && git commit -q --allow-empty -m init
git init -q nested && mkdir -p nested/.vscode && echo '{}' > nested/.mcp.json
echo secret-file > ~/srtbox-test/outside/file.txt
```

`~/srtbox-test/config/base.json`:

```json
{
  "_denyEnv": ["*TOKEN*", "*_KEY", "*_SECRET", "*PASSWORD*"],
  "_allowEnv": ["SRTBOX_TEST_ALLOWED_TOKEN"],
  "network": {
    "allowedDomains": ["github.com", "*.github.com", "api.github.com", "example.com", "registry.npmjs.org",
                       "api.anthropic.com", "claude.ai", "*.claude.com", "console.anthropic.com", "statsig.anthropic.com"],
    "allowAllUnixSockets": true,
    "allowLocalBinding": true
  },
  "filesystem": {
    "denyRead": ["~"],
    "allowRead": ["~/srtbox-test/proj", "~/.gitconfig", "~/.config/gh", "~/.local/bin", "~/.claude", "~/.claude.json"],
    "allowWrite": ["~/srtbox-test/proj", "/tmp", "/private/tmp", "~/.claude", "~/.claude.json"]
  }
}
```

`~/srtbox-test/config/t.json` (srtbox expands the `~`):

```json
{
  "_root": "~/srtbox-test/proj",
  "_forward": ["47311", "@.port"],
  "_broker": {
    "hosttool": {
      "path": "~/srtbox-test/proj/bin/hosttool",
      "check": "~/srtbox-test/config/brokers/check",
      "host": [["host"], ["cat"]],
      "approve": [["risky"]],
      "stdin": [["cat"]]
    }
  }
}
```

`~/srtbox-test/proj/bin/hosttool` (make it executable). It reports whether it
can write outside the project, which only the host can:

```sh
#!/bin/sh
case "$1" in
  cat) cat ;;
  tty) [ -t 1 ] && echo tty || echo notty ;;
  *) if touch ~/srtbox-test/outside/probe 2>/dev/null; then echo "ran on host"; rm ~/srtbox-test/outside/probe; else echo "ran in sandbox"; fi ;;
esac
```

`~/srtbox-test/config/brokers/check` (make it executable):

```sh
#!/bin/sh
case "$*" in *forbidden*) echo "check: refused"; exit 1 ;; esac
```

From here on, run everything from `~/srtbox-test/proj` with:

```sh
export SRTBOX_CONFIG_DIR=~/srtbox-test/config
```

## 1. Basics

| # | Command | Expected |
|---|---|---|
| 1.1 | `srtbox list` | `t` |
| 1.2 | `srtbox show \| head` | JSON settings; no `_` keys |
| 1.3 | `srtbox run -- true; echo $?` | `0` |
| 1.4 | `srtbox run -- sh -c 'exit 7'; echo $?` | `7` |
| 1.5 | `srtbox run -- sh -c 'kill -TERM $$'; echo $?`, then the same with `kill -KILL` | `143`, then `137` |
| 1.6 | `cd /tmp && srtbox run -- true` | refuses: no project's `_root` contains `/tmp` |
| 1.7 | `cd /tmp && srtbox run -p t -- true` | runs, with a warning that cwd is outside the root |
| 1.8 | `srtbox banzai true` | `unknown command`, exit 2 |

## 2. Filesystem

Run each inside `srtbox run -- sh -c '…'`.

| # | Probe | Expected |
|---|---|---|
| 2.1 | `cat ~/srtbox-test/proj/.git/HEAD` | readable |
| 2.2 | `cat ~/srtbox-test/outside/file.txt` | not readable |
| 2.3 | `ls ~/.ssh` | not readable |
| 2.4 | `echo x > ~/srtbox-test/proj/new.txt` | succeeds |
| 2.5 | `echo x > ~/srtbox-test/outside/new.txt`, then check from outside whether the file exists | fails, or "succeeds" but the file is absent on the host. Record which: on Linux writes under a denied home land in a scratch filesystem and vanish |
| 2.6 | `echo x > /tmp/srtbox-probe` then `ls /tmp/srtbox-probe` outside | succeeds and visible on the host |
| 2.7 | `echo x > ~/.zshrc-probe` | fails |

## 3. Nested repo protection

| # | Probe inside | Expected |
|---|---|---|
| 3.1 | `touch nested/.git/hooks/pre-commit` | fails |
| 3.2 | `echo x >> nested/.git/config` | fails |
| 3.3 | `echo x >> nested/.mcp.json` | fails |
| 3.4 | `touch nested/.vscode/tasks.json` | fails |
| 3.5 | `touch nested/src.txt` | succeeds |
| 3.6 | `mkdir nested/.idea` (absent at launch) | succeeds on Linux, where only paths present at launch are protected; record whether srt blocks it on macOS |
| 3.7 | While a session runs (`srtbox run -- sleep 20 &`), run `git -C nested status --short` outside | no unexpected empty files such as `.gitmodules` or `.claude/commands` |

Clean up 3.5/3.6 afterwards.

## 4. Environment

```sh
export SRTBOX_TEST_SECRET_TOKEN=aaaa SRTBOX_TEST_ALLOWED_TOKEN=bbbb HOST_AGENT="$SSH_AUTH_SOCK"
```

| # | Probe inside | Expected |
|---|---|---|
| 4.1 | `echo ${SRTBOX_TEST_SECRET_TOKEN:-unset}` | `unset` |
| 4.2 | `echo ${SRTBOX_TEST_ALLOWED_TOKEN:-unset}` | `bbbb` |
| 4.3 | `echo ${SSH_AUTH_SOCK:-unset}` | `unset` |
| 4.4 | `SSH_AUTH_SOCK="$HOST_AGENT" ssh-add -l; echo $?` | exit 2, cannot connect. **Important:** on macOS srtbox allows only its own Unix sockets plus `allowUnixSockets`. Exit 1 ("The agent has no identities") or a key list means the login agent is reachable. |

## 5. Network

| # | Probe inside | Expected |
|---|---|---|
| 5.1 | `curl -s -o /dev/null -w '%{http_code}' https://example.com` | `200` |
| 5.2 | `curl -s -o /dev/null -w '%{http_code}' https://www.google.com` | `000` or `403` (refused) |
| 5.3 | `curl -s -o /dev/null -w '%{http_code}' https://api.github.com` | `200` |

## 6. Host loopback

macOS shares the host's loopback with the sandbox; srtbox cannot narrow it
there. These checks record what actually happens.

```sh
python3 -m http.server 47311 --bind 127.0.0.1 >/dev/null 2>&1 &   # declared
python3 -m http.server 47399 --bind 127.0.0.1 >/dev/null 2>&1 &   # undeclared
```

| # | Probe inside | Expected |
|---|---|---|
| 6.1 | `curl -s -o /dev/null -w '%{http_code}' --noproxy '*' http://127.0.0.1:47311/` | `200` |
| 6.2 | the same for `47399` | `200` with `allowLocalBinding: true` (known limitation) |
| 6.3 | Set `"allowLocalBinding": false` in base.json, repeat 6.1 and 6.2 | record both; restore `true` afterwards |
| 6.4 | `srtbox why 127.0.0.1:47311 127.0.0.1:47399` (inside) | explains that the sandbox shares the host's loopback |

Stop both servers afterwards.

## 7. Broker

| # | Command inside | Expected |
|---|---|---|
| 7.1 | `command -v hosttool` | a path in a `srtbox-session-*` directory |
| 7.2 | `hosttool host` | `ran on host` |
| 7.3 | `hosttool other` | `ran in sandbox` (no rule: runs locally) |
| 7.4 | `hosttool host forbidden; echo $?` | `check: refused`, exit 126 |
| 7.5 | `echo hello \| hosttool cat` | `hello` (stdin rule) |
| 7.6 | `printf 'a\nb\n' \| { hosttool host; cat; }` | `ran on host`, then `a` and `b`: the brokered command did not swallow the input |
| 7.7 | `srtbox run -- hosttool tty` from an interactive terminal (ask the user to run it) | `tty` |
| 7.8 | `hosttool risky` | prints `waiting for approval`; a dialog appears. Ask the user to click **Deny**; expect exit 126 "denied" |
| 7.9 | `hosttool risky` again, and this time answer from a second terminal outside: `srtbox approve`, then `o` | `ran on host` |
| 7.10 | Outside first: `export HOST_TMP="$TMPDIR"` (srt sets a different `TMPDIR` inside). Inside: `ls "$HOST_TMP"/srtbox-$(id -u)/approvals; touch "$HOST_TMP"/srtbox-$(id -u)/approvals/x` | not listable, not writable. **Important:** a writable approvals directory would let the sandbox approve its own requests |
| 7.11 | `hosttool risky` with no answer for 2 minutes | refused: "no answer within 2m0s" |

## 8. SSH

Needs a GitHub key in `~/.ssh/config` and github.com in `~/.ssh/known_hosts`.

| # | Command | Expected |
|---|---|---|
| 8.1 | `srtbox run --ssh github.com -- git ls-remote git@github.com:brettatoms/srtbox.git HEAD` | a commit hash |
| 8.2 | `srtbox run -- git ls-remote git@github.com:brettatoms/srtbox.git HEAD` | fails: no SSH without `--ssh` |
| 8.3 | If the user has a second SSH host: `srtbox run --ssh github.com --ssh <host> -- sh -c 'ssh -F "$SRTBOX_SSH_CONFIG" <host> true; echo $?'` | `0` |
| 8.4 | After each run: `pgrep -lf 'ssh-agent -s -a'` | nothing left from srtbox |

## 9. Keychain and injected credentials

macOS has no D-Bus; the question is whether the sandbox can reach the login
keychain, and whether an injected token works.

| # | Probe | Expected |
|---|---|---|
| 9.1 | Inside: `security find-generic-password -s 'gh:github.com' >/dev/null 2>&1; echo $?` | record the exit code. `0` means the sandbox can read keychain items (attributes only here; do not add `-w`) |
| 9.2 | Inside: `gh auth status 2>&1 \| head -n 3` | record whether gh is logged in, and from where (keyring, token) |
| 9.3 | Add to t.json: `"_inject": {"GH_TOKEN": {"from": "gh auth token", "hosts": ["api.github.com", "github.com"]}}`. Inside: `echo "${GH_TOKEN:0:8}"` | `fake_val` (a placeholder) |
| 9.4 | Inside, with 9.3: `curl -s -o /dev/null -w '%{http_code}' -H "Authorization: token $GH_TOKEN" https://api.github.com/user` | `200` if curl trusts srt's session CA (srt sets `CURL_CA_BUNDLE` and `SSL_CERT_FILE`); record the code, and curl's error if it is `000` |
| 9.5 | Inside, with 9.3: `gh api user -q .login` | record. Expected to **fail** with a certificate error: Go on macOS verifies TLS with the system keychain and ignores `SSL_CERT_FILE` |
| 9.6 | `srtbox why GH_TOKEN` (inside, with 9.3) | `masked`, listing the two hosts |

Remove `_inject` afterwards.

## 10. srtbox why

| # | Command | Expected |
|---|---|---|
| 10.1 | `srtbox why ~/srtbox-test/outside/file.txt nested/.git/config example.com www.google.com SRTBOX_TEST_SECRET_TOKEN` (outside, so it starts its own session) | one explanation each, naming the rule and `base.json`/`t.json`/"added by srtbox" |
| 10.2 | The same command inside `srtbox run -- …` | same answers |

## 11. Sessions and cleanup

| # | Check | Expected |
|---|---|---|
| 11.1 | Run two sessions at once: `srtbox run -- sleep 10 & srtbox run -- sh -c 'hosttool host'; wait` | both work |
| 11.2 | Afterwards: `ls -d "$TMPDIR"/srtbox-* /tmp/srtbox-* 2>/dev/null` | only `srtbox-<uid>` (the approvals parent), no session, ssh or settings files |
| 11.3 | Ask the user to run `srtbox run -- vim` (or `htop`) and resize the window | redraws at the new size |
| 11.4 | Ask the user to run `srtbox run -p t -- claude` and try a few commands | Claude starts and works. Record any garbled text, such as stray escape sequences or dropped characters, with a screenshot if they can |

## 12. Clean up

```sh
pkill -f 'http.server 473' ; rm -rf ~/srtbox-test
```

## Second pass

After a first run, the user may ask for a second pass instead of the full
runbook. Do setup (section 0), then only these checks, and write
`~/srtbox-test/results-2.md` in the same format:

- 1.5: signal deaths report `143` and `137`.
- 2.2 with `srtbox why ~/srtbox-test/outside/file.txt`: says `denyRead "~"`
  without "looks missing".
- 4.4: the login agent is unreachable.
- 7.1 to 7.11: the broker still works with the socket allowlist.
- 8.1, 8.2, 8.4: `--ssh` still works with the socket allowlist.
- 9.2 to 9.6: with `~/.config/gh` readable, whether `gh api user -q .login`
  works with an injected token. If it fails, record the exact error; a
  certificate error answers the open question.
- 11.4: Claude inside the sandbox, including whether the terminal output is
  garbled.

## Third pass

Do setup (section 0) first. Write `~/srtbox-test/results-3.md`. Steps marked
**user** need an interactive terminal: ask the user to run them and report
what they see.

**T1. Keystroke injection (decides how srtbox handles terminals on macOS).**
`allowPty` lets a sandboxed process control terminal devices. The question is
whether it can then push keystrokes into the user's terminal with `TIOCSTI`,
which the shell would run after the session ends. The payload is harmless and
has no newline, so nothing runs. It only appears at the next prompt.

Write the probe to `~/srtbox-test/proj/inject.py`:

```python
import fcntl, sys, termios
try:
    for c in b"echo INJECTED":
        fcntl.ioctl(sys.stdin.fileno(), termios.TIOCSTI, bytes([c]))
    print("ioctl succeeded")
except OSError as e:
    print("ioctl failed:", e)
```

| # | Step | Record |
|---|---|---|
| T1.1 | **user**: with `allowPty` absent from base.json, `srtbox run -- python3 inject.py` | the exit status and error, and whether `echo INJECTED` appears at the next shell prompt (clear it with Ctrl-C) |
| T1.2 | **user**: add `"allowPty": true` at the top level of base.json, next to `network` and `filesystem`, and repeat | same. **Important:** text at the prompt means the sandbox can type into the user's shell |
| T1.3 | **user**: with `allowPty` still true, `script -q /dev/null srtbox run -- python3 inject.py` | same. Here the sandbox has a private terminal from `script`, so text should *not* reach the user's prompt |
| T1.4 | `script -q /dev/null srtbox run -- sh -c 'exit 7'; echo $?` | the exit status: does `script` pass `7` through |
| T1.5 | **user**: with `allowPty` true, `srtbox run -p t -- claude`, then again as `script -q /dev/null srtbox run -p t -- claude` | whether the escape-sequence garbage from 11.4 is gone in each. Claude may still be not logged in until T3 |

Remove `allowPty` afterwards.

**T2. Symlinked allow paths.**

```sh
mkdir -p ~/srtbox-test/outside/real ~/srtbox-test/outside/secret
echo hi > ~/srtbox-test/outside/real/f; echo secret > ~/srtbox-test/outside/secret/f
ln -s ~/srtbox-test/outside/real ~/srtbox-test/linked
ln -s ~/srtbox-test/outside/secret ~/srtbox-test/proj/planted
```

Add `"~/srtbox-test/linked"` and `"~/srtbox-test/proj/planted"` to
`allowRead` in base.json.

| # | Check | Expected |
|---|---|---|
| T2.1 | `srtbox run -- cat ~/srtbox-test/linked/f` | `hi`: srtbox also allows the link's target |
| T2.2 | Launch output of T2.1 | a warning that `planted` is not followed because it sits in a writable path |
| T2.3 | `srtbox run -- cat ~/srtbox-test/proj/planted/f` | fails |
| T2.4 | `srtbox why ~/srtbox-test/proj/planted/f` | a `link: ->` line naming the target, and `denyRead "~"` as the reason |

**T3. Claude's login through an injected token.**

| # | Step | Expected |
|---|---|---|
| T3.1 | **user**: `claude setup-token`, then store the token with `security add-generic-password -a "$USER" -s srtbox-claude-token -w` (paste at the prompt) | stored; never print it |
| T3.2 | Add to t.json: `"_inject": {"CLAUDE_CODE_OAUTH_TOKEN": {"from": "security find-generic-password -a \"$USER\" -s srtbox-claude-token -w", "hosts": ["api.anthropic.com"]}}`. Inside: `echo "${CLAUDE_CODE_OAUTH_TOKEN:0:8}"` | `fake_val` |
| T3.3 | `srtbox run -- claude -p 'reply with the single word ok'` | `ok`. If it fails, record the exact error: Claude may reject the placeholder before sending it |
| T3.4 | **user**: delete the stored token afterwards if they don't want to keep it (`security delete-generic-password -s srtbox-claude-token`) | |

**T4. Version.** `srtbox version` prints a module version such as
`v0.0.0-2026…-<commit>`, not `dev`.

## Fourth pass

Do setup (section 0), then write `~/srtbox-test/results-4.md`. Leave
`allowPty` out of every config: srtbox now turns it on for macOS itself.

| # | Step | Expected |
|---|---|---|
| F1 | `srtbox show \| grep allowPty` | `"allowPty": true` |
| F2 | **user**: `srtbox run -p t -- claude` | a clean screen, no escape-sequence garbage |
| F3 | T2 from the third pass, with its fixed setup (the two links now have different targets) | T2.1 to T2.4 as listed |
| F4 | **user**, on the host, outside any sandbox: `security find-generic-password -a "$USER" -s srtbox-claude-token -w \| wc -c` | the token's length only. Compare it with the token `claude setup-token` printed: a different length means extra text, such as spaces or quotes, was stored with it |
| F5 | **user**, on the host: `CLAUDE_CODE_OAUTH_TOKEN="$(security find-generic-password -a "$USER" -s srtbox-claude-token -w)" claude -p 'reply with the single word ok'` | `ok`. A 401 here means the stored token itself is bad: create a new one with `claude setup-token`, store it again, and repeat |
| F6 | **user**, with the `_inject` entry from T3.2: `srtbox run -- sh -c 'curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $CLAUDE_CODE_OAUTH_TOKEN" -H "anthropic-version: 2023-06-01" -H "anthropic-beta: oauth-2025-04-20" https://api.anthropic.com/v1/models'` | `200` means srt swaps the token in. `401` with F5 passing means it does not reach this request |
| F7 | **user**: `srtbox run -- claude -p 'reply with the single word ok'` | `ok` |

## Results format

Write `~/srtbox-test/results.md` (copy it somewhere outside `~/srtbox-test`
before cleaning up) as:

```
# srtbox macOS results
macOS <version>, <arch>; srt <version>; srtbox <version>; go <version>

| # | Result | Observed |
|---|---|---|
| 1.1 | pass | t |
| 4.4 | FAIL | ssh-add listed 2 keys |
...

## Notes
Anything surprising, with the exact command and output.
```

Mark each check `pass`, `FAIL` (differs from Expected), or `info` (no fixed
expectation, observation recorded). Put the important ones first in the notes:
4.4, 7.10, 9.1 and 9.5.
