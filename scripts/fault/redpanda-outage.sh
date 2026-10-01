#!/usr/bin/env bash
# Fault injection: the message broker goes away (acceptance criterion 6 of
# phase 1). While Redpanda is down the APIs keep working (events wait in
# the outboxes); once it is back the services reconnect on their own, the
# waiting events are published and consumed once (the new user's welcome
# funds and notices arrive), and nothing new lands in a dead-letter topic.
# Disrupts the test environment for about half a minute; always restarts
# Redpanda. Reaches the server with REMOTE (default: ssh exchange).
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'compose "start redpanda" >/dev/null 2>&1 || true; cleanup_remote' EXIT
EMAIL="e2e-fault-rp-$RUN@example.com"
DEVICE="e2e-fault-rp-$RUN"

before=$(dlq_total)
echo "ok   $before dead letters before the outage"

echo "== redpanda down"
compose "stop redpanda" >/dev/null
register "$EMAIL" "$DEVICE" "e2e fault $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
call GET /v1/market/pairs ""
expect 200 - "reads work without the broker"

echo "== redpanda back"
compose "start redpanda" >/dev/null
wait_healthy redpanda
funded() {
  call GET "/v1/account/balances?account_type=SPOT" "" "${AUTH[@]}"
  [[ $(jq -r '[.balances[] | select(.asset == "USDT")][0].available // "0"' <<<"$BODY") == "10000" ]]
}
eventually 180 "the registration event was published and consumed: welcome funds credited once" funded
welcomed() {
  call GET /v1/notifications "" "${AUTH[@]}"
  [[ $(jq '[.items[] | select(.type == "WELCOME")] | length' <<<"$BODY") == 1 ]]
}
eventually 60 "exactly one welcome notice" welcomed
call POST /v1/account/transfers '{"asset":"USDT","amount":"1","from_account_type":"SPOT","to_account_type":"FUTURES"}' "${AUTH[@]}" -H "Idempotency-Key: fault-rp-$RUN"
expect 201 - "new work flows after the outage"

after=$(dlq_total)
[[ "$after" == "$before" ]] || { echo "FAIL $((after - before)) new dead letters after the outage" >&2; exit 1; }
echo "ok   no new dead letters"
echo "redpanda outage survived"
