#!/usr/bin/env bash
# Matching and settlement end to end (implementation plan §6.3 tasks 3 and
# 4): two users trade on ETH-BTC through the matching engine. A resting
# sell fills against a buy at its price; an IOC buy takes the rest and
# cancels its own rest; a post-only order that would take and a self-trade
# are rejected; a market buy runs out of book; a market sell takes the best
# bid; a limit buy above the ask pays the ask; a user cancels. Fills carry
# role and fee; the ledger settles every trade (TRADE_SETTLE, TRADE_FEE,
# the saved difference unfrozen) and unused funds come back when an order
# finishes, so the final balances check every step. ETH-BTC has no market
# maker (BTC-USDT has one since task 7); the prices assume no other resting
# order between 0.035 and 0.042 (the e2e scripts cancel theirs on exit).
# Needs ETH-BTC in TRADING.
#
#   scripts/e2e/matching.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

call GET /v1/market/pairs/ETH-BTC ""
check '.status == "TRADING"' "ETH-BTC accepts orders"

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
funded() { [[ $(balance ETH "${SELLER[@]}") == "2 0" && $(balance BTC "${BUYER[@]}") == "0.1 0" ]]; }
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
place '{"symbol":"ETH-BTC","side":"SELL","type":"LIMIT","price":"0.04","quantity":"0.1"}' "${SELLER[@]}"
SELL=$ORDER
eventually 40 "the sell rests (OPEN)" status_is "$SELL" OPEN "${SELLER[@]}"
place '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","price":"0.04","quantity":"0.04"}' "${BUYER[@]}"
BUY1=$ORDER
eventually 40 "the buy fills (FILLED)" status_is "$BUY1" FILLED "${BUYER[@]}"
check '.filled_quantity == "0.04" and .filled_quote == "0.0016"' "0.04 ETH for 0.0016 BTC"
eventually 40 "the sell is partly filled" status_is "$SELL" PARTIALLY_FILLED "${SELLER[@]}"
call GET "/v1/orders/$BUY1/fills" "" "${BUYER[@]}"
check '(.fills | length == 1) and (.fills[0] | .price == "0.04" and .role == "TAKER" and .fee_asset == "ETH" and .fee == "0.00004")' "the buyer's fill: taker, 0.1% in ETH"
call GET "/v1/orders/$SELL/fills" "" "${SELLER[@]}"
check '(.fills | length == 1) and (.fills[0] | .role == "MAKER" and .fee_asset == "BTC" and .fee == "0.0000016")' "the seller's fill: maker, 0.1% in BTC"

echo "== an IOC buy takes the rest and cancels its own rest"
place '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","time_in_force":"IOC","price":"0.04","quantity":"0.1"}' "${BUYER[@]}"
BUY2=$ORDER
eventually 40 "the IOC rest is canceled" status_is "$BUY2" CANCELED "${BUYER[@]}"
check '.filled_quantity == "0.06" and .cancel_reason == "IOC"' "0.06 filled, the rest canceled (IOC)"
eventually 40 "the sell is filled" status_is "$SELL" FILLED "${SELLER[@]}"
released() { [[ $(balance BTC "${BUYER[@]}") == "0.096 0" ]]; }
eventually 40 "0.004 BTC paid, the IOC's unused 0.0016 came back" released

echo "== rejections"
place '{"symbol":"ETH-BTC","side":"SELL","type":"LIMIT","price":"0.0401","quantity":"0.01"}' "${SELLER[@]}"
ASK=$ORDER
eventually 40 "a new ask rests" status_is "$ASK" OPEN "${SELLER[@]}"
place '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","time_in_force":"POST_ONLY","price":"0.0401","quantity":"0.01"}' "${BUYER[@]}"
POST=$ORDER
eventually 40 "a post-only order that would take is rejected" status_is "$POST" REJECTED "${BUYER[@]}"
check '.reject_reason == "ORDER_WOULD_TAKE"' "ORDER_WOULD_TAKE"
place '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","price":"0.0401","quantity":"0.01"}' "${SELLER[@]}"
SELF=$ORDER
eventually 40 "a buy against one's own ask is rejected" status_is "$SELF" REJECTED "${SELLER[@]}"
check '.reject_reason == "ORDER_SELF_TRADE"' "ORDER_SELF_TRADE (CANCEL_NEWEST)"

