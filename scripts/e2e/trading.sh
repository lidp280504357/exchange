#!/usr/bin/env bash
# Spot orders end to end (implementation plan §6.3 task 2): a funded order
# is accepted (202, NEW) with its funds frozen; bad orders are refused and
# not stored; an order the balance cannot fund is stored as REJECTED; a
# client_order_id makes retries safe; cancels complete through the matching
# engine and give the frozen funds back. The orders rest far from the
# prices of matching.sh. Needs BTC-USDT in TRADING and ETH-BTC not.
#
#   scripts/e2e/trading.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

echo "== pairs"
call GET /v1/market/pairs/BTC-USDT ""
expect 200 - "BTC-USDT"
check '.status == "TRADING"' "BTC-USDT accepts orders (exchangectl instruments pair-status BTC-USDT --to TRADING)"

EMAIL="e2e-trade-$RUN@example.com"
DEVICE="e2e-trade-$RUN"
echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "e2e trade $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${AUTH[@]}"'

# balance ASSET prints "available frozen" of the SPOT account.
balance() {
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '.balances[] | select(.asset == $a and .account_type == "SPOT") | "\(.available) \(.frozen)"' <<<"$BODY"
}
funded() { [[ $(balance USDT) == "10000 0" ]]; }
eventually 40 "welcome funds arrived" funded

order() { call POST /v1/orders "$1" "${AUTH[@]}" -H "Idempotency-Key: $(uuidgen 2>/dev/null || date +%s%N)"; }

echo "== a limit buy"
BUY="{\"symbol\":\"BTC-USDT\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"50000\",\"quantity\":\"0.001\",\"client_order_id\":\"e2e-$RUN\"}"
order "$BUY"
expect 202 - "limit buy accepted"
check '.status == "NEW" and .frozen_asset == "USDT" and .frozen_amount == "50" and .time_in_force == "GTC" and .cancel_requested == false' "NEW, 50 USDT frozen"
ORDER=$(jq -r .order_id <<<"$BODY")
[[ $(balance USDT) == "9950 50" ]] || { echo "FAIL USDT balance after the buy: $(balance USDT)" >&2; exit 1; }
echo "ok   the balance shows 9950 available, 50 frozen"
order "$BUY"
expect 202 - "the same client_order_id again"
check ".order_id == \"$ORDER\"" "returns the same order"
[[ $(balance USDT) == "9950 50" ]] || { echo "FAIL a retry froze again: $(balance USDT)" >&2; exit 1; }
echo "ok   nothing frozen twice"
order "${BUY/0.001/0.002}"
expect 409 COMMON_IDEMPOTENCY_CONFLICT "another order under the same client_order_id"

echo "== checks refuse bad orders"
order '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","price":"50000.001","quantity":"0.001"}'
expect 400 INSTRUMENT_PRECISION "price off the tick"
order '{"symbol":"BTC-USDT","side":"SELL","type":"LIMIT","price":"50000","quantity":"101"}'
expect 400 ORDER_QUANTITY_OUT_OF_RANGE "quantity above the maximum"
order '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","price":"100","quantity":"0.001"}'
expect 422 ORDER_MIN_NOTIONAL "below the minimum notional"
order '{"symbol":"BTC-USDT","side":"BUY","type":"MARKET","quantity":"0.001"}'
expect 400 COMMON_INVALID_ARGUMENT "a market buy by quantity"
order '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","price":"0.05","quantity":"0.01"}'
expect 422 INSTRUMENT_NOT_TRADING "a pair that is not trading"
order '{"symbol":"DOGE-USDT","side":"BUY","type":"LIMIT","price":"1","quantity":"10"}'
expect 404 COMMON_NOT_FOUND "an unknown pair"
call GET "/v1/orders?symbol=BTC-USDT" "" "${AUTH[@]}"
check '.items | length == 1' "refused orders are not stored"

echo "== an order the balance cannot fund"
order '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","price":"50000","quantity":"1"}'
expect 422 LEDGER_INSUFFICIENT_BALANCE "50000 USDT is more than the balance"
REJECTED=$(jq -r .details.order_id <<<"$BODY")
call GET "/v1/orders/$REJECTED" "" "${AUTH[@]}"
expect 200 - "the rejected order"
check '.status == "REJECTED" and .reject_reason == "LEDGER_INSUFFICIENT_BALANCE"' "stored as REJECTED"

echo "== a limit sell"
order '{"symbol":"BTC-USDT","side":"SELL","type":"LIMIT","price":"150000","quantity":"0.01"}'
expect 202 - "limit sell accepted"
check '.frozen_asset == "BTC" and .frozen_amount == "0.01"' "0.01 BTC frozen"
SELL=$(jq -r .order_id <<<"$BODY")
[[ $(balance BTC) == "0.09 0.01" ]] || { echo "FAIL BTC balance after the sell: $(balance BTC)" >&2; exit 1; }
echo "ok   the balance shows 0.09 BTC available, 0.01 frozen"
status_is() { call GET "/v1/orders/$1" "" "${AUTH[@]}"; [[ $(jq -r .status <<<"$BODY") == "$2" ]]; }
both() { status_is "$ORDER" "$1" && status_is "$SELL" "$1"; }
eventually 40 "the engine opens both orders" both OPEN

echo "== lists"
call GET "/v1/orders?status=ACTIVE" "" "${AUTH[@]}"
check '.items | length == 2' "two active orders"
call GET "/v1/orders?status=ACTIVE&limit=1" "" "${AUTH[@]}"
check '(.items | length == 1) and .next_cursor != null' "pages carry a cursor"
call GET "/v1/orders?status=REJECTED" "" "${AUTH[@]}"
check '.items | length == 1' "one rejected order"
call GET "/v1/orders/$(uuidgen | tr 'A-Z' 'a-z')" "" "${AUTH[@]}"
expect 404 COMMON_NOT_FOUND "an order that is not the caller's"

echo "== cancels"
call DELETE "/v1/orders/$ORDER" "" "${AUTH[@]}"
expect 202 - "cancel the buy"
check '.cancel_requested == true' "cancel requested"
call DELETE "/v1/orders?symbol=BTC-USDT" "" "${AUTH[@]}"
expect 202 - "cancel the rest"
check '.requested == 1' "one more order asked to cancel"
call DELETE "/v1/orders/$REJECTED" "" "${AUTH[@]}"
expect 409 COMMON_CONFLICT "a rejected order cannot be canceled"
eventually 40 "the engine cancels both" both CANCELED
check '.cancel_reason == "USER" and .filled_quantity == "0"' "canceled by the user, nothing filled"
refunded() { [[ $(balance USDT) == "10000 0" && $(balance BTC) == "0.1 0" ]]; }
eventually 40 "the frozen funds came back" refunded

echo "all trading checks passed"
