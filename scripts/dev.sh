#!/usr/bin/env bash
# Local stack: builds every service and runs it on this machine against the
# test server's infrastructure (there is no local Docker), in a namespace
# of its own so nothing mixes with the deployed environment: PostgreSQL
# database exchange_dev, Redis DB 1, ClickHouse database exchange_dev, and
# Kafka topics and consumer groups prefixed "dev.". Addresses and
# credentials come from .env; the namespace is created on the server over
# ssh (idempotent, so every start checks it). Ports are the services'
# defaults (docs/runbook/server-deploy.md), the gateway on
# http://localhost:8080; logs go to .dev/logs/. See docs/runbook/local-dev.md.
#
#   scripts/dev.sh                   start everything; Ctrl-C stops it
#   DEV_SKIP="auth-service" scripts/dev.sh   everything but the services named
#   scripts/dev.sh run SERVICE       one service in the foreground (go run)
#   scripts/dev.sh ctl ARGS...       exchangectl against the local namespace
#   API_ORIGIN=http://localhost:8080 task web:dev   the H5 against it
set -euo pipefail
cd "$(dirname "$0")/.."

DEV_DB=exchange_dev
NS=dev.
DEV_DIR=.dev
# Readiness is reported in this order.
SERVICES=(notification-service user-service auth-service instrument-service ledger-service spot-trading-service matching-engine market-data-service market-maker risk-service analytics-consumer api-gateway)
# Flags that are on in the test environment (docs/runbook/ledger.md,
# otp.md), turned on when the namespace is created.
DEV_FLAGS=(ledger.welcome_credit account.transfer ledger.manual_adjustment auth.sms)

[[ -f .env ]] || { echo "dev: .env is missing (copy .env.example and fill it in)" >&2; exit 1; }
set -a
# shellcheck disable=SC1091
. ./.env
set +a
: "${POSTGRES_DSN:?dev: POSTGRES_DSN is not set in .env}" "${REDIS_URL:?dev: REDIS_URL is not set in .env}"

# Point every service at the namespace; process variables win over .env.
base=${POSTGRES_DSN%%\?*}
export POSTGRES_DSN="${base%/*}/$DEV_DB${POSTGRES_DSN#"$base"}"
if [[ $REDIS_URL =~ /[0-9]+$ ]]; then
  export REDIS_URL="${REDIS_URL%/*}/1"
else
  export REDIS_URL="$REDIS_URL/1"
fi
export CLICKHOUSE_DB=$DEV_DB KAFKA_NAMESPACE=$NS APP_ENV=local

if [[ ${1:-} == ctl ]]; then
  shift
  exec go run ./cmd/exchangectl "$@"
fi

# prepare creates the namespace on the test server if it is missing and
# sets fresh=1 when it did.
fresh=0
prepare() {
  echo "== namespace on the test server: database $DEV_DB, Redis DB 1, Kafka prefix $NS"
  local out
  # The script arrives on stdin, so every command in it reads /dev/null:
  # docker compose exec would otherwise swallow the rest of the script.
  # shellcheck disable=SC2087 # the variables are expanded here on purpose
  out=$(ssh -o ConnectTimeout=20 exchange "DEV_DB=$DEV_DB KAFKA_NAMESPACE=$NS bash -s" <<'EOF'
set -euo pipefail
cd /opt/exchange/infra
set -a && . ./.env && set +a
pg() { sudo docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -tAc "$1" </dev/null; }
if [ "$(pg "SELECT 1 FROM pg_database WHERE datname = '$DEV_DB'")" != 1 ]; then
  pg "CREATE DATABASE $DEV_DB OWNER $POSTGRES_USER" >/dev/null && echo "created database $DEV_DB"
fi
sudo docker compose exec -T clickhouse clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" \
  --query "CREATE DATABASE IF NOT EXISTS $DEV_DB" </dev/null
created=$(bash redpanda/topics.sh </dev/null | grep -c '^created' || true)
echo "topics ready ($created created now)"
EOF
  )
  echo "$out"
  if [[ $out == *"created database"* ]]; then fresh=1; fi
}

# default_flags CTL... turns on DEV_FLAGS with the exchangectl command given.
default_flags() {
  local flag
  for flag in "${DEV_FLAGS[@]}"; do
    "$@" flags set "$flag" --on --reason "local stack defaults (scripts/dev.sh)" >/dev/null
    echo "flag $flag on"
  done
}

