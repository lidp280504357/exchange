#!/usr/bin/env bash
# Matching and settlement end to end (implementation plan §6.3 tasks 3 and
# 4, ADR-0015): every order trades against HOUSE, which quotes Binance's
# book; users never meet each other while market.internal_matching is off
# (user decision 2026-10-02). Two users trade ETH-BTC (Binance's ETHBTC):
# a limit buy above the ask fills at once at HOUSE's price, not at its
# limit; a limit sell below the bid likewise; a sell resting above the
# market is not taken by the other user's crossing buy, which fills against
# HOUSE instead; a post-only order that would take is rejected and one
# below the market rests; an IOC under the market is canceled unfilled; a
# market buy spends its quote amount and a market sell its quantity; a
# cancel unfreezes. Prices come from the public book each time. Fills
# carry role and fee (0.1% of what is received); the ledger settles every
# fill, so in the end each user holds the welcome funds plus what its fills
# say, with nothing frozen.
# Needs ETH-BTC in TRADING with HOUSE liquidity (scripts/ops/house.sh flags).
#
#   scripts/e2e/matching.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

call GET /v1/market/pairs/ETH-BTC ""
check '.status == "TRADING" and .reference_symbol == "ETHBTC"' "ETH-BTC accepts orders and follows Binance's ETHBTC"

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
funded() {
  [[ $(balance ETH "${SELLER[@]}") == "2 0" && $(balance BTC "${SELLER[@]}") == "0.1 0" &&
    $(balance ETH "${BUYER[@]}") == "2 0" && $(balance BTC "${BUYER[@]}") == "0.1 0" ]]
}
eventually 40 "welcome funds arrived for both (0.1 BTC, 2 ETH)" funded

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
# book sets BID and ASK from the public book (Binance's; HOUSE quotes it at
# the pair's tick, which is Binance's).
book() {
  call GET "/v1/market/ETH-BTC/depth?limit=5" "" && [[ $STATUS == 200 ]] || return 1
  BID=$(jq -r '.bids[0][0] // empty' <<<"$BODY")
  ASK=$(jq -r '.asks[0][0] // empty' <<<"$BODY")
  [[ -n $BID && -n $ASK ]]
}
eventually 40 "ETH-BTC shows a two-sided book" book
times() { awk -v p="$1" -v f="$2" 'BEGIN { printf "%.5f", p * f }'; } # times PRICE FACTOR, at the tick

echo "== a limit buy above the ask fills at once, at HOUSE's price"
book
LIMIT=$(times "$ASK" 1.02)
place "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LIMIT\",\"quantity\":\"0.04\"}" "${BUYER[@]}"
BUY1=$ORDER
eventually 40 "the buy at $LIMIT fills (FILLED)" status_is "$BUY1" FILLED "${BUYER[@]}"
check '.filled_quantity == "0.04" and (.filled_quote | tonumber) < (.frozen_amount | tonumber)' "0.04 ETH, for less than its limit froze (the rest comes back)"
call GET "/v1/orders/$BUY1/fills" "" "${BUYER[@]}"
check "(.fills | length) >= 1 and all(.fills[]; .role == \"TAKER\" and .fee_asset == \"ETH\" and (.price | tonumber) <= $LIMIT)" "taker fills at or under its limit (the ask was $ASK), the fee in ETH"
check '([.fills[].fee | tonumber] | add) == 0.00004' "0.1% of 0.04 ETH in fees"

echo "== a limit sell below the bid fills at once, at HOUSE's price"
book
LIMIT=$(times "$BID" 0.98)
place "{\"symbol\":\"ETH-BTC\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$LIMIT\",\"quantity\":\"0.05\"}" "${SELLER[@]}"
SELL1=$ORDER
eventually 40 "the sell at $LIMIT fills (FILLED)" status_is "$SELL1" FILLED "${SELLER[@]}"
call GET "/v1/orders/$SELL1/fills" "" "${SELLER[@]}"
check "(.fills | length) >= 1 and all(.fills[]; .role == \"TAKER\" and .fee_asset == \"BTC\" and (.price | tonumber) >= $LIMIT)" "taker fills at or over its limit (the bid was $BID), the fee in BTC"

echo "== users do not meet: a crossing buy takes HOUSE's ask, not the other user's sell"
book
HIGH=$(times "$ASK" 1.05)
place "{\"symbol\":\"ETH-BTC\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"0.02\"}" "${SELLER[@]}"
REST=$ORDER
eventually 40 "the seller's sell at $HIGH rests (OPEN)" status_is "$REST" OPEN "${SELLER[@]}"
place "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"0.02\"}" "${BUYER[@]}"
CROSS=$ORDER
eventually 40 "the buyer's buy at $HIGH fills" status_is "$CROSS" FILLED "${BUYER[@]}"
call GET "/v1/orders/$CROSS/fills" "" "${BUYER[@]}"
check "all(.fills[]; (.price | tonumber) < $HIGH)" "below the seller's price: HOUSE's ask (it was $ASK)"
call GET "/v1/orders/$REST" "" "${SELLER[@]}"
check '.status == "OPEN" and .filled_quantity == "0"' "the seller's sell still rests, untouched"
call DELETE "/v1/orders/$REST" "" "${SELLER[@]}"
expect 202 - "the seller cancels it"
eventually 40 "it is canceled" status_is "$REST" CANCELED "${SELLER[@]}"
check '.cancel_reason == "USER"' "canceled by the user"

