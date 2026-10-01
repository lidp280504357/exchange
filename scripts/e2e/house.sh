#!/usr/bin/env bash
# HOUSE's virtual liquidity end to end (design §8, ADR-0013, ADR-0015):
# every trade is against HOUSE, so HOUSE offers every trading pair and
# contract; every USDT pair of deploy/instruments/test.json (the top 50 and
# the 2026-10-02 extension) trades and shows Binance's book (1000SHIB in
# units of 1000); a new user's market buy of SOL-USDT fills at once
# against HOUSE at the shown ask, a market sell of what it got fills at the
# bid, a limit buy above the ask fills at once at the ask (not its limit);
# a limit buy below the book rests until canceled; ETH-BTC (quoted in BTC)
# fills at HOUSE's ask and bid too; on each contract a market buy opens a
# long against HOUSE and a reduce-only market sell closes it; afterwards
# the ledger invariants hold (HOUSE's MARKET_MAKER accounts are the
# exception to invariant 3). Needs market.reference_depth and
# market.house_liquidity on for every symbol, HOUSE seeded and the pairs
# open (scripts/ops/house.sh), and ssh to the server.
#
#   scripts/e2e/house.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

SYMBOL=SOL-USDT
PERPS=(BTC-USDT-PERP ETH-USDT-PERP)
fail() { echo "FAIL $1" >&2; exit 1; }

echo "== HOUSE offers every trading pair and contract"
call GET /v1/market/pairs ""
expect 200 - "pairs"
WANT=$(jq -r '[.pairs[] | select(.status == "TRADING") | .symbol] | join(" ")' <<<"$BODY")
call GET /v1/market/contracts ""
expect 200 - "contracts"
WANT="$WANT $(jq -r '[.contracts[] | select(.status == "TRADING") | .symbol] | join(" ")' <<<"$BODY")"
offered() { # MISSING: the symbols of WANT whose HOUSE book is empty
  local out s
  out=$(compose "exec -T market-maker wget -qO- http://127.0.0.1:9091/metrics") || return 1
  MISSING=""
  for s in $WANT; do
    grep -qF "market_house_active{symbol=\"$s\"} 1" <<<"$out" || MISSING="$MISSING $s"
  done
  [[ -z $MISSING ]]
}
offered || fail "HOUSE offers nothing on:$MISSING"
echo "ok   HOUSE offers all $(wc -w <<<"$WANT" | tr -d ' ') of them"

echo "== the USDT pairs"
USDT_PAIRS=$(jq '[.pairs[] | select(.quote_asset == "USDT")] | length' "$(dirname "$0")/../../deploy/instruments/test.json")
call GET /v1/market/pairs ""
expect 200 - "pairs"
check "[.pairs[] | select(.quote_asset == \"USDT\" and .status == \"TRADING\")] | length == $USDT_PAIRS" "all $USDT_PAIRS USDT pairs trade"
check '(.pairs[] | select(.symbol == "1000BONK-USDT")) | .reference_symbol == "BONKUSDT" and .reference_multiplier == "1000"' "1000BONK-USDT follows BONKUSDT x 1000"
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
at_exit 'call DELETE /v1/orders "" "${AUTH[@]}"; for p in "${PERPS[@]}"; do call DELETE "/v1/derivatives/orders?symbol=$p" "" "${AUTH[@]}"; done'
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

echo "== ETH-BTC, quoted in BTC: HOUSE's ask and bid"
shown ETH-BTC
ASK=$(jq -r '.asks[0][0]' <<<"$BODY")
place '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}'
EBUY=$ORDER
eventually 40 "the market buy of 0.0005 BTC is FILLED" status_is "$EBUY" FILLED
call GET "/v1/orders/$EBUY/fills" "" "${AUTH[@]}"
FILL=$(jq -r '.fills[0].price' <<<"$BODY")
near "$FILL" "$ASK" || fail "filled at $FILL, the ask was $ASK"
echo "ok   bought at $FILL (the ask was $ASK)"
shown ETH-BTC
BID=$(jq -r '.bids[0][0]' <<<"$BODY")
place '{"symbol":"ETH-BTC","side":"SELL","type":"MARKET","quantity":"0.01"}'
ESELL=$ORDER
eventually 40 "the market sell of 0.01 ETH is FILLED" status_is "$ESELL" FILLED
call GET "/v1/orders/$ESELL/fills" "" "${AUTH[@]}"
FILL=$(jq -r '.fills[0].price' <<<"$BODY")
near "$FILL" "$BID" || fail "filled at $FILL, the bid was $BID"
echo "ok   sold at $FILL (the bid was $BID)"

call POST /v1/account/transfers '{"asset":"USDT","amount":"200","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
  "${AUTH[@]}" -H "Idempotency-Key: house-in-$RUN"
expect 201 - "200 USDT to FUTURES"
position() { # position SYMBOL QTY: the caller's position on SYMBOL is QTY (none for 0)
  call GET "/v1/derivatives/positions?symbol=$1" "" "${AUTH[@]}" &&
    jq -e --arg q "$2" '([.positions[] | .quantity | tonumber] | add // 0) == ($q | tonumber)' <<<"$BODY" >/dev/null
}
for PERP in "${PERPS[@]}"; do
  echo "== $PERP: a market buy opens a long against HOUSE, a reduce-only sell closes it"
  call GET "/v1/market/contracts/$PERP" ""
  QTY=$(jq -r .min_quantity <<<"$BODY")
  call POST /v1/derivatives/orders "{\"symbol\":\"$PERP\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"$QTY\"}" "${AUTH[@]}"
  expect 202 - "a market buy of $QTY"
  eventually 40 "a long of $QTY" position "$PERP" "$QTY"
  call POST /v1/derivatives/orders "{\"symbol\":\"$PERP\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$QTY\",\"reduce_only\":true}" "${AUTH[@]}"
  expect 202 - "a reduce-only market sell"
  eventually 40 "flat again" position "$PERP" 0
done

echo "== the ledger after HOUSE's trades"
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | sed 's/^/     /'
echo "ok   the ledger invariants hold"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all HOUSE checks passed"
