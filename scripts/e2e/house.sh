#!/usr/bin/env bash
# HOUSE's virtual liquidity end to end (design §8, ADR-0013, ADR-0015):
# the top 50 USDT pairs trade and show Binance's book (1000SHIB in units
# of 1000); a new user's market buy of SOL-USDT fills at once against
# HOUSE at the shown ask, a market sell of what it got fills at the bid, a
# limit buy above the ask fills at once at the ask (not its limit); a
# limit buy below the book rests until canceled; on BTC-USDT-PERP a market
# buy opens a long against HOUSE and a reduce-only market sell closes it;
# afterwards the ledger invariants hold (HOUSE's MARKET_MAKER accounts are
# the exception to invariant 3). Needs market.reference_depth and
# market.house_liquidity on for the pairs and BTC-USDT-PERP, HOUSE seeded
# and the pairs open (scripts/ops/house.sh), and ssh to the server.
#
#   scripts/e2e/house.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

SYMBOL=SOL-USDT
PERP=BTC-USDT-PERP

echo "== the top 50"
call GET /v1/market/pairs ""
expect 200 - "pairs"
check '[.pairs[] | select(.quote_asset == "USDT" and .status == "TRADING")] | length == 50' "50 USDT pairs trade"
check '(.pairs[] | select(.symbol == "1000SHIB-USDT")) | .reference_symbol == "SHIBUSDT" and .reference_multiplier == "1000"' "1000SHIB-USDT follows SHIBUSDT x 1000"
shown() { # shown SYMBOL: BODY holds a two-sided book of SYMBOL
  call GET "/v1/market/$1/depth?limit=5" "" && [[ $STATUS == 200 ]] &&
    jq -e '(.bids | length) > 0 and (.asks | length) > 0' <<<"$BODY" >/dev/null
}
eventually 30 "$SYMBOL shows a two-sided book" shown "$SYMBOL"
check '((.asks[0][0] | tonumber) - (.bids[0][0] | tonumber)) / (.bids[0][0] | tonumber) | . > 0 and . < 0.002' "a spread under 0.2%"
eventually 30 "1000SHIB-USDT shows a two-sided book" shown 1000SHIB-USDT
check '(.bids[0][0] | tonumber) > 0.001' "priced per 1000 SHIB"

EMAIL="e2e-house-$RUN@example.com"
echo "== register $EMAIL"
register "$EMAIL" "e2e-house-$RUN" "e2e house $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${AUTH[@]}"; call DELETE "/v1/derivatives/orders?symbol='$PERP'" "" "${AUTH[@]}"'
balance() { # balance ASSET prints the SPOT account's available amount
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '[.balances[] | select(.asset == $a and .account_type == "SPOT")][0].available // "0"' <<<"$BODY"
}
funded() { [[ $(balance USDT) != 0 ]]; }
eventually 40 "welcome funds arrived" funded

