#!/usr/bin/env bash
# Runs srtbox under the real sandbox with throwaway configs and checks what
# the sandboxed commands see.
#
#   tests/e2e/run.sh [name...]
#
# Each name is a file in tests/e2e without .sh; with none, all of them run.
# SRTBOX_E2E_NETWORK=1 adds the checks that reach hosts on the internet.
set -u
here=$(cd "$(dirname "$0")" && pwd -P)

if [ -n "${SRTBOX_PROJECT:-}" ]; then
  echo "e2e: run this outside a srtbox session; srt cannot start inside one" >&2
  exit 2
fi
if [ -z "${SRTBOX_SRT:-}" ] && ! command -v srt >/dev/null; then
  echo "e2e: srt is not on PATH" >&2
  exit 2
fi

# /tmp rather than $TMPDIR, which on macOS sits under /var/folders, where the
# sandbox cannot write.
T=$(mktemp -d /tmp/srtbox-e2e.XXXXXX) && T=$(cd "$T" && pwd -P) || exit 2
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/config"
echo '{}' > "$T/config/base.json"
(cd "$here/../.." && go build -o "$T/bin/srtbox" .) || exit 2

# Approval requests are answered only through `srtbox approve`: these stand in
# for the desktop notifiers and answer nothing.
for n in notify-send osascript; do
  printf '#!/bin/sh\nexit 0\n' > "$T/bin/$n"
  chmod +x "$T/bin/$n"
done
export PATH="$T/bin:$PATH" SRTBOX_CONFIG_DIR="$T/config" T
os=$(uname -s)
export os

. "$here/lib.sh"

if [ $# -eq 0 ]; then
  set -- launch filesystem credentials broker forward network
fi
for name in "$@"; do
  echo "== $name"
  # A subshell, so one file's environment and projects don't reach the next.
  (. "$here/$name.sh")
done

fails=$(grep -c fail "$T/results" 2>/dev/null)
passes=$(grep -c ok "$T/results" 2>/dev/null)
echo "== ${passes:-0} passed, ${fails:-0} failed"
[ "${fails:-0}" -eq 0 ]
