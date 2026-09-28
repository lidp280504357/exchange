# Helpers for checks that look inside the test server; source it after
# common.sh. REMOTE is how to reach the server. By default every command
# shares one ssh connection through the jump host: a master started here,
# detached from our stdio (a master born inside $(...) would keep the
# substitution open until it exits), and closed when the script ends.

# cleanup_remote closes the shared connection and removes the work
# directory; scripts that set their own EXIT trap must call it.
cleanup_remote() {
  if [[ -n "${SSH_CTL:-}" ]]; then
    ssh -o ControlPath="$SSH_CTL" -O exit exchange >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}

if [[ -z "${REMOTE:-}" ]]; then
  SSH_CTL="$WORK/ssh-ctl"
  ssh -o ControlMaster=yes -o ControlPath="$SSH_CTL" -o ControlPersist=yes -o ConnectTimeout=20 -fN exchange </dev/null >/dev/null 2>&1 ||
    echo "note: no shared ssh connection; each command connects on its own" >&2
  # ControlMaster=no: use the master when it is up, else connect directly.
  REMOTE="ssh -o ControlPath=$SSH_CTL -o ControlMaster=no -o ConnectTimeout=20 exchange"
fi
trap cleanup_remote EXIT
REMOTE_INFRA="${REMOTE_INFRA:-/opt/exchange/infra}"
COMPOSE_FILES="-f docker-compose.yml -f docker-compose.apps.yml"

# remote CMD [INPUT] runs a shell command in the infrastructure directory of
# the server with its .env loaded, INPUT on stdin. Connection failures
# (ssh exit status 255) are retried twice.
remote() {
  local cmd="cd $REMOTE_INFRA && set -a && . ./.env && set +a && $1" input=${2-} status=255 attempt
  for attempt in 1 2 3; do
    if printf '%s' "$input" | $REMOTE "$cmd"; then
      return 0
    else
      status=$?
    fi
    [[ $status -ne 255 ]] && return "$status"
    echo "ssh failed (attempt $attempt); retrying" >&2
    sleep 3
  done
  return "$status"
}

# ch SQL prints the result of a ClickHouse query (TSV); the query travels
# on stdin, so it needs no quoting.
ch() {
  # shellcheck disable=SC2016 # expanded on the server
  remote 'sudo docker compose exec -T clickhouse clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --database "$CLICKHOUSE_DB"' "$1"
}

# logs SINCE prints the logs of every container since SINCE (e.g. 10m).
logs() {
  remote "sudo docker compose $COMPOSE_FILES logs --no-color --since $1 2>&1"
}

# compose ARGS... runs docker compose on the server with both files.
compose() {
  remote "sudo docker compose $COMPOSE_FILES $*"
}

# wait_healthy SERVICE [SECONDS] waits until the container reports healthy.
wait_healthy() {
  local svc=$1 limit=${2:-120} waited=0 health
  while :; do
    health=$(compose "ps --format '{{.Health}}' $svc" 2>/dev/null | tr -d '\r' || true)
    [[ "$health" == healthy ]] && { printf 'ok   %s is healthy again (%ss)\n' "$svc" "$waited"; return 0; }
    (( waited >= limit )) && { echo "FAIL $svc not healthy after ${limit}s ($health)" >&2; return 1; }
    sleep 3
    waited=$((waited + 3))
  done
}

# dlq_total prints how many records sit in the dead-letter topics of the
# phase 1 business topics.
dlq_total() {
  local total=0 n t
  for t in auth.events user.events instrument.events ledger.events account.events notification.events audit.events; do
    n=$(exchangectl dlq list "$t" | grep -cE '^[0-9]+:[0-9]+' || true)
    total=$((total + n))
  done
  echo "$total"
}

# exchangectl ARGS... runs the operator CLI inside a service container.
exchangectl() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl $args"
}
