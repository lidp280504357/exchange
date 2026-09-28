#!/usr/bin/env bash
# Matching end to end (implementation plan §6.3 task 3): two users trade on
# BTC-USDT through the matching engine. A resting sell fills against a
# buy at its price; an IOC buy takes the rest and cancels its own rest; a
# post-only order that would take and a self-trade are rejected; a market
# buy runs out of book; a market sell takes the best bid; a user cancels;
# unused funds come back when an order finishes. Fills carry role and fee.
# Settlement (moving the traded funds) is task 4, so traded amounts stay
# frozen here. The prices assume no other resting orders between 60000 and
# 71000 (the e2e scripts cancel theirs on exit). Needs BTC-USDT in TRADING.
#
#   scripts/e2e/matching.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

call GET /v1/market/pairs/BTC-USDT ""
check '.status == "TRADING"' "BTC-USDT accepts orders"

signup() { # signup NAME: registers a user, sets NAME_AUTH
  register "e2e-match-$1-$RUN@example.com" "e2e-match-$1-$RUN" "e2e match $RUN"
  eval "${1}_AUTH=(-H \"Authorization: Bearer $(jq -r .access_token <<<"$BODY")\")"
}
echo "== two traders"
signup seller
signup buyer
# shellcheck disable=SC2154 # set by signup
SELLER=("${seller_AUTH[@]}")
BUYER=("${buyer_AUTH[@]}")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${SELLER[@]}"; call DELETE /v1/orders "" "${BUYER[@]}"'

balance() { # balance ASSET AUTH... prints "available frozen"
  local asset=$1
  shift
  call GET /v1/account/balances "" "$@"
  jq -r --arg a "$asset" '.balances[] | select(.asset == $a and .account_type == "SPOT") | "\(.available) \(.frozen)"' <<<"$BODY"
}
funded() { [[ $(balance USDT "${SELLER[@]}") == "10000 0" && $(balance USDT "${BUYER[@]}") == "10000 0" ]]; }
eventually 40 "welcome funds arrived for both" funded

