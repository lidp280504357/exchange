#!/usr/bin/env bash
# Spot orders end to end (implementation plan §6.3 task 2): a funded order
# is accepted (202, NEW) with its funds frozen; bad orders are refused and
# not stored; an order the balance cannot fund is stored as REJECTED; a
# client_order_id makes retries safe; cancels complete through the matching
# engine and give the frozen funds back. It trades ETH-BTC, where HOUSE
# quotes Binance's ETHBTC: prices come from that book, the orders resting
# 10% away from it (inside ETH-BTC's price band of 100% around the
# reference price). Needs ETH-BTC in TRADING and SOL-BTC not (the test data
# keeps it PREPARE). Last, spot trading is closed as a product line for a
# moment (design 2026-10-07, product switches): new orders are refused with
# PRODUCT_CLOSED, the console's cancel-open takes the resting orders (every
# user's, as when an operator closes it), and opened again orders are taken.
#
#   scripts/e2e/trading.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

echo "== pairs"
call GET /v1/market/pairs/ETH-BTC ""
expect 200 - "ETH-BTC"
check '.status == "TRADING"' "ETH-BTC accepts orders (exchangectl instruments pair-status ETH-BTC --to TRADING)"

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
funded() { [[ $(balance BTC) == "0.1 0" && $(balance ETH) == "2 0" ]]; }
eventually 40 "welcome funds arrived" funded

order() { call POST /v1/orders "$1" "${AUTH[@]}" -H "Idempotency-Key: $(uuidgen 2>/dev/null || date +%s%N)"; }
# BID and ASK: Binance's ETHBTC book, which HOUSE quotes.
book() {
  call GET "/v1/market/ETH-BTC/depth?limit=5" "" && [[ $STATUS == 200 ]] || return 1
  BID=$(jq -r '.bids[0][0] // empty' <<<"$BODY")
  ASK=$(jq -r '.asks[0][0] // empty' <<<"$BODY")
  [[ -n $BID && -n $ASK ]]
}
eventually 40 "ETH-BTC shows a two-sided book" book
times() { awk -v p="$1" -v f="$2" 'BEGIN { printf "%.5f", p * f }'; } # times PRICE FACTOR, at the tick (0.00001)
LOW=$(times "$BID" 0.9)   # a buy that rests
HIGH=$(times "$ASK" 1.1)  # a sell that rests
FAR=$(times "$ASK" 3)     # above the band (2 x the reference price)
FROZEN=$(awk -v p="$LOW" 'BEGIN { printf "%.6f", p * 0.1 }')
# holds ASSET AVAILABLE FROZEN: the SPOT account holds those (compared as numbers).
holds() {
  awk -v got="$(balance "$1")" -v a="$2" -v f="$3" 'BEGIN { split(got, x, " "); d1 = x[1] - a; d2 = x[2] - f; exit !(d1 < 1e-12 && d1 > -1e-12 && d2 < 1e-12 && d2 > -1e-12) }'
}

echo "== a limit buy at $LOW (the bid is $BID)"
BUY="{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\",\"client_order_id\":\"e2e-$RUN\"}"
order "$BUY"
expect 202 - "limit buy accepted"
check ".status == \"NEW\" and .frozen_asset == \"BTC\" and ((.frozen_amount | tonumber) - $FROZEN | fabs) < 1e-12 and .time_in_force == \"GTC\" and .cancel_requested == false" "NEW, $FROZEN BTC frozen"
ORDER=$(jq -r .order_id <<<"$BODY")
LEFT=$(awk -v f="$FROZEN" 'BEGIN { printf "%.6f", 0.1 - f }')
holds BTC "$LEFT" "$FROZEN" || { echo "FAIL BTC balance after the buy: $(balance BTC)" >&2; exit 1; }
echo "ok   the balance shows $LEFT available, $FROZEN frozen"
order "$BUY"
expect 202 - "the same client_order_id again"
check ".order_id == \"$ORDER\"" "returns the same order"
holds BTC "$LEFT" "$FROZEN" || { echo "FAIL a retry froze again: $(balance BTC)" >&2; exit 1; }
echo "ok   nothing frozen twice"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.2\",\"client_order_id\":\"e2e-$RUN\"}"
expect 409 COMMON_IDEMPOTENCY_CONFLICT "another order under the same client_order_id"

echo "== checks refuse bad orders"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"${LOW}1\",\"quantity\":\"0.1\"}"
expect 400 INSTRUMENT_PRECISION "price off the tick"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"1001\"}"
expect 400 ORDER_QUANTITY_OUT_OF_RANGE "quantity above the maximum"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$FAR\",\"quantity\":\"0.1\"}"
expect 422 ORDER_PRICE_OUT_OF_BAND "three times the reference price, past the band"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.001\"}"
expect 422 ORDER_MIN_NOTIONAL "below the minimum notional"
order '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quantity":"0.1"}'
expect 400 COMMON_INVALID_ARGUMENT "a market buy by quantity"
order '{"symbol":"SOL-BTC","side":"BUY","type":"LIMIT","price":"0.002","quantity":"1"}'
expect 422 INSTRUMENT_NOT_TRADING "a pair that is not trading"
order '{"symbol":"NOPE-USDT","side":"BUY","type":"LIMIT","price":"1","quantity":"10"}'
expect 404 COMMON_NOT_FOUND "an unknown pair"
call GET "/v1/orders?symbol=ETH-BTC" "" "${AUTH[@]}"
check '.items | length == 1' "refused orders are not stored"