echo "== a market buy runs out of book"
place '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.001"}' "${BUYER[@]}"
MKT=$ORDER
eventually 40 "the market buy's rest is canceled" status_is "$MKT" CANCELED "${BUYER[@]}"
check '.filled_quantity == "0.01" and .filled_quote == "0.000401" and .cancel_reason == "NO_LIQUIDITY"' "0.01 ETH for 0.000401 BTC, then no more book"

echo "== a market sell takes the best bid"
place '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","price":"0.035","quantity":"0.01"}' "${BUYER[@]}"
BID=$ORDER
eventually 40 "a bid rests" status_is "$BID" OPEN "${BUYER[@]}"
place '{"symbol":"ETH-BTC","side":"SELL","type":"MARKET","quantity":"0.01"}' "${SELLER[@]}"
MKTS=$ORDER
eventually 40 "the market sell fills" status_is "$MKTS" FILLED "${SELLER[@]}"
check '.time_in_force == "IOC" and .filled_quote == "0.00035"' "IOC, 0.01 ETH for 0.00035 BTC"
call GET "/v1/orders/$MKTS/fills" "" "${SELLER[@]}"
check '(.fills | length == 1) and (.fills[0] | .price == "0.035" and .role == "TAKER" and .fee_asset == "BTC" and .fee == "0.00000035")' "the seller's fill: taker at the bid, 0.1% in BTC"

echo "== a limit buy above the ask pays the ask"
place '{"symbol":"ETH-BTC","side":"SELL","type":"LIMIT","price":"0.0405","quantity":"0.01"}' "${SELLER[@]}"
ASK2=$ORDER
eventually 40 "an ask rests at 0.0405" status_is "$ASK2" OPEN "${SELLER[@]}"
place '{"symbol":"ETH-BTC","side":"BUY","type":"LIMIT","price":"0.041","quantity":"0.01"}' "${BUYER[@]}"
IMP=$ORDER
eventually 40 "the buy fills at the ask" status_is "$IMP" FILLED "${BUYER[@]}"
check '.frozen_amount == "0.00041" and .filled_quote == "0.000405"' "0.00041 BTC frozen, 0.000405 paid (settlement returns 0.000005)"

echo "== cancels"
place '{"symbol":"ETH-BTC","side":"SELL","type":"LIMIT","price":"0.042","quantity":"0.02"}' "${SELLER[@]}"
LATE=$ORDER
eventually 40 "an ask rests" status_is "$LATE" OPEN "${SELLER[@]}"
call DELETE "/v1/orders/$LATE" "" "${SELLER[@]}"
expect 202 - "cancel it"
eventually 40 "it is canceled" status_is "$LATE" CANCELED "${SELLER[@]}"
check '.cancel_reason == "USER"' "canceled by the user"
call GET "/v1/fills?symbol=ETH-BTC" "" "${BUYER[@]}"
check '.items | length == 5' "the buyer has five fills"

echo "== settlement"
# The buyer paid 0.0016 + 0.0024 + 0.000401 + 0.00035 + 0.000405 BTC for
# 0.13 ETH less 0.1% fees (in ETH); the seller got the BTC less 0.1%
# (rounded up to the satoshi) and gave the ETH. The IOC rest, the rejected
# orders, the market buy's rest, the canceled ask and what the last buy
# saved all came back, so nothing stays frozen.
settled() {
  [[ $(balance BTC "${BUYER[@]}") == "0.094844 0" && $(balance ETH "${BUYER[@]}") == "2.12987 0" &&
    $(balance BTC "${SELLER[@]}") == "0.10515083 0" && $(balance ETH "${SELLER[@]}") == "1.87 0" ]]
}
eventually 40 "both sides settled, nothing left frozen" settled
call GET "/v1/account/ledger?type=TRADE_SETTLE&limit=50" "" "${BUYER[@]}"
check '.items | length == 10' "five TRADE_SETTLE journals, each on the buyer's BTC and ETH"
call GET "/v1/account/ledger?type=TRADE_FEE&asset=ETH" "" "${BUYER[@]}"
check '[.items[].amount] | sort == ["-0.00001", "-0.00001", "-0.00001", "-0.00004", "-0.00006"]' "the buyer's fees, in ETH"

echo "all matching checks passed"