place() { # place JSON AUTH...; sets ORDER
  local body=$1
  shift
  call POST /v1/orders "$body" "$@"
  expect 202 - "place $(jq -r '"\(.side) \(.type) \(.time_in_force // "") \(.quantity // .quote_amount) @ \(.price // "market")"' <<<"$body")"
  ORDER=$(jq -r .order_id <<<"$BODY")
}
# status_is ORDER STATUS AUTH... succeeds once the order has STATUS.
status_is() {
  local id=$1 want=$2
  shift 2
  call GET "/v1/orders/$id" "" "$@"
  [[ $(jq -r .status <<<"$BODY") == "$want" ]]
}

echo "== a resting sell fills against a buy at its price"
place '{"symbol":"BTC-USDT","side":"SELL","type":"LIMIT","price":"70000","quantity":"0.01"}' "${SELLER[@]}"
SELL=$ORDER
eventually 40 "the sell rests (OPEN)" status_is "$SELL" OPEN "${SELLER[@]}"
place '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","price":"70000","quantity":"0.004"}' "${BUYER[@]}"
BUY1=$ORDER
eventually 40 "the buy fills (FILLED)" status_is "$BUY1" FILLED "${BUYER[@]}"
check '.filled_quantity == "0.004" and .filled_quote == "280"' "0.004 BTC for 280 USDT"
eventually 40 "the sell is partly filled" status_is "$SELL" PARTIALLY_FILLED "${SELLER[@]}"
call GET "/v1/orders/$BUY1/fills" "" "${BUYER[@]}"
check '.fills | length == 1 and (.fills[0] | .price == "70000" and .role == "TAKER" and .fee_asset == "BTC" and .fee == "0.000004")' "the buyer's fill: taker, 0.1% in BTC"
call GET "/v1/orders/$SELL/fills" "" "${SELLER[@]}"
check '.fills | length == 1 and (.fills[0] | .role == "MAKER" and .fee_asset == "USDT" and .fee == "0.28")' "the seller's fill: maker, 0.1% in USDT"

echo "== an IOC buy takes the rest and cancels its own rest"
place '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","time_in_force":"IOC","price":"70000","quantity":"0.01"}' "${BUYER[@]}"
BUY2=$ORDER
eventually 40 "the IOC rest is canceled" status_is "$BUY2" CANCELED "${BUYER[@]}"
check '.filled_quantity == "0.006" and .cancel_reason == "IOC"' "0.006 filled, the rest canceled (IOC)"
eventually 40 "the sell is filled" status_is "$SELL" FILLED "${SELLER[@]}"
released() { [[ $(balance USDT "${BUYER[@]}") == "9300 700" ]]; }
eventually 40 "the IOC's unused 280 USDT came back (700 traded, awaiting settlement)" released

echo "== rejections"
place '{"symbol":"BTC-USDT","side":"SELL","type":"LIMIT","price":"70100","quantity":"0.001"}' "${SELLER[@]}"
ASK=$ORDER
eventually 40 "a new ask rests" status_is "$ASK" OPEN "${SELLER[@]}"
place '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","time_in_force":"POST_ONLY","price":"70100","quantity":"0.001"}' "${BUYER[@]}"
POST=$ORDER
eventually 40 "a post-only order that would take is rejected" status_is "$POST" REJECTED "${BUYER[@]}"
check '.reject_reason == "ORDER_WOULD_TAKE"' "ORDER_WOULD_TAKE"
place '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","price":"70100","quantity":"0.001"}' "${SELLER[@]}"
SELF=$ORDER
eventually 40 "a buy against one's own ask is rejected" status_is "$SELF" REJECTED "${SELLER[@]}"
check '.reject_reason == "ORDER_SELF_TRADE"' "ORDER_SELF_TRADE (CANCEL_NEWEST)"

echo "== a market buy runs out of book"
place '{"symbol":"BTC-USDT","side":"BUY","type":"MARKET","quote_amount":"100"}' "${BUYER[@]}"
MKT=$ORDER
eventually 40 "the market buy's rest is canceled" status_is "$MKT" CANCELED "${BUYER[@]}"
check '.filled_quantity == "0.001" and .filled_quote == "70.1" and .cancel_reason == "NO_LIQUIDITY"' "0.001 BTC for 70.1 USDT, then no more book"

echo "== a market sell takes the best bid"
place '{"symbol":"BTC-USDT","side":"BUY","type":"LIMIT","price":"60000","quantity":"0.001"}' "${BUYER[@]}"
BID=$ORDER
eventually 40 "a bid rests" status_is "$BID" OPEN "${BUYER[@]}"
place '{"symbol":"BTC-USDT","side":"SELL","type":"MARKET","quantity":"0.001"}' "${SELLER[@]}"
MKTS=$ORDER
eventually 40 "the market sell fills" status_is "$MKTS" FILLED "${SELLER[@]}"
check '.time_in_force == "IOC" and .filled_quote == "60"' "IOC, 0.001 BTC for 60 USDT"
call GET "/v1/orders/$MKTS/fills" "" "${SELLER[@]}"
check '.fills | length == 1 and (.fills[0] | .price == "60000" and .role == "TAKER" and .fee_asset == "USDT" and .fee == "0.06")' "the seller's fill: taker at the bid, 0.1% in USDT"

echo "== cancels"
place '{"symbol":"BTC-USDT","side":"SELL","type":"LIMIT","price":"71000","quantity":"0.002"}' "${SELLER[@]}"
LATE=$ORDER
eventually 40 "an ask rests" status_is "$LATE" OPEN "${SELLER[@]}"
call DELETE "/v1/orders/$LATE" "" "${SELLER[@]}"
expect 202 - "cancel it"
eventually 40 "it is canceled" status_is "$LATE" CANCELED "${SELLER[@]}"
check '.cancel_reason == "USER"' "canceled by the user"
call GET "/v1/fills?symbol=BTC-USDT" "" "${BUYER[@]}"
check '.items | length == 4' "the buyer has four fills"

echo "== unused funds are free, traded funds wait for settlement"
# Buyer: 280 + 420 (limit buys) + 70.1 (market buy) + 60 (the bid) traded.
# Seller: 0.01 + 0.001 + 0.001 BTC traded; the rejected buy and the
# canceled ask gave everything back.
balances() {
  [[ $(balance USDT "${BUYER[@]}") == "9169.9 830.1" && $(balance BTC "${SELLER[@]}") == "0.088 0.012" &&
    $(balance USDT "${SELLER[@]}") == "10000 0" ]]
}
eventually 40 "buyer 830.1 USDT and seller 0.012 BTC frozen, the rest available" balances

echo "all matching checks passed"
