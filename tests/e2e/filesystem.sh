# Filesystem policy: allowWrite, denyRead and allowRead, nested-repo and
# agent-config protection, and the hidden runtime directory.
project fs <<EOF
{"_root": "$T/fs",
 "filesystem": {
   "allowWrite": ["$T/fs"],
   "denyRead": ["$T/fs/private"],
   "allowRead": ["$T/fs/private/open"]}}
EOF
mkdir -p "$T/outside" "$T/fs/private/open"
echo private > "$T/fs/private/a"
echo open > "$T/fs/private/open/b"

check "writes inside allowWrite succeed" written sbx fs sh -c 'echo x > new && echo written'
check "writes outside allowWrite fail" refused \
  sbx fs sh -c 'echo x > "$1" || echo refused' _ "$T/outside/f"
check "denyRead hides a path" refused sbx fs sh -c 'cat private/a || echo refused'
check "allowRead re-opens a path inside it" open sbx fs cat private/open/b
check "why names the denyRead rule" "denyRead \"$T/fs/private\" (fs.json)" \
  sbx fs srtbox why private/a

# An allowWrite path under denyRead that no allowRead covers: srt on Linux
# binds it back from the host, readable, and on macOS makes it write-only.
project wo <<EOF
{"_root": "$T/wo",
 "filesystem": {
   "denyRead": ["$T/wo/home"],
   "allowWrite": ["$T/wo/home/cache", "$T/wo/home/later"]}}
EOF
mkdir -p "$T/wo/home/cache"
echo host-token > "$T/wo/home/cache/token"

sbx wo sh -c 'echo x > home/cache/new'
check "a write under denyRead reaches the host" x cat "$T/wo/home/cache/new"
if [ "$os" = Linux ]; then
  check "on Linux, allowWrite re-opens a denyRead path" host-token sbx wo cat home/cache/token
  check "why says allowWrite re-opens it on Linux" \
    "allowWrite \"$T/wo/home/cache\" (wo.json) re-opens denyRead \"$T/wo/home\" (wo.json) on Linux" \
    sbx wo srtbox why home/cache
  check "on Linux, an allowWrite path made after launch is scratch" "read:  scratch" \
    sbx wo sh -c 'mkdir home/later && srtbox why home/later'
else
  check "on macOS, allowWrite leaves a denyRead path unreadable" refused \
    sbx wo sh -c 'cat home/cache/token || echo refused'
  check "why says allowWrite grants writes only on macOS" \
    "allowWrite \"$T/wo/home/cache\" (wo.json) grants writes only on macOS" \
    sbx wo srtbox why home/cache
fi
check "doctor warns that reads differ by platform" "⚠ allowWrite \"$T/wo/home/cache\"" \
  sh -c "cd '$T/wo' && srtbox doctor -p wo"

if command -v git >/dev/null; then
  git init -q "$T/fs/inner"
  echo '{}' > "$T/fs/inner/.mcp.json"
  mkdir "$T/fs/inner/.idea"
  check "a nested repo's .git/config is read-only" refused \
    sbx fs sh -c 'echo x >> inner/.git/config || echo refused'
  check "a nested repo's hooks can't be created" refused \
    sbx fs sh -c 'echo x > inner/.git/hooks/pre-commit || echo refused'
  check "a nested repo's .mcp.json is read-only" refused \
    sbx fs sh -c 'echo x >> inner/.mcp.json || echo refused'
  check "a nested repo's other files are writable" written \
    sbx fs sh -c 'echo x > inner/file && echo written'
  check "a nested repo's .idea is read-only" refused \
    sbx fs sh -c 'echo x > inner/.idea/f || echo refused'
  # A new git worktree checks out its own .idea and .claude/agents.
  check "an .idea made in the session is writable" written \
    sbx fs sh -c 'mkdir -p wt/.idea && echo x > wt/.idea/f && echo written'
  check "a .claude/agents made in the session is writable" written \
    sbx fs sh -c 'mkdir -p wt/.claude/agents && echo x > wt/.claude/agents/a.md && echo written'
