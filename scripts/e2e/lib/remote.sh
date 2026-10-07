# Helpers for checks that look inside the test server; source it after
# common.sh. REMOTE is how to reach the server. By default every command
# shares one ssh connection through the jump host: a master started here,
# detached from our stdio (a master born inside $(...) would keep the
# substitution open until it exits), and closed when the script ends.

# close_remote closes the shared connection.
close_remote() {
  if [[ -n "${SSH_CTL:-}" ]]; then
    ssh -o ControlPath="$SSH_CTL" -O exit exchange >/dev/null 2>&1 || true
  fi
}

# cleanup_remote closes the shared connection and removes the work
# directory; scripts that set their own EXIT trap must call it.
cleanup_remote() {
  close_remote
  rm -rf "$WORK"
}

if [[ -z "${REMOTE:-}" ]]; then
  SSH_CTL="$WORK/ssh-ctl"
  ssh -o ControlMaster=yes -o ControlPath="$SSH_CTL" -o ControlPersist=yes -o ConnectTimeout=20 -fN exchange </dev/null >/dev/null 2>&1 ||
    echo "note: no shared ssh connection; each command connects on its own" >&2
  # ControlMaster=no: use the master when it is up, else connect directly.
  REMOTE="ssh -o ControlPath=$SSH_CTL -o ControlMaster=no -o ConnectTimeout=20 exchange"
fi
# After common.sh the connection closes once the at_exit commands ran
# (replacing its EXIT trap would skip them).
if declare -F at_exit >/dev/null; then
  AT_END=close_remote
else
  trap cleanup_remote EXIT
fi
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

# pg SQL prints the result of a PostgreSQL query in the exchange database
# (unaligned, no header); the query travels on stdin.
pg() {
  # shellcheck disable=SC2016 # expanded on the server
  remote 'sudo docker compose exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA' "$1"
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

# metric SERVICE PORT NAME [FILTER] prints the value of the first sample
# of metric NAME on the service's ops port whose labels contain FILTER.
metric() {
  local out
  out=$(compose "exec -T $1 wget -qO- http://127.0.0.1:$2/metrics") || return 1
  awk -v name="$3" -v filter="${4:-}" '($1 == name || index($1, name "{") == 1) && index($1, filter) { print $2; exit }' <<<"$out"
}

# block_egress SERVICE drops the container's traffic to the internet
# (everything outside the compose network) at the host's DOCKER-USER
# chain, as if the outside went silent; unblock_egress SERVICE lifts it.
block_egress() {
  local ip
  ip=$(remote "sudo docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' exchange-infra-$1-1")
  [[ $ip =~ ^[0-9.]+$ ]] || { echo "FAIL no IP address for $1" >&2; return 1; }
  remote "sudo iptables -I DOCKER-USER -s $ip ! -d 172.16.0.0/12 -m comment --comment exchange-fault-$1 -j DROP"
}
unblock_egress() {
  # shellcheck disable=SC2016 # expanded on the server
  remote 'sudo iptables -S DOCKER-USER | grep -- "exchange-fault-'"$1"'" | sed "s/^-A/-D/" | while read -r rule; do eval sudo iptables "$rule"; done'
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

# internal METHOD SERVICE PORT PATH [JSON] calls a service's internal
# endpoint (the gateway does not route /internal) from the test server and
# sets STATUS and BODY.
internal() {
  local method=$1 svc=$2 port=$3 path=$4 body=${5-} data="" out
  [[ -n "$body" ]] && data="--data-binary @-"
  # shellcheck disable=SC2016 # expanded on the server
  out=$(remote "ip=\$(sudo docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' \$(sudo docker compose $COMPOSE_FILES ps -q $svc) | awk '{print \$1}') && curl -s -m 20 -X $method -H 'Content-Type: application/json' $data -w '\n%{http_code}' http://\$ip:$port$path" "$body")
  STATUS=${out##*$'\n'}
  BODY=${out%$'\n'*}
}

# exchangectl ARGS... runs the operator CLI inside a service container.
exchangectl() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl $args"
}
