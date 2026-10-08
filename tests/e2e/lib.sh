# Helpers for the e2e checks, sourced by run.sh. $T is the scratch directory.

# project <name>: creates $T/<name> and writes the project's config from stdin.
project() {
  mkdir -p "$T/$1"
  cat > "$T/config/$1.json"
}

# sbx <project> <command...>: runs command in the project's sandbox, started
# from the project's root.
sbx() {
  local p=$1
  shift
  (cd "$T/$p" && srtbox run -p "$p" -- "$@")
}

# check <name> <want> <command...>: passes when the command's output, stdout
# and stderr together, contains want.
check() {
  local name=$1 want=$2 out
  shift 2
  out=$("$@" 2>&1)
  if [[ $out == *"$want"* ]]; then
    echo "ok    $name"
    echo ok >> "$T/results"
  else
    echo "FAIL  $name"
    echo "      want: $want"
    printf '%s\n' "$out" | sed 's/^/      /'
    echo fail >> "$T/results"
  fi
}

skip() {
  echo "skip  $1: $2"
}

# rc <command...>: runs command and prints its exit status as rc=N.
rc() {
  "$@"
  echo "rc=$?"
}
