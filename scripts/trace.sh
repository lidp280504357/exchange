#!/usr/bin/env bash
# Everything the test environment recorded about one request, by its trace
# ID (the X-Trace-Id response header, or trace_id in an error body): the log
# lines of every service that carry it, in time order, then the events it
# caused, which ClickHouse keeps with the trace ID as correlation_id (events
# published while handling an event continue its trace). Spans are not
# exported yet (requirements §12.2), so logs and events are the trace. Needs
# ssh to the test server; see docs/runbook/observability.md.
#
#   scripts/trace.sh TRACE_ID [SINCE]   SINCE bounds the logs searched (default 24h)
set -euo pipefail

id=${1:-}
since=${2:-24h}
if [[ ! $id =~ ^[0-9a-f]{32}$ || ! $since =~ ^[0-9]+[smh]$ ]]; then
  echo "usage: scripts/trace.sh TRACE_ID [SINCE]   TRACE_ID is 32 hex digits, SINCE like 30m or 24h" >&2
  exit 2
fi
REMOTE="ssh -o ConnectTimeout=20 exchange"

echo "== logs (last $since)"
logs=$($REMOTE "cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml logs --no-color --no-log-prefix --since $since 2>&1 | grep -F $id" || true)
if [[ -z "$logs" ]]; then
  echo "(no log line carries this trace ID)"
else
  # time service level message, then the other attributes as key=value.
  jq -Rr 'fromjson? | [.time, .service, .level, .msg,
      (del(.time, .service, .level, .msg, .trace_id) | to_entries | map("\(.key)=\(.value | tostring)") | join(" "))]
    | join("  ")' <<<"$logs" | sort
fi

echo "== events (ClickHouse events with correlation_id = the trace ID)"
# shellcheck disable=SC2016 # the credentials expand on the server
events=$($REMOTE 'cd /opt/exchange/infra && set -a && . ./.env && set +a && sudo docker compose exec -T clickhouse clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --database "$CLICKHOUSE_DB" --format PrettyCompactMonoBlock' <<SQL
SELECT occurred_at, producer, topic, event_type, aggregate_id, event_id
FROM events FINAL
WHERE correlation_id = '$id'
ORDER BY occurred_at, sequence
SQL
)
echo "${events:-(no event carries this trace ID)}"
