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
  "derivatives-engine 9096 outbox_pending matching_depth_published_total"
  "derivatives-service 9095 outbox_pending kafka_consumer_lag derivatives_fills_total derivatives_settlements_parked_total grpc_server_handled_total"
  "market-data-service 9090 kafka_consumer_lag market_updates_published_total market_reference_age_seconds market_reference_book_resyncs_total"
  "market-maker 9091 market_house_active market_house_publish_failures_total flags_last_refresh_timestamp_seconds"
  "wallet-service 9092 outbox_pending kafka_consumer_lag wallet_scan_lag_blocks wallet_scan_block wallet_sweeps_open wallet_chain_fees_unbooked"
  "signer 9093 grpc_server_handled_total"
  "admin-service 9094 outbox_pending"
  "risk-service 9086 outbox_pending kafka_consumer_lag flags_last_refresh_timestamp_seconds"
  "analytics-consumer 9087 analytics_ingested_rows_total analytics_reconcile_missing kafka_consumer_lag"
)
for entry in "${services[@]}"; do
  read -r svc port families <<<"$entry"
  out=$(compose "exec -T $svc sh -c 'wget -qO- http://127.0.0.1:$port/healthz >/dev/null && wget -qO- http://127.0.0.1:$port/readyz >/dev/null && wget -qO- http://127.0.0.1:$port/metrics'") ||
    { echo "FAIL $svc: liveness, readiness or metrics endpoint failed" >&2; exit 1; }
  # The ledger's reconciliation gauges appear once its first run is done:
  # a minute after the start, then about two over the whole ledger. Right
  # after a deploy, wait for it (3 minutes at most).
  if [[ $svc == ledger-service ]]; then
    for _ in $(seq 1 36); do
      grep -q '^ledger_reconcile_mismatches' <<<"$out" && break
      sleep 5
      out=$(compose "exec -T $svc sh -c 'wget -qO- http://127.0.0.1:$port/metrics'") || true
    done
  fi
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
echo "ok   ClickHouse holds exactly the events the outboxes published in the reconciliation window"

echo "== read models"
# Trades and order changes are one row per event (a minute old, so
# ingested); contract trades and orders share the tables with the spot ones.
# analytics-consumer stores a batch's events before its read models, so a
# read between the two inserts, or behind a busy batch, can count a few
# events without their rows yet: asked again for up to half a minute
# (B63's regression saw 3 of 1.58 million once, equal a minute later).
read_models() {
  local counts
  counts=$(ch "SELECT
    (SELECT count() FROM trades FINAL WHERE executed_at < now() - INTERVAL 1 MINUTE),
    (SELECT uniqExact(event_id) FROM events WHERE topic IN ('trade.events', 'derivatives.trade.events') AND occurred_at < now() - INTERVAL 1 MINUTE),
    (SELECT count() FROM order_updates FINAL WHERE occurred_at < now() - INTERVAL 1 MINUTE),
    (SELECT uniqExact(event_id) FROM events WHERE topic IN ('order.events', 'derivatives.order.events') AND occurred_at < now() - INTERVAL 1 MINUTE)")
  read -r trades trade_events updates order_events <<<"$counts"
  [[ $trades == "$trade_events" && $updates == "$order_events" ]]
}
matched=""
for _ in $(seq 10); do
  read_models && { matched=1; break; }
  sleep 3
done
[[ -n $matched ]] ||
  { echo "FAIL read models: $trades trades for $trade_events trade events, $updates order changes for $order_events order events" >&2; exit 1; }
echo "ok   $trades trades and $updates order changes, one per event"
# The wallet read models agree with wallet-service's tables on every
# status the events report (confirmation progress is not an event). A
# deposit of nobody (to an address no user has, booked unclaimed) has no
# events, there being nobody to tell, until a person assigns it to a user.
wallet_statuses() {
  local pg_deposits pg_withdrawals ch_deposits ch_withdrawals
  pg_deposits=$(pg "SELECT coalesce(string_agg(s || '=' || n, ',' ORDER BY s), '') FROM (SELECT CASE status WHEN 'CONFIRMING' THEN 'DETECTED' ELSE status END AS s, count(*) AS n FROM wallet.deposits WHERE user_id <> '00000000-0000-0000-0000-000000000000' GROUP BY 1) x")
  pg_withdrawals=$(pg "SELECT coalesce(string_agg(s || '=' || n, ',' ORDER BY s), '') FROM (SELECT CASE status WHEN 'SIGNING' THEN 'APPROVED' WHEN 'CONFIRMING' THEN 'BROADCAST' ELSE status END AS s, count(*) AS n FROM wallet.withdrawals GROUP BY 1) x")
  ch_deposits=$(ch "SELECT arrayStringConcat(arraySort(groupArray(concat(status, '=', toString(n)))), ',') FROM (SELECT status, count() AS n FROM wallet_deposits FINAL GROUP BY status)")
  ch_withdrawals=$(ch "SELECT arrayStringConcat(arraySort(groupArray(concat(status, '=', toString(n)))), ',') FROM (SELECT status, count() AS n FROM wallet_withdrawals FINAL GROUP BY status)")
  WALLET_STATUSES="deposits $pg_deposits / $ch_deposits, withdrawals $pg_withdrawals / $ch_withdrawals"
  [[ $pg_deposits == "$ch_deposits" && $pg_withdrawals == "$ch_withdrawals" ]]
}
matched=""
for _ in $(seq 10); do
  wallet_statuses && { matched=1; break; }
  sleep 3
done
[[ -n $matched ]] || { echo "FAIL wallet read models (PostgreSQL / ClickHouse): $WALLET_STATUSES" >&2; exit 1; }
echo "ok   wallet read models match wallet-service: $WALLET_STATUSES"
candles=$(ch "SELECT (SELECT sum(trades) FROM candles_1m FINAL WHERE open_time < toStartOfMinute(now()) - INTERVAL 1 MINUTE),
  (SELECT count() FROM trades FINAL WHERE executed_at < toStartOfMinute(now()) - INTERVAL 1 MINUTE)")
read -r in_candles in_trades <<<"$candles"
[[ $in_candles == "$in_trades" ]] || { echo "FAIL one-minute candles count $in_candles trades, the trades table $in_trades" >&2; exit 1; }
echo "ok   one-minute candles cover all $in_trades trades"

echo "all observability checks passed"
