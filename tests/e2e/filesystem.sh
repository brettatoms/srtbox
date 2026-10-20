# Filesystem policy: allowWrite, denyRead and allowRead, and nested-repo
# protection.
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
  check "a nested repo's .git/config is read-only" refused \
    sbx fs sh -c 'echo x >> inner/.git/config || echo refused'
  check "a nested repo's hooks can't be created" refused \
    sbx fs sh -c 'echo x > inner/.git/hooks/pre-commit || echo refused'
  check "a nested repo's .mcp.json is read-only" refused \
    sbx fs sh -c 'echo x >> inner/.mcp.json || echo refused'
  check "a nested repo's other files are writable" written \
    sbx fs sh -c 'echo x > inner/file && echo written'
else
  skip "nested repos" "git is not installed"
fi
