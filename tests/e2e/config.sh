# _include, _requires, srtbox shell and srtbox doctor.
mkdir -p "$T/config/include" "$T/shared"
echo secret > "$T/shared/a"
cat > "$T/config/include/team.json" <<EOF2
{"filesystem": {"denyRead": ["$T/shared"]}}
EOF2
project inc <<EOF2
{"_root": "$T/inc", "_include": ["include/team.json"]}
EOF2
check "an included file's rules apply" refused sbx inc sh -c 'cat "$1" || echo refused' _ "$T/shared/a"
check "why names the included file" "denyRead \"$T/shared\" (include/team.json)" sbx inc srtbox why "$T/shared/a"

project broken <<EOF2
{"_root": "$T/broken", "_include": ["include/missing.json"]}
EOF2
check "a missing include stops the launch" "include/missing.json" sbx broken true

# A dev build skips _requires, so this one carries a version.
(cd "$here/../.." && go build -ldflags "-X main.version=v0.1.0" -o "$T/srtbox-v0.1.0" .)
project future <<EOF2
{"_root": "$T/future", "_requires": "999.0.0"}
EOF2
check "_requires refuses an older srtbox" "needs srtbox 999.0.0 or later; this is v0.1.0" \
  sh -c "cd '$T/future' && '$T/srtbox-v0.1.0' run -p future -- true"

check "shell starts a shell in the sandbox" "in inc" \
  sh -c "cd '$T/inc' && echo 'echo in \$SRTBOX_PROJECT' | SHELL=/bin/sh srtbox shell -p inc"
check "shell refuses a command" "takes no command" srtbox shell -p inc -- true

check "doctor passes a working project" "✓ sandbox starts" sh -c "cd '$T/inc' && srtbox doctor -p inc"
check "doctor reports a broken config" "✗ config broken" rc srtbox doctor -p broken