else
  skip "nested repos" "git is not installed"
fi

# Claude Code's config: srtbox denies writes to what Claude Code loads outside
# the sandbox, and leaves the rest of its directory writable. On Linux a
# missing directory is created first and denied; a missing file stays
# writable, since srt could deny it only by creating it on the host.
export CLAUDE_CONFIG_DIR="$T/claude"
mkdir -p "$T/claude/hooks" "$T/skills-repo"
echo '{}' > "$T/claude/settings.json"
ln -s "$T/skills-repo" "$T/claude/skills"
project agent <<EOF
{"_root": "$T/agent", "filesystem": {"allowWrite": ["$T/claude"]}}
EOF
project agent-off <<EOF
{"_root": "$T/agent-off", "_protectAgentConfig": false, "filesystem": {"allowWrite": ["$T/claude"]}}
EOF
check "agent settings are read-only" refused \
  sbx agent sh -c 'echo x >> "$CLAUDE_CONFIG_DIR/settings.json" || echo refused'
check "agent hooks can't be added" refused \
  sbx agent sh -c 'echo x > "$CLAUDE_CONFIG_DIR/hooks/h" || echo refused'
check "a missing agent directory can't be made" refused \
  sbx agent sh -c 'mkdir -p "$CLAUDE_CONFIG_DIR/agents" && echo x > "$CLAUDE_CONFIG_DIR/agents/a.md" || echo refused'
check "the rest of the agent's directory is writable" written \
  sbx agent sh -c 'mkdir -p "$CLAUDE_CONFIG_DIR/projects/p" && echo x > "$CLAUDE_CONFIG_DIR/projects/p/m.md" && echo written'
check "why says Claude Code loads it" "Claude Code loads it outside the sandbox" \
  sbx agent srtbox why "$T/claude/settings.json"
check "_protectAgentConfig false leaves it writable" written \
  sbx agent-off sh -c 'echo "{}" > "$CLAUDE_CONFIG_DIR/settings.json" && echo written'
if [ "$os" = Linux ]; then
  check "on Linux, a missing agent directory is created as a directory" dir \
    sh -c "[ -d '$T/claude/agents' ] && echo dir"
  check "on Linux, a missing agent file is left writable" written \
    sbx agent sh -c 'echo x > "$CLAUDE_CONFIG_DIR/loop.md" && echo written'
  rm -f "$T/claude/loop.md"
  check "on Linux, doctor names the missing agent files" "not protected until they exist: " \
    sh -c "cd '$T/agent' && srtbox doctor -p agent"
  check "on Linux, a linked agent entry warns" "skills is a symbolic link" sbx agent true
else
  check "on macOS, a missing agent file can't be made" refused \
    sbx agent sh -c 'echo x > "$CLAUDE_CONFIG_DIR/loop.md" || echo refused'
fi
unset CLAUDE_CONFIG_DIR

# $XDG_RUNTIME_DIR holds the login session's sockets; on Linux srtbox hides
# it, and allowRead re-opens a path under it.
if [ "$os" = Linux ]; then
  export XDG_RUNTIME_DIR="$T/runtime"
  mkdir -p -m 700 "$XDG_RUNTIME_DIR/open"
  echo secret > "$XDG_RUNTIME_DIR/bus"
  echo open > "$XDG_RUNTIME_DIR/open/f"
  project runtime <<EOF
{"_root": "$T/runtime-proj", "filesystem": {"allowRead": ["$T/runtime/open"]}}
EOF
  check "on Linux, the runtime directory is hidden" refused \
    sbx runtime sh -c 'cat "$XDG_RUNTIME_DIR/bus" || echo refused'
  check "on Linux, allowRead re-opens a path in the runtime directory" open \
    sbx runtime sh -c 'cat "$XDG_RUNTIME_DIR/open/f"'
  check "why says what the runtime directory holds" "the login session's sockets" \
    sbx runtime srtbox why "$T/runtime/bus"
fi
