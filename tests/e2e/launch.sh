# Launch basics: exit status, session variables, _env and _init, list and
# show, and the per-session TMPDIR.
project launch <<EOF
{"_root": "$T/launch", "_env": {"SRTBOX_E2E_HOME": "~/e2e"}}
EOF

check "exit status passes through" "rc=3" rc sbx launch sh -c 'exit 3'
check "a signal death reports 128+N" "rc=143" rc sbx launch sh -c 'kill -TERM $$'
check "SRTBOX_PROJECT and SRTBOX_ROOT are set" "launch $T/launch" \
  sbx launch sh -c 'echo "$SRTBOX_PROJECT $SRTBOX_ROOT"'
check "_env sets a variable, expanding ~" "e2e=$HOME/e2e" \
  sbx launch sh -c 'echo "e2e=$SRTBOX_E2E_HOME"'
check "list names the project" launch srtbox list
check "show prints what srtbox adds" "\"$T/bin/srtbox\"" srtbox show launch

# mktemp gets an explicit template: on macOS a bare mktemp can ignore TMPDIR.
out=$(sbx launch sh -c 'echo "tmp=$TMPDIR"; f=$(mktemp "$TMPDIR/e2e.XXXXXX") && echo "mktemp=$f"' 2>&1)
tmp=$(printf '%s\n' "$out" | sed -n 's/^tmp=//p')
check "TMPDIR is a per-session directory" /srtbox-tmp- echo "$tmp"
check "mktemp works in TMPDIR" "mktemp=$tmp/" echo "$out"
check "TMPDIR is removed at exit" gone sh -c '[ -n "$1" ] && [ ! -e "$1" ] && echo gone' _ "$tmp"

# Outside every project's _root, run falls back to the default project, which
# grants the working directory and nothing above it.
mkdir -p "$T/loose"
out=$(cd "$T/loose" && srtbox run -- sh -c 'echo "session=$SRTBOX_PROJECT $SRTBOX_ROOT"; touch inside && echo wrote; touch ../outside 2>/dev/null || echo refused' 2>&1)
check "outside every root the default project runs" "session=default $T/loose" echo "$out"
check "the default project warns" "no project's _root contains $T/loose" echo "$out"
check "the default project can write the working directory" wrote echo "$out"
check "the default project cannot write above it" refused echo "$out"
check "the default project refuses a directory holding the config" "won't grant it" \
  sh -c 'cd "$T" && srtbox run -- true'
check "the default project refuses the home directory" "won't grant it" \
  sh -c 'cd ~ && srtbox run -- true'

project envdeny <<EOF
{"_root": "$T/envdeny", "_denyEnv": ["*TOKEN*"], "_env": {"E2E_TOKEN": "x"}}
EOF
check "_env refuses a name _denyEnv withholds" "matches _denyEnv" rc sbx envdeny true

# _init hooks run inside, in name order, and the command still runs when one
# fails or times out.
project hooks <<EOF
{"_root": "$T/hooks", "_init": {
  "a": "echo \"HOOK_A=\$SRTBOX_PROJECT\"",
  "b": "echo HOOK_B=\$HOOK_A; touch \"\$TMPDIR/hooked\"",
  "c": "exit 4",
  "d": {"command": "sleep 30", "timeout": 0.5}
}}
EOF
out=$(sbx hooks sh -c 'echo "vars=$HOOK_A,$HOOK_B"; [ -e "$TMPDIR/hooked" ] && echo "tmp=yes"' 2>&1)
check "_init sets the variables a hook prints" "vars=hooks,hooks" echo "$out"
check "_init runs inside the session" "tmp=yes" echo "$out"
check "_init warns about a failed hook" "_init.c: exit status 4" echo "$out"
check "_init warns about a timed-out hook" "_init.d: timed out after 0.5s" echo "$out"
