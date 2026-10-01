#!/usr/bin/env bash
# The ops lock of the test server: one deploy, full end-to-end run or fault
# drill at a time, whoever starts it (two coding sessions share the server).
#
#   scripts/ops/lock.sh run [--owner TEXT] -- COMMAND [ARGS...]
#       waits up to 60 minutes for the lock (flock on the server's
#       /opt/exchange/infra/ops.lock), writes who holds it and since when to
#       ops.lock.owner, runs COMMAND here and releases the lock when it ends.
#       The lock lapses after 2 hours, or as soon as this side goes away
#       (the server holds it for as long as the ssh connection lives).
#       COMMAND sees OPS_LOCK_HELD=1, so the steps it runs that lock on
#       their own (a fault script, server-update.sh) do not wait for it.
#   scripts/ops/lock.sh status
#       who holds the lock, or that it is free.
#
# task deploy, task e2e and task fault run through it, and so does a fault
# script started on its own; server-update.sh takes the same lock when it
# runs without it (OPS_LOCK_HELD unset).
set -euo pipefail

LOCK=/opt/exchange/infra/ops.lock
WAIT=3600 # seconds
HOLD=7200 # seconds

status() {
  # shellcheck disable=SC2016 # expanded on the server
  ssh exchange 'if flock -n '"$LOCK"' true; then echo "free"; else echo "held by $(cat '"$LOCK"'.owner 2>/dev/null || echo "?")"; fi'
}

run() {
  local owner=""
  while [[ $# -gt 0 && $1 != -- ]]; do
    case $1 in
    --owner)
      owner=$2
      shift 2
      ;;
    *)
      echo "lock.sh run: unknown option $1" >&2
      exit 2
      ;;
    esac
  done
  [[ ${1:-} == -- ]] && shift
  [[ $# -gt 0 ]] || {
    echo "lock.sh run: no command" >&2
    exit 2
  }
  if [[ -n ${OPS_LOCK_HELD:-} ]]; then
    exec "$@" # an outer run holds it
  fi
  local where
  where="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "?")@$(basename "$PWD")"
  owner="${owner:-$*} ($(whoami)@$(hostname -s) $where)"
  owner=${owner//[^[:print:]]/}

  dir=$(mktemp -d)
  trap 'rm -rf "$dir"' EXIT
  mkfifo "$dir/in" "$dir/out"
  # The server keeps the lock while it reads this connection's stdin: when
  # this side closes it (or dies) the shell ends and the lock goes with it.
  # shellcheck disable=SC2016 # expanded on the server
  ssh -o ConnectTimeout=20 -o ServerAliveInterval=30 exchange "exec 9>$LOCK
    if ! flock -n 9; then
      echo \"WAITING for \$(cat $LOCK.owner 2>/dev/null || echo ?)\"
      flock -w $WAIT 9 || { echo TIMEOUT; exit 1; }
    fi
    printf '%s since %s\n' $(printf %q "$owner") \"\$(date -u +%Y-%m-%dT%H:%M:%SZ)\" >$LOCK.owner
    echo LOCKED
    timeout $HOLD cat >/dev/null
    : >$LOCK.owner" <"$dir/in" >"$dir/out" 2>&1 &
  local pid=$!
  exec 7>"$dir/in" 8<"$dir/out"
  local line locked=""
  while read -r line <&8; do
    case $line in
    LOCKED)
      locked=1
      break
      ;;
    TIMEOUT)
      echo "lock: not free after $((WAIT / 60)) minutes" >&2
      break
      ;;
    *) echo "lock: $line" >&2 ;;
    esac
  done
  if [[ -z $locked ]]; then
    exec 7>&- 8<&-
    wait "$pid" 2>/dev/null || true
    echo "lock: not taken" >&2
    exit 1
  fi
  echo "lock: held by $owner" >&2
  local status=0
  OPS_LOCK_HELD=1 "$@" || status=$?
  exec 7>&- 8<&-
  wait "$pid" 2>/dev/null || true
  echo "lock: released" >&2
  return "$status"
}

case "${1:-}" in
run)
  shift
  run "$@"
  ;;
status) status ;;
*)
  sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
