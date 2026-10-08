# Launch basics: exit status, session variables, list and show, and the
# per-session TMPDIR.
project launch <<EOF
{"_root": "$T/launch"}
EOF

check "exit status passes through" "rc=3" rc sbx launch sh -c 'exit 3'
check "a signal death reports 128+N" "rc=143" rc sbx launch sh -c 'kill -TERM $$'
check "SRTBOX_PROJECT and SRTBOX_ROOT are set" "launch $T/launch" \
  sbx launch sh -c 'echo "$SRTBOX_PROJECT $SRTBOX_ROOT"'
check "list names the project" launch srtbox list
check "show prints what srtbox adds" "\"$T/bin/srtbox\"" srtbox show launch

# mktemp gets an explicit template: on macOS a bare mktemp can ignore TMPDIR.
out=$(sbx launch sh -c 'echo "tmp=$TMPDIR"; f=$(mktemp "$TMPDIR/e2e.XXXXXX") && echo "mktemp=$f"' 2>&1)
tmp=$(printf '%s\n' "$out" | sed -n 's/^tmp=//p')
check "TMPDIR is a per-session directory" /srtbox-tmp- echo "$tmp"
check "mktemp works in TMPDIR" "mktemp=$tmp/" echo "$out"
check "TMPDIR is removed at exit" gone sh -c '[ -n "$1" ] && [ ! -e "$1" ] && echo gone' _ "$tmp"
