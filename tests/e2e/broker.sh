# Broker: host rules run on the host, a check hook can refuse, approve rules
# wait for `srtbox approve`, and other commands run inside.

# e2e-tool reports where it ran: only the host can write to $T/broker-out.
mkdir -p "$T/broker-out"
cat > "$T/bin/e2e-tool" <<EOF
#!/bin/sh
if echo "\$*" >> "$T/broker-out/log" 2>/dev/null; then
  echo "ran on host: \$*"
else
  echo "ran inside: \$*"
fi
EOF
cat > "$T/bin/e2e-check" <<'EOF'
#!/bin/sh
case " $* " in *" bad "*) echo "e2e-check: bad is not allowed"; exit 1 ;; esac
EOF
chmod +x "$T/bin/e2e-tool" "$T/bin/e2e-check"

programs='{"e2e-tool": {
  "path": "'"$T"'/bin/e2e-tool",
  "check": "'"$T"'/bin/e2e-check",
  "host": [["hello"]],
  "approve": [["ask"]]}}'
# On Linux the sandbox reaches the broker's socket only with
# allowAllUnixSockets; without it the client warns and runs every command
# inside.
project broker <<EOF
{"_root": "$T/broker", "network": {"allowAllUnixSockets": true}, "_broker": $programs}
EOF
project broker-nosock <<EOF
{"_root": "$T/broker-nosock", "_broker": $programs}
EOF
if [ "$os" = Linux ]; then
  check "an unreachable broker warns" "cannot reach the broker" sbx broker-nosock e2e-tool hello
fi

# Approval requests live under XDG_RUNTIME_DIR; a private one keeps
# `srtbox approve` here from seeing requests from real sessions.
export XDG_RUNTIME_DIR="$T/run"
mkdir -p -m 700 "$XDG_RUNTIME_DIR"

check "a host rule runs on the host" "ran on host: hello" sbx broker e2e-tool hello
check "the check hook can refuse" "bad is not allowed" sbx broker e2e-tool hello bad
check "other commands run inside" "ran inside: other" sbx broker e2e-tool other

# answer <reply>: waits for the session's approval request and answers it.
answer() {
  local i
  for i in $(seq 50); do
    ls "$XDG_RUNTIME_DIR"/srtbox/approvals/*.json >/dev/null 2>&1 && break
    sleep 0.2
  done
  printf '%s\n' "$1" | srtbox approve >/dev/null
}
answer o &
check "an approve rule runs once approved" "ran on host: ask" sbx broker e2e-tool ask
wait
answer d &
check "an approve rule is refused when denied" "was not approved" sbx broker e2e-tool ask
wait