echo "== post-only and IOC"
book
place "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"time_in_force\":\"POST_ONLY\",\"price\":\"$(times "$ASK" 1.01)\",\"quantity\":\"0.01\"}" "${BUYER[@]}"
POST=$ORDER
eventually 40 "a post-only buy over the ask is rejected" status_is "$POST" REJECTED "${BUYER[@]}"
check '.reject_reason == "ORDER_WOULD_TAKE"' "ORDER_WOULD_TAKE"
LOW=$(times "$BID" 0.95)
place "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"time_in_force\":\"POST_ONLY\",\"price\":\"$LOW\",\"quantity\":\"0.01\"}" "${BUYER[@]}"
MAKER=$ORDER
eventually 40 "a post-only buy under the bid rests" status_is "$MAKER" OPEN "${BUYER[@]}"
call DELETE "/v1/orders/$MAKER" "" "${BUYER[@]}"
expect 202 - "cancel it"
eventually 40 "it is canceled" status_is "$MAKER" CANCELED "${BUYER[@]}"
place "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"time_in_force\":\"IOC\",\"price\":\"$LOW\",\"quantity\":\"0.01\"}" "${BUYER[@]}"
IOC=$ORDER
eventually 40 "an IOC under the bid is canceled" status_is "$IOC" CANCELED "${BUYER[@]}"
check '.filled_quantity == "0" and .cancel_reason == "IOC"' "nothing filled, canceled (IOC)"

echo "== market orders"
place '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${BUYER[@]}"
MKT=$ORDER
eventually 40 "a market buy of 0.0005 BTC fills" status_is "$MKT" FILLED "${BUYER[@]}"
check '(.filled_quote | tonumber) <= 0.0005 and (.filled_quantity | tonumber) >= 0.01' "it spent at most 0.0005 BTC, on whole lots"
place '{"symbol":"ETH-BTC","side":"SELL","type":"MARKET","quantity":"0.01"}' "${SELLER[@]}"
MKTS=$ORDER
eventually 40 "a market sell of 0.01 ETH fills" status_is "$MKTS" FILLED "${SELLER[@]}"
check '.time_in_force == "IOC" and .filled_quantity == "0.01"' "IOC, 0.01 ETH sold"
call GET "/v1/orders/$MKTS/fills" "" "${SELLER[@]}"
check 'all(.fills[]; .role == "TAKER" and .fee_asset == "BTC")' "taker fills, the fee in BTC"

echo "== settlement"
# Each user holds the welcome funds (0.1 BTC, 2 ETH) plus what its fills
# say: a buy pays the quote and receives the quantity less the fee, a
# sell gives the quantity and receives the quote less the fee. What limits
# froze beyond their fills, the rests and the rejected orders came back,
# so nothing stays frozen.
settled() { # settled AUTH...
  call GET "/v1/fills?symbol=ETH-BTC&limit=50" "" "$@"
  local want
  want=$(jq -r '
    def sum(f): map(f) | add // 0;
    [.items[] | select(.side == "BUY")] as $b | [.items[] | select(.side == "SELL")] as $s |
    "\(0.1 - ($b | sum(.quote_quantity | tonumber)) + ($s | sum((.quote_quantity | tonumber) - (.fee | tonumber)))) \(2 + ($b | sum((.quantity | tonumber) - (.fee | tonumber))) - ($s | sum(.quantity | tonumber)))"' <<<"$BODY")
  local btc eth
  btc=$(balance BTC "$@")
  eth=$(balance ETH "$@")
  awk -v w="$want" -v b="$btc" -v e="$eth" 'BEGIN {
    split(w, x, " "); split(b, y, " "); split(e, z, " ")
    d1 = x[1] - y[1]; d2 = x[2] - z[1]
    exit !(d1 < 5e-9 && d1 > -5e-9 && d2 < 5e-9 && d2 > -5e-9 && y[2] == 0 && z[2] == 0)
  }'
}
eventually 40 "the buyer's balances are the welcome funds plus its fills, nothing frozen" settled "${BUYER[@]}"
eventually 40 "the seller's balances are the welcome funds plus its fills, nothing frozen" settled "${SELLER[@]}"
call GET "/v1/fills?symbol=ETH-BTC&limit=50" "" "${BUYER[@]}"
FILLS=$(jq '.items | length' <<<"$BODY")
call GET "/v1/account/ledger?type=TRADE_SETTLE&limit=50" "" "${BUYER[@]}"
check ".items | length == $((FILLS * 2))" "a TRADE_SETTLE journal per fill, on the buyer's BTC and ETH ($FILLS fills)"
call GET "/v1/account/ledger?type=TRADE_FEE&limit=50" "" "${BUYER[@]}"
check ".items | length == $FILLS" "a TRADE_FEE per fill"

echo "all matching checks passed"