place() { # place JSON; sets ORDER
  call POST /v1/orders "$1" "${AUTH[@]}" -H "Idempotency-Key: $(uuidgen 2>/dev/null || date +%s%N)"
  expect 202 - "place $(jq -r '"\(.side) \(.type) \(.quantity // .quote_amount) \(.symbol) @ \(.price // "market")"' <<<"$1")"
  ORDER=$(jq -r .order_id <<<"$BODY")
}
status_is() { # status_is ORDER STATUS
  call GET "/v1/orders/$1" "" "${AUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == "$2" ]]
}
fail() { echo "FAIL $1" >&2; exit 1; }
near() { # near PRICE REF: within 0.5%
  awk -v p="$1" -v r="$2" 'BEGIN { d = (p - r) / r; exit !(d < 0.005 && d > -0.005) }'
}

echo "== a market buy fills against HOUSE"
shown "$SYMBOL"
ASK=$(jq -r '.asks[0][0]' <<<"$BODY")
place "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quote_amount\":\"20\"}"
BUY=$ORDER
eventually 40 "the market buy is FILLED" status_is "$BUY" FILLED
check '(.filled_quote | tonumber) > 19 and (.filled_quote | tonumber) <= 20' "about 20 USDT spent"
call GET "/v1/orders/$BUY/fills" "" "${AUTH[@]}"
check '(.fills | length) >= 1 and all(.fills[]; .role == "TAKER" and .fee_asset == "SOL")' "taker fills, the fee in SOL"
FILL=$(jq -r '.fills[0].price' <<<"$BODY")
near "$FILL" "$ASK" || fail "filled at $FILL, the ask was $ASK"
echo "ok   filled at $FILL (the ask was $ASK)"

echo "== a market sell of what it got fills at the bid"
GOT=$(balance SOL)
QTY=$(awk -v q="$GOT" 'BEGIN { printf "%.3f", int(q * 1000) / 1000 }')
shown "$SYMBOL"
BID=$(jq -r '.bids[0][0]' <<<"$BODY")
place "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$QTY\"}"
SELL=$ORDER
eventually 40 "the market sell is FILLED" status_is "$SELL" FILLED
call GET "/v1/orders/$SELL/fills" "" "${AUTH[@]}"
FILL=$(jq -r '.fills[0].price' <<<"$BODY")
near "$FILL" "$BID" || fail "filled at $FILL, the bid was $BID"
echo "ok   sold $QTY SOL at $FILL (the bid was $BID)"

echo "== a limit buy above the ask fills at once, at HOUSE's price"
shown "$SYMBOL"
ASK=$(jq -r '.asks[0][0]' <<<"$BODY")
HIGH=$(awk -v a="$ASK" 'BEGIN { printf "%.2f", int(a * 100.5) / 100 }')
place "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"0.1\"}"
LIM=$ORDER
eventually 40 "the limit buy at $HIGH is FILLED" status_is "$LIM" FILLED
call GET "/v1/orders/$LIM/fills" "" "${AUTH[@]}"
FILL=$(jq -r '.fills[0].price' <<<"$BODY")
near "$FILL" "$ASK" && awk -v f="$FILL" -v h="$HIGH" 'BEGIN { exit !(f <= h) }' || fail "filled at $FILL, the ask was $ASK, the limit $HIGH"
echo "ok   filled at $FILL, not at its limit $HIGH (the ask was $ASK)"

echo "== a limit buy below the book rests until canceled"
LOW=$(awk -v b="$BID" 'BEGIN { printf "%.2f", int(b * 95) / 100 }')
place "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\"}"
REST=$ORDER
eventually 40 "the bid at $LOW rests (OPEN)" status_is "$REST" OPEN
call DELETE "/v1/orders/$REST" "" "${AUTH[@]}"
expect 202 - "cancel it"
eventually 40 "it is canceled" status_is "$REST" CANCELED

echo "== $PERP: a market buy opens a long against HOUSE, a reduce-only sell closes it"
call POST /v1/account/transfers '{"asset":"USDT","amount":"200","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
  "${AUTH[@]}" -H "Idempotency-Key: house-in-$RUN"
expect 201 - "200 USDT to FUTURES"
position() { # position QTY: the caller's position on PERP is QTY (none for 0)
  call GET "/v1/derivatives/positions?symbol=$PERP" "" "${AUTH[@]}" &&
    jq -e --arg q "$1" '([.positions[] | .quantity | tonumber] | add // 0) == ($q | tonumber)' <<<"$BODY" >/dev/null
}
call POST /v1/derivatives/orders "{\"symbol\":\"$PERP\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"0.001\"}" "${AUTH[@]}"
expect 202 - "a market buy of 0.001"
eventually 40 "a long of 0.001" position 0.001
call POST /v1/derivatives/orders "{\"symbol\":\"$PERP\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"0.001\",\"reduce_only\":true}" "${AUTH[@]}"
expect 202 - "a reduce-only market sell"
eventually 40 "flat again" position 0

echo "== the ledger after HOUSE's trades"
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | sed 's/^/     /'
echo "ok   the ledger invariants hold"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all HOUSE checks passed"
