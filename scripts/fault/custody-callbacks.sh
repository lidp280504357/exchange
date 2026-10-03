#!/usr/bin/env bash
# Fault injection: the custody wallet's callbacks and gateway misbehave
# (phase 4 B7, ADR-0011), on the test server's mock gateway as the
# custodian UDUNMOCK with the hidden test asset TUSD (ADR-0017), so that it
# moves nothing of UDUN, whatever UDUN is:
#   - callbacks held back two minutes: the deposit waits, then is credited
#     once; replays of it change nothing;
#   - wallet-service down while the custodian calls back: the custodian
#     retries until it is back, and the deposit is credited once;
#   - the gateway down: new deposit addresses fail with WALLET_UNAVAILABLE
#     (addresses already given keep working), a reconciliation fails and
#     wallet_custody_up drops to 0; all recovers when it is back, and the
#     custodian and the ledger agree again. (While a callback is held
#     back the custodian holds more than the ledger expects: a surplus,
#     never a shortfall.)
# Needs wallet.test_assets on for region AQ. About six minutes; the delay
# is lifted and both containers are started again whatever happens.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"

TUSD_COIN="195:TQQCuyVcUEknTGyfSRKhcUuLZfEe93qWpy"
mock() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T udun-mock /app/udun-mock $args"
}
restore() {
  compose "start wallet-service udun-mock" >/dev/null 2>&1 || true
  mock delay --seconds 0 >/dev/null 2>&1 || true
}
at_exit restore

# Only on the custodian that is the stand-in (ADR-0017).
if [[ $(remote "sudo docker compose $COMPOSE_FILES exec -T wallet-service printenv UDUNMOCK_GATEWAY_URL </dev/null || true") != http://udun-mock:* ]]; then
  echo "SKIP custody-callbacks: wallet-service's UDUNMOCK custodian is not the stand-in udun-mock"
  exit 0
fi

EMAIL="e2e-fault-custody-$RUN@example.com"
# Region AQ: a test account, eligible for TEST_ASSETS (wallet.test_assets).
register "$EMAIL" "e2e-fault-custody-$RUN" "e2e fault $RUN" AQ
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
call GET "/v1/wallet/deposit-address?asset=TUSD&network=TRON-TEST" "" "${AUTH[@]}"
expect 200 - "a TRON-TEST address"
ADDR=$(jq -r .address <<<"$BODY")

# credited AMOUNT prints how many deposits of AMOUNT are credited.
credited() {
  call GET /v1/wallet/deposits "" "${AUTH[@]}" >/dev/null && jq "[.items[] | select(.status == \"CREDITED\" and .amount == \"$1\")] | length" <<<"$BODY"
}

echo "== callbacks held back two minutes"
mock delay --seconds 120 >/dev/null
TRADE=$(mock deposit --address "$ADDR" --coin "$TUSD_COIN" --amount 11 | jq -r .trade_id)
sleep 60
[[ $(credited 11) == 0 ]] || { echo "FAIL the deposit arrived before its callback" >&2; exit 1; }
echo "ok   a minute later nothing is credited"
mock delay --seconds 0 >/dev/null
once11() { [[ $(credited 11) == 1 ]]; }
eventually 240 "the delayed callback arrives and credits the deposit once" once11
for _ in 1 2 3; do mock replay --trade "$TRADE" >/dev/null; done
[[ $(credited 11) == 1 ]] || { echo "FAIL a replay credited the deposit again" >&2; exit 1; }
attempts=$(pg "SELECT max(attempts) FROM wallet.custody_callbacks WHERE trade_id = '$TRADE'")
((attempts >= 4)) || { echo "FAIL the replays are not counted ($attempts attempts)" >&2; exit 1; }
echo "ok   three replays change nothing ($attempts attempts on one callback)"

echo "== wallet-service down while the custodian calls back"
compose "stop wallet-service" >/dev/null
mock deposit --address "$ADDR" --coin "$TUSD_COIN" --amount 12 >/dev/null
sleep 20
compose "start wallet-service" >/dev/null
wait_healthy wallet-service 180
once12() { [[ $(credited 12) == 1 ]]; }
eventually 400 "the custodian's retries reach it and the deposit is credited once" once12

echo "== the gateway goes down"
compose "stop udun-mock" >/dev/null
register "e2e-fault-custody2-$RUN@example.com" "e2e-fault-custody2-$RUN" "e2e fault $RUN" AQ
OTHER=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
call GET "/v1/wallet/deposit-address?asset=TUSD&network=TRON-TEST" "" "${OTHER[@]}"
expect 503 WALLET_UNAVAILABLE "a new address cannot be created"
call GET "/v1/wallet/deposit-address?asset=TUSD&network=TRON-TEST" "" "${AUTH[@]}"
expect 200 - "an address given before still answers"
exchangectl wallet reconcile --network UDUNMOCK | head -1
down() { [[ $(metric wallet-service 9092 wallet_custody_up 'provider="UDUNMOCK"') == 0 ]]; }
eventually 60 "the reconciliation fails and wallet_custody_up drops to 0" down
compose "start udun-mock" >/dev/null
wait_healthy udun-mock 120
call GET "/v1/wallet/deposit-address?asset=TUSD&network=TRON-TEST" "" "${OTHER[@]}"
expect 200 - "addresses are created again"
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
exchangectl wallet reconcile --network UDUNMOCK | head -1
up() { [[ $(metric wallet-service 9092 wallet_custody_up 'provider="UDUNMOCK"') == 1 ]]; }
eventually 60 "the custodian answers again (wallet_custody_up 1)" up
checked() {
  SHORT=$(pg "SELECT shortfall FROM wallet.chain_checks WHERE network = 'UDUNMOCK' AND asset = 'TUSD' AND checked_at > '$SINCE' ORDER BY checked_at DESC LIMIT 1")
  [[ -n $SHORT ]]
}
eventually 60 "a reconciliation runs after the gateway is back" checked
[[ $(jq -n "$SHORT == 0") == true ]] || { echo "FAIL TUSD shortfall $SHORT after the drill" >&2; exit 1; }
echo "ok   the custodian and the ledger agree after the drill (TUSD shortfall $SHORT)"
echo "custody faults survived"
