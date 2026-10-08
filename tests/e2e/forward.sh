# _forward: forwarded host loopback ports, by number and through a port file,
# are reachable inside; on Linux other ports are not.
if ! command -v python3 >/dev/null || ! command -v curl >/dev/null; then
  skip "_forward" "needs python3 and curl"
  return
fi

base=$((20000 + RANDOM % 20000))
direct=$base filed=$((base + 1)) other=$((base + 2))
mkdir -p "$T/www"
echo served > "$T/www/index.txt"
pids=()
for p in $direct $filed $other; do
  python3 -m http.server --bind 127.0.0.1 --directory "$T/www" "$p" >/dev/null 2>&1 &
  pids+=($!)
done
trap 'kill "${pids[@]}" 2>/dev/null' EXIT
for p in $direct $filed $other; do
  for _ in $(seq 50); do
    curl -s "http://127.0.0.1:$p/index.txt" >/dev/null && break
    sleep 0.2
  done
done

# Seatbelt cannot scope loopback per port: macOS needs every port opened.
local_binding=false
[ "$os" = Darwin ] && local_binding=true
project fwd <<EOF
{"_root": "$T/fwd",
 "network": {"allowLocalBinding": $local_binding},
 "_forward": ["$direct", "@.port"]}
EOF
echo "$filed" > "$T/fwd/.port"

get='curl -s --noproxy "*" "http://127.0.0.1:$1/index.txt" || echo unreachable'
check "a forwarded port is reachable" served sbx fwd sh -c "$get" _ "$direct"
check "a port named in a port file is reachable" served sbx fwd sh -c "$get" _ "$filed"
if [ "$os" = Linux ]; then
  check "an unforwarded port is unreachable" unreachable sbx fwd sh -c "$get" _ "$other"
  check "why says the port is not forwarded" "this is not one" sbx fwd srtbox why "127.0.0.1:$other"
fi