if [[ ${1:-} == run ]]; then
  svc=${2:-}
  [[ -n "$svc" && -d "cmd/$svc" ]] || { echo "usage: scripts/dev.sh run SERVICE (one of: ${SERVICES[*]})" >&2; exit 2; }
  prepare
  if ((fresh)); then default_flags go run ./cmd/exchangectl; fi
  exec go run "./cmd/$svc"
fi

ops_port() {
  case $1 in
    api-gateway) echo 9080 ;;
    auth-service) echo 9081 ;;
    user-service) echo 9082 ;;
    notification-service) echo 9083 ;;
    instrument-service) echo 9084 ;;
    ledger-service) echo 9085 ;;
    spot-trading-service) echo 9088 ;;
    matching-engine) echo 9089 ;;
    market-data-service) echo 9090 ;;
    market-maker) echo 9091 ;;
    risk-service) echo 9086 ;;
    analytics-consumer) echo 9087 ;;
  esac
}

DEV_SKIP=${DEV_SKIP:-}
skip=" ${DEV_SKIP//,/ } "
started=()
for svc in "${SERVICES[@]}"; do
  [[ $skip == *" $svc "* ]] || started+=("$svc")
done

prepare

echo "== build"
mkdir -p "$DEV_DIR/bin" "$DEV_DIR/logs"
pkgs=(./cmd/exchangectl)
for svc in "${started[@]}"; do pkgs+=("./cmd/$svc"); done
go build -o "$DEV_DIR/bin/" "${pkgs[@]}"

if ((fresh)); then default_flags "$DEV_DIR/bin/exchangectl"; fi

pids=()
stop() {
  trap - EXIT
  if [[ ${#pids[@]} -gt 0 ]]; then
    echo
    echo "== stopping"
    kill "${pids[@]}" 2>/dev/null || true
    wait 2>/dev/null || true
  fi
}
trap stop EXIT
trap 'exit 130' INT TERM

# died I reports that the I-th started service exited, with the end of its
# log, and fails.
died() {
  local svc=${started[$1]}
  echo "dev: $svc exited; the end of $DEV_DIR/logs/$svc.log:" >&2
  tail -20 "$DEV_DIR/logs/$svc.log" >&2
  exit 1
}

echo "== start"
# All at once: gRPC clients connect lazily and the gateway retries the
# signing keys, so no service needs another to be up first. Every round
# trip to the test server is slow from here, which makes starting in
# sequence take minutes.
for svc in "${started[@]}"; do
  "$DEV_DIR/bin/$svc" >"$DEV_DIR/logs/$svc.log" 2>&1 &
  pids+=($!)
done
for i in "${!started[@]}"; do
  svc=${started[$i]}
  port=$(ops_port "$svc")
  for _ in $(seq 180); do
    curl -sf "http://127.0.0.1:$port/readyz" >/dev/null && break
    kill -0 "${pids[$i]}" 2>/dev/null || died "$i"
    sleep 1
  done
  curl -sf "http://127.0.0.1:$port/readyz" >/dev/null || { echo "dev: $svc is not ready after 180s" >&2; died "$i"; }
  printf 'ok   %-21s ready (ops :%s)\n' "$svc" "$port"
  if [[ $svc == instrument-service ]]; then
    "$DEV_DIR/bin/exchangectl" instruments apply --file deploy/instruments/test.json --reason "local stack" | tail -1 | sed 's/^/     reference data: /'
  fi
done

cat <<EOF

The local stack is up in namespace "$NS"${DEV_SKIP:+ (not started: $DEV_SKIP)}:
  API          http://localhost:8080/v1/time
  H5           API_ORIGIN=http://localhost:8080 task web:dev
  dev inbox    curl 'http://localhost:8080/v1/dev/messages?target=someone%40example.com'
  logs         $DEV_DIR/logs/<service>.log
  operator CLI scripts/dev.sh ctl flags list
Ctrl-C stops everything.
EOF

while :; do
  for i in "${!pids[@]}"; do
    kill -0 "${pids[$i]}" 2>/dev/null || died "$i"
  done
  sleep 2
done
