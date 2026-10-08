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
