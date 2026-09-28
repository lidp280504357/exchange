#!/usr/bin/env bash
# Observability checks (acceptance criterion 10 of phase 1): every
# response carries a trace ID (the X-Trace-Id header, repeated as trace_id
# in error bodies), and every service answers liveness and readiness and
# exports the metrics operators alert on: request rate, errors and
# latency, OTP volume, event backlog, outbox, dead letters and the
# reconciliations. Reaches the server with REMOTE (default: ssh exchange).
#
#   scripts/e2e/ops.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

echo "== trace IDs"
trace_check() { # trace_check METHOD PATH STATUS [BODY]
  local method=$1 path=$2 want=$3 data=${4:-} headers status trace body_trace
  local args=(-s -D - -o "$WORK/body" -X "$method" "$BASE$path")
  [[ -n "$data" ]] && args+=(-H 'Content-Type: application/json' -d "$data")
  headers=$(curl "${args[@]}" | tr -d '\r')
  status=$(head -1 <<<"$headers" | awk '{print $2}')
  trace=$(awk -F': ' 'tolower($1) == "x-trace-id" {print $2}' <<<"$headers")
  [[ "$status" == "$want" && "$trace" =~ ^[0-9a-f]{32}$ ]] || { echo "FAIL $method $path: status $status, trace '$trace'" >&2; exit 1; }
  if (( status >= 400 )); then
    body_trace=$(jq -r .trace_id "$WORK/body")
    [[ "$body_trace" == "$trace" ]] || { echo "FAIL $method $path: body trace_id $body_trace != header $trace" >&2; exit 1; }
  fi
  printf 'ok   %s %s → %s with trace %s\n' "$method" "$path" "$status" "$trace"
}
trace_check GET /v1/time 200
trace_check GET /v1/market/pairs 200
trace_check GET /v1/auth/sessions 401
trace_check GET /v1/no-such-thing 404           # the gateway's own error
trace_check GET /v1/market/pairs/NOPE_NOPE 404  # an upstream's error, proxied
trace_check DELETE /v1/time 405
trace_check POST /v1/auth/otp/verify 400 '{"challenge_id":"x"'
# One OTP request, so auth-service has counted at least one.
call POST /v1/auth/otp/request "{\"scene\":\"LOGIN\",\"channel\":\"EMAIL\",\"identifier\":\"nobody-$RUN@example.com\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"e2e-ops-$RUN\"}"
expect 200 - "an OTP request (a decoy: the account does not exist)"

echo "== health and metrics of every service"
# service ops-port metric families that must be exported
services=(
  "api-gateway 9080 http_server_requests_total http_server_request_duration_seconds ws_connections"
  "auth-service 9081 http_server_requests_total auth_otp_requests_total auth_otp_verifications_total kafka_consumer_lag outbox_pending grpc_server_handled_total"
  "user-service 9082 grpc_server_handled_total outbox_pending kafka_consumer_lag"
  "notification-service 9083 notify_sends_total notify_provider_circuit_open kafka_consumer_records_total kafka_consumer_lag outbox_pending"
  "instrument-service 9084 http_server_requests_total grpc_server_handled_total outbox_pending"
  "ledger-service 9085 ledger_reconcile_mismatches ledger_reconcile_runs_total kafka_consumer_lag outbox_pending"
  "spot-trading-service 9088 outbox_pending kafka_consumer_lag"
  "matching-engine 9089 outbox_pending kafka_consumer_lag matching_depth_published_total"
  "market-data-service 9090 kafka_consumer_lag market_updates_published_total market_reference_age_seconds"
  "market-maker 9091 mm_quoting flags_last_refresh_timestamp_seconds"
  "wallet-service 9092 outbox_pending kafka_consumer_lag wallet_scan_lag_blocks wallet_scan_block"
  "risk-service 9086 outbox_pending kafka_consumer_lag flags_last_refresh_timestamp_seconds"
  "analytics-consumer 9087 analytics_ingested_rows_total analytics_reconcile_missing kafka_consumer_lag"
)
for entry in "${services[@]}"; do
  read -r svc port families <<<"$entry"
  out=$(compose "exec -T $svc sh -c 'wget -qO- http://127.0.0.1:$port/healthz >/dev/null && wget -qO- http://127.0.0.1:$port/readyz >/dev/null && wget -qO- http://127.0.0.1:$port/metrics'") ||
    { echo "FAIL $svc: liveness, readiness or metrics endpoint failed" >&2; exit 1; }
  for f in exchange_build_info go_goroutines $families; do
    grep -q "^$f" <<<"$out" || { echo "FAIL $svc does not export $f" >&2; exit 1; }
  done
  printf 'ok   %-21s healthy, ready, exports %s\n' "$svc" "$(wc -w <<<"$families" | tr -d ' ') key metric families"
done
dlq=$(compose "exec -T notification-service sh -c 'wget -qO- http://127.0.0.1:9083/metrics'" | grep -c 'kafka_consumer_records_total{.*result=' || true)
(( dlq > 0 )) && echo "ok   consumer outcomes (ok/retry/dlq) are counted per group and topic"
mismatches=$(compose "exec -T ledger-service sh -c 'wget -qO- http://127.0.0.1:9085/metrics'" | awk '/^ledger_reconcile_mismatches/ {s += $2} END {print s + 0}')
[[ "$mismatches" == 0 ]] || { echo "FAIL the last ledger reconciliation found $mismatches mismatches" >&2; exit 1; }
echo "ok   the last ledger reconciliation found no mismatch"
# Acceptance criterion 8: ClickHouse holds exactly what the outboxes published.
metrics=$(compose "exec -T analytics-consumer sh -c 'wget -qO- http://127.0.0.1:9087/metrics'")
grep -q '^analytics_reconcile_missing{topic="auth.events"}' <<<"$metrics" || { echo "FAIL no ClickHouse reconciliation has run" >&2; exit 1; }
off=$(awk '/^analytics_reconcile_missing/ && $2 != 0' <<<"$metrics")
[[ -z "$off" ]] || { echo "FAIL ClickHouse and the outboxes disagree (published minus ingested): $off" >&2; exit 1; }
echo "ok   ClickHouse holds exactly the events the outboxes published in the last day"

echo "all observability checks passed"