echo "== an order the balance cannot fund"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"10\"}"
expect 422 LEDGER_INSUFFICIENT_BALANCE "10 ETH's worth is more than the balance"
REJECTED=$(jq -r .details.order_id <<<"$BODY")
call GET "/v1/orders/$REJECTED" "" "${AUTH[@]}"
expect 200 - "the rejected order"
check '.status == "REJECTED" and .reject_reason == "LEDGER_INSUFFICIENT_BALANCE"' "stored as REJECTED"

echo "== a limit sell at $HIGH (the ask is $ASK)"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"0.1\"}"
expect 202 - "limit sell accepted"
check '.frozen_asset == "ETH" and .frozen_amount == "0.1"' "0.1 ETH frozen"
SELL=$(jq -r .order_id <<<"$BODY")
[[ $(balance ETH) == "1.9 0.1" ]] || { echo "FAIL ETH balance after the sell: $(balance ETH)" >&2; exit 1; }
echo "ok   the balance shows 1.9 ETH available, 0.1 frozen"
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
call DELETE "/v1/orders?symbol=ETH-BTC" "" "${AUTH[@]}"
expect 202 - "cancel the rest"
check '.requested == 1' "one more order asked to cancel"
call DELETE "/v1/orders/$REJECTED" "" "${AUTH[@]}"
expect 409 COMMON_CONFLICT "a rejected order cannot be canceled"
eventually 40 "the engine cancels both" both CANCELED
check '.cancel_reason == "USER" and .filled_quantity == "0"' "canceled by the user, nothing filled"
refunded() { [[ $(balance BTC) == "0.1 0" && $(balance ETH) == "2 0" ]]; }
eventually 40 "the frozen funds came back" refunded

echo "== spot trading closed as a product line, then open again"
exchangectl flags show product.spot >"$WORK/spot-flag" 2>/dev/null || true
jq -e '.enabled' "$WORK/spot-flag" >/dev/null 2>&1 ||
  { echo "FAIL product.spot is not on: an operator closed spot trading ($(cat "$WORK/spot-flag"))" >&2; exit 1; }
order "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\"}"
expect 202 - "a buy that rests"
RESTING=$(jq -r .order_id <<<"$BODY")
eventually 40 "the engine opens it" status_is "$RESTING" OPEN
SPOT_CLOSED=""
spot_back() {
  [[ -n $SPOT_CLOSED ]] || return 0
  if exchangectl flags set product.spot --on --reason "e2e trading.sh: spot trading open again" >/dev/null; then
    SPOT_CLOSED=""
    return 0
  fi
  echo "FAIL product.spot not opened again; by hand: exchangectl flags set product.spot --on --reason ..." >&2
  EXIT_FAILED=1
}
at_exit spot_back
SPOT_CLOSED=1
exchangectl flags set product.spot --off --reason "e2e trading.sh: spot trading closed for a moment" >/dev/null
spot_is() { call GET /v1/platform/products "" && [[ $STATUS == 200 && $(jq -r .spot.enabled <<<"$BODY") == "$1" ]]; }
eventually 30 "GET /v1/platform/products shows spot closed" spot_is false
check '.spot.closed_at != null' "with the time it closed"
# Below the minimum notional: refused by the line while it is closed, by
# its own checks once open, never stored.
TINY="{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.001\"}"
refused() { order "$TINY"; [[ $STATUS == 403 ]]; }
eventually 30 "spot-trading-service refuses new orders" refused
expect 403 PRODUCT_CLOSED "an order while spot is closed"
check '.details.product == "spot"' "the details name the line"
order "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\"}"
expect 403 PRODUCT_CLOSED "a funded order too"
internal POST spot-trading-service 8088 /internal/products/spot/cancel-open \
  '{"actor":"e2e:trading.sh","reason":"e2e: spot trading closed for a moment"}'
[[ $STATUS == 202 ]] || { echo "FAIL cancel-open: $STATUS $BODY" >&2; exit 1; }
jq -e --arg o "$RESTING" '[.orders[].order_id] | index($o) != null and (.canceled == (.orders | length))' <<<"$BODY" >/dev/null ||
  { echo "FAIL cancel-open did not take $RESTING: $BODY" >&2; exit 1; }
echo "ok   cancel-open asked to cancel $(jq -r .canceled <<<"$BODY") resting spot orders, this one among them"
eventually 40 "the engine cancels it" status_is "$RESTING" CANCELED
eventually 40 "its frozen funds came back" refunded
audited() { (( $(ch "SELECT count() FROM audit_logs WHERE actor_id = 'e2e:trading.sh' AND position(payload, '$RESTING') > 0 AND position(payload, 'admin.orders.canceled') > 0") == 1 )); }
eventually 60 "the cancel is audited (admin.orders.canceled)" audited
exchangectl flags set product.spot --on --reason "e2e trading.sh: spot trading open again" >/dev/null
SPOT_CLOSED=""
eventually 30 "GET /v1/platform/products shows spot open" spot_is true
taken() { order "$TINY"; [[ $STATUS == 422 ]]; }
eventually 30 "orders get past the line again" taken
expect 422 ORDER_MIN_NOTIONAL "the order's own checks again"

echo "all trading checks passed"
