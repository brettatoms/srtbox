# Network: the domain allowlist, TLS termination only for injection hosts, and
# injected values reaching only their hosts. These reach httpbin.org,
# postman-echo.com and example.com, so they run only with
# SRTBOX_E2E_NETWORK=1.
if [ "${SRTBOX_E2E_NETWORK:-}" != 1 ]; then
  skip "network" "set SRTBOX_E2E_NETWORK=1 to run"
  return
fi

project net <<EOF
{"_root": "$T/net",
 "network": {"allowedDomains": ["httpbin.org", "postman-echo.com", "example.com"]},
 "credentials": {"files": [{"path": "$T/net/token", "mode": "mask", "injectHosts": ["httpbin.org"]}]},
 "_inject": {"E2E_INJECTED": {"from": "printf e2e-injected-value", "hosts": ["httpbin.org"]}}}
EOF
printf e2e-file-value > "$T/net/token"

check "an allowed host is reachable" code=200 \
  sbx net curl -s -o /dev/null -w 'code=%{http_code}' https://example.com
check "an unlisted host is blocked" blocked \
  sbx net sh -c 'curl -sf -o /dev/null https://example.org && echo reached || echo blocked'
check "why says an unlisted host is blocked" "no allowedDomains entry matches it" \
  sbx net srtbox why example.org

tls='curl -sv -o /dev/null "https://$1" 2>&1 | grep -q "issuer:.*sandbox-runtime" && echo terminated || echo end-to-end'
check "an injection host is terminated" terminated sbx net sh -c "$tls" _ httpbin.org
check "other hosts keep end-to-end TLS" end-to-end sbx net sh -c "$tls" _ example.com

check "an injected value reaches its host" e2e-injected-value \
  sbx net sh -c 'curl -s -H "X-E2e: $E2E_INJECTED" https://httpbin.org/headers'
check "an injected value reaches no other host" placeholder-only \
  sbx net sh -c 'r=$(curl -s -H "X-E2e: $E2E_INJECTED" https://postman-echo.com/headers)
    case $r in *e2e-injected-value*) echo leaked ;; *x-e2e*) echo placeholder-only ;; *) echo "no echo: $r" ;; esac'
if [ "$os" = Linux ]; then
  check "a masked file's value reaches its host" e2e-file-value \
    sbx net sh -c 'curl -s -H "X-E2e: $(cat token)" https://httpbin.org/headers'
fi
