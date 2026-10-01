#!/usr/bin/env bash
# Starts what the integration tests of CI need (internal/platform/testenv):
# PostgreSQL, Redis, ClickHouse and Redpanda, at the test server's versions,
# on their usual localhost ports.
#
# GitHub's service containers give a pull three tries within seconds, and a
# registry that throttles anonymous pulls fails the job before it starts.
# Here pulls retry with back-off, and every failure lands in the run's
# annotations, which (unlike the logs) anyone can read without signing in.
set -euo pipefail

POSTGRES_IMAGE=public.ecr.aws/docker/library/postgres:16-alpine
REDIS_IMAGE=public.ecr.aws/docker/library/redis:7.4-alpine
# Google's mirror of Docker Hub: Docker Hub rate-limits anonymous pulls.
CLICKHOUSE_IMAGE=mirror.gcr.io/clickhouse/clickhouse-server:25.3
REDPANDA_IMAGE=docker.redpanda.com/redpandadata/redpanda:v24.3.16

# annotate LEVEL TITLE MESSAGE writes a workflow annotation (one line).
annotate() {
  local msg=${3//'%'/'%25'}
  msg=${msg//$'\r'/}
  msg=${msg//$'\n'/ | }
  echo "::$1 title=$2::$msg"
}

pull() {
  local image=$1 delay=5 attempt out
  for attempt in 1 2 3 4 5 6; do
    if out=$(docker pull -q "$image" 2>&1); then
      echo "pulled $image"
      return 0
    fi
    annotate warning "docker pull $image" "attempt $attempt: $out"
    if ((attempt < 6)); then
      sleep "$delay"
      delay=$((delay * 2))
    fi
  done
  annotate error "docker pull $image" "giving up after 6 attempts"
  return 1
}

# ready NAME COMMAND... waits until COMMAND succeeds inside container NAME.
ready() {
  local name=$1 i
  shift
  for i in $(seq 90); do
    if docker exec "$name" "$@" >/dev/null 2>&1; then
      echo "$name is ready"
      return 0
    fi
    sleep 2
  done
  docker logs --tail 80 "$name" || true
  annotate error "$name" "not ready after 180 seconds (its log is above)"
  return 1
}

for image in "$POSTGRES_IMAGE" "$REDIS_IMAGE" "$CLICKHOUSE_IMAGE" "$REDPANDA_IMAGE"; do
  pull "$image"
done

docker run -d --name postgres -p 5432:5432 \
  -e POSTGRES_USER=exchange -e POSTGRES_PASSWORD=exchange -e POSTGRES_DB=exchange_test "$POSTGRES_IMAGE" >/dev/null
docker run -d --name redis -p 6379:6379 "$REDIS_IMAGE" >/dev/null
docker run -d --name clickhouse -p 9000:9000 \
  -e CLICKHOUSE_USER=exchange -e CLICKHOUSE_PASSWORD=exchange -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 "$CLICKHOUSE_IMAGE" >/dev/null
docker run -d --name redpanda -p 9092:9092 -p 8081:8081 "$REDPANDA_IMAGE" redpanda start \
  --mode dev-container --smp 1 --overprovisioned \
  --kafka-addr 0.0.0.0:9092 --advertise-kafka-addr localhost:9092 \
  --schema-registry-addr 0.0.0.0:8081 >/dev/null

# Over TCP: while the image initialises the database, PostgreSQL listens
# on its socket only, and then restarts.
ready postgres pg_isready -h 127.0.0.1 -U exchange -d exchange_test
ready redis redis-cli ping
ready clickhouse wget -q --spider http://127.0.0.1:8123/ping
ready redpanda sh -c "rpk cluster health | grep -q 'Healthy:.*true'"
