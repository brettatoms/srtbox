# Credentials: credentials.files deny and mask, _inject, and withheld
# environment variables.
project creds <<EOF
{"_root": "$T/creds",
 "filesystem": {"allowWrite": ["$T/creds"]},
 "network": {"allowedDomains": ["httpbin.org"]},
 "credentials": {"files": [
   {"path": "$T/creds/denied.txt", "mode": "deny"},
   {"path": "$T/creds/masked.txt", "mode": "mask", "injectHosts": ["httpbin.org"]}]},
 "_inject": {
   "E2E_INJECTED": {"from": "printf e2e-injected-value", "hosts": ["httpbin.org"]},
   "E2E_BROKEN": {"from": "false", "hosts": ["httpbin.org"]}},
 "_denyEnv": ["E2E_*_TOKEN"],
 "_allowEnv": ["E2E_KEEP_TOKEN"]}
EOF
printf e2e-denied-value > "$T/creds/denied.txt"
printf e2e-masked-value > "$T/creds/masked.txt"
export E2E_DROP_TOKEN=drop E2E_KEEP_TOKEN=keep SSH_AUTH_SOCK=/nonexistent/agent.sock

check "a deny entry hides the file" refused sbx creds sh -c 'cat denied.txt || echo refused'
check "why names the deny entry" "hidden by credentials.files \"$T/creds/denied.txt\" (creds.json)" \
  sbx creds srtbox why denied.txt
if [ "$os" = Linux ]; then
  check "a mask entry shows a placeholder" placeholder \
    sbx creds sh -c 'c=$(cat masked.txt) && [ -n "$c" ] && [ "$c" != e2e-masked-value ] && echo placeholder'
  check "a masked file is read-only" refused sbx creds sh -c 'echo x >> masked.txt || echo refused'
  check "why reports the masked file" "read:  masked    credentials.files" \
    sbx creds srtbox why masked.txt
else
  check "a mask entry acts as deny on macOS" refused sbx creds sh -c 'cat masked.txt || echo refused'
  check "why says mask acts as deny" "srt treats mask as deny on macOS" \
    sbx creds srtbox why masked.txt
fi
check "the host's masked file is unchanged" e2e-masked-value cat "$T/creds/masked.txt"

check "_inject gives the sandbox a placeholder" placeholder \
  sbx creds sh -c '[ -n "$E2E_INJECTED" ] && [ "$E2E_INJECTED" != e2e-injected-value ] && echo placeholder'
check "a failing _inject command warns" "warning: _inject.E2E_BROKEN" sbx creds true
check "a failing _inject command leaves the variable out" broken=unset \
  sbx creds sh -c 'echo "broken=${E2E_BROKEN-unset}"'

check "_denyEnv withholds matching variables" drop=unset \
  sbx creds sh -c 'echo "drop=${E2E_DROP_TOKEN-unset}"'
check "_allowEnv passes an exempt name through" keep=keep \
  sbx creds sh -c 'echo "keep=${E2E_KEEP_TOKEN-unset}"'
check "why names the _denyEnv pattern" 'matches _denyEnv "E2E_*_TOKEN"' \
  sbx creds srtbox why '$E2E_DROP_TOKEN'
check "SSH_AUTH_SOCK is withheld" sock=unset sbx creds sh -c 'echo "sock=${SSH_AUTH_SOCK-unset}"'
