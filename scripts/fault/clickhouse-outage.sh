#!/usr/bin/env bash
# Fault injection: the analytics store goes away (acceptance criterion 8
# of phase 1). Core state in PostgreSQL does not depend on ClickHouse:
# registrations, transfers and balances keep working while it is down;
# once it is back the analytics consumer catches up, so the user's events
# and ledger lines become queryable. Disrupts reporting for about half a
# minute; always restarts ClickHouse.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'compose "start clickhouse" >/dev/null 2>&1 || true; cleanup_remote' EXIT
EMAIL="e2e-fault-ch-$RUN@example.com"
DEVICE="e2e-fault-ch-$RUN"

echo "== clickhouse down"
compose "stop clickhouse" >/dev/null
register "$EMAIL" "$DEVICE" "e2e fault $RUN"
USER_ID=$(jq -r .user_id <<<"$BODY")
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
funded() {
  call GET "/v1/account/balances?account_type=SPOT" "" "${AUTH[@]}"
  [[ $(jq -r '[.balances[] | select(.asset == "USDT")][0].available // "0"' <<<"$BODY") == "10000" ]]
}
eventually 60 "welcome funds credited without the analytics store" funded
call POST /v1/account/transfers '{"asset":"USDT","amount":"2.5","from_account_type":"SPOT","to_account_type":"FUTURES"}' "${AUTH[@]}" -H "Idempotency-Key: fault-ch-$RUN"
expect 201 - "a transfer settles without the analytics store"
TRANSFER_ID=$(jq -r .transfer_id <<<"$BODY")

echo "== clickhouse back"
compose "start clickhouse" >/dev/null
wait_healthy clickhouse
ingested() { (( $(ch "SELECT count() FROM events WHERE aggregate_id = '$USER_ID'") >= 3 )); }
eventually 180 "the user's events reached ClickHouse after the outage" ingested
transfer_rows() { (( $(ch "SELECT count() FROM events WHERE event_type = 'account.AccountTransferCompleted' AND position(payload, '$TRANSFER_ID') > 0") == 1 )); }
eventually 60 "the transfer event was ingested once" transfer_rows
wait_healthy analytics-consumer
echo "clickhouse outage survived"
