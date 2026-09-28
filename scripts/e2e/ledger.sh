#!/usr/bin/env bash
# End-to-end check of the ledger: welcome funds, balances, idempotent spot
# <-> futures transfers and the fund flow, against a deployed test
# environment with ledger.welcome_credit and account.transfer switched on
# (docs/runbook/ledger.md).
#
#   scripts/e2e/ledger.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
EMAIL="e2e-ledger-$RUN@example.com"
DEVICE="e2e-ledger-$RUN"

echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "e2e ledger $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")

echo "== welcome funds"
funded() {
  call GET "/v1/account/balances?account_type=SPOT" "" "${AUTH[@]}"
  [[ $STATUS == 200 && $(jq -r '[.balances[] | select(.asset == "USDT")][0].available // "0"' <<<"$BODY") == "10000" ]]
}
eventually 40 "10000 USDT credited" funded
check '[.balances[].asset] | contains(["BTC","ETH","USDT"])' "every welcome asset credited"

echo "== transfers"
KEY="e2e-$RUN"
transfer() { # transfer KEY AMOUNT FROM TO
  call POST /v1/account/transfers "{\"asset\":\"USDT\",\"amount\":\"$2\",\"from_account_type\":\"$3\",\"to_account_type\":\"$4\"}" \
    "${AUTH[@]}" -H "Idempotency-Key: $1"
}
transfer "$KEY" 2500.5 SPOT FUTURES
expect 201 - "spot -> futures"
check '.status == "COMPLETED" and .amount == "2500.5"' "transfer completed"
ID=$(jq -r .transfer_id <<<"$BODY")
transfer "$KEY" 2500.5 SPOT FUTURES
expect 201 - "retry with the same key"
check ".transfer_id == \"$ID\"" "same transfer returned"
transfer "$KEY" 1 SPOT FUTURES
expect 409 COMMON_IDEMPOTENCY_CONFLICT "same key, other body"
transfer "$KEY-big" 999999 SPOT FUTURES
expect 422 LEDGER_INSUFFICIENT_BALANCE "not enough funds"
transfer "$KEY-big" 999999 SPOT FUTURES
expect 422 LEDGER_INSUFFICIENT_BALANCE "failure replayed"
transfer "$KEY-fine" 0.0000001 SPOT FUTURES
expect 400 LEDGER_AMOUNT_PRECISION "too many decimals"
call POST /v1/account/transfers '{"asset":"USDT","amount":"1","from_account_type":"SPOT","to_account_type":"FUTURES"}' "${AUTH[@]}"
expect 400 COMMON_INVALID_ARGUMENT "Idempotency-Key required"

call GET "/v1/account/balances" "" "${AUTH[@]}"
check '([.balances[] | select(.asset == "USDT" and .account_type == "SPOT")][0].available == "7499.5") and ([.balances[] | select(.asset == "USDT" and .account_type == "FUTURES")][0].available == "2500.5")' "both sides moved"
call GET "/v1/account/transfers?limit=10" "" "${AUTH[@]}"
check '(.items | length) == 2 and .items[0].status == "FAILED" and .items[1].status == "COMPLETED"' "transfer history"
call GET "/v1/account/ledger?asset=USDT&limit=10" "" "${AUTH[@]}"
check '[.items[].entry_type] == ["ACCOUNT_TRANSFER","ACCOUNT_TRANSFER","MANUAL_ADJUSTMENT"]' "fund flow"

echo "all ledger checks passed"
