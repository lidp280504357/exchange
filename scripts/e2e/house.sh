#!/usr/bin/env bash
# HOUSE's virtual liquidity end to end (design §8, ADR-0013, ADR-0015):
# every trade is against HOUSE, so HOUSE offers every trading pair and
# contract; every USDT pair of deploy/instruments/test.json (the top 50 and
# the 2026-10-02 extension) trades and shows Binance's book (1000SHIB in
# units of 1000); a new user's market buy of SOL-USDT fills at once
# against HOUSE at the shown ask, a market sell of what it got fills at the
# bid, a limit buy above the ask fills at once at the ask (not its limit);
# a limit buy below the book rests until canceled; ETH-BTC (quoted in BTC)
# fills at HOUSE's ask and bid too; a market buy of about 5 BTC on
# BTC-USDT and a market sell of 5 each fill whole in one order; on each
# contract a market buy opens a long against HOUSE and a reduce-only market
# sell closes it; a market buy of 5 BTC on BTC-USDT-PERP and the
# reduce-only sell closing it each fill whole in one order; afterwards the
# ledger invariants hold (HOUSE's MARKET_MAKER accounts are the exception
# to invariant 3). The 5 BTC steps credit the user what they need (ledger
# adjustments, ledger.manual_adjustment) and take it back at the end.
# Needs market.reference_depth and
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
# Pairs with their own market (the platform coin, ASTRA design §2) have no
# HOUSE, nor have the contracts on them (ASTRA-USDT-PERP).
WANT=$(jq -r '[.pairs[] | select(.status == "TRADING" and .reference_symbol != null) | .symbol] | join(" ")' <<<"$BODY")
FOLLOWED=$(jq -c '[.pairs[] | select(.reference_symbol != null) | .symbol]' <<<"$BODY")
call GET /v1/market/contracts ""
expect 200 - "contracts"
WANT="$WANT $(jq -r --argjson followed "$FOLLOWED" \
  '[.contracts[] | select(.status == "TRADING" and (.index_symbol as $i | $followed | index($i) != null)) | .symbol] | join(" ")' <<<"$BODY")"
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
USDT_PAIRS=$(jq '[.pairs[] | select(.quote_asset == "USDT" and .reference_symbol != null)] | length' "$(dirname "$0")/../../deploy/instruments/test.json")
call GET /v1/market/pairs ""
expect 200 - "pairs"
check "[.pairs[] | select(.quote_asset == \"USDT\" and .reference_symbol != null and .status == \"TRADING\")] | length == $USDT_PAIRS" "all $USDT_PAIRS USDT pairs following Binance trade"
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
USER_ID=$(jq -r .user_id <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${AUTH[@]}"; for p in "${PERPS[@]}"; do call DELETE "/v1/derivatives/orders?symbol=$p" "" "${AUTH[@]}"; done'
balance() { # balance ASSET prints the SPOT account's available amount
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '[.balances[] | select(.asset == $a and .account_type == "SPOT")][0].available // "0"' <<<"$BODY"
}
funded() { [[ $(balance USDT) != 0 ]]; }
eventually 40 "welcome funds arrived" funded
# The 5 BTC steps need more than the welcome funds: credit AMOUNT USDT
# (a test-server ledger adjustment, as the bots get theirs; needs
# ledger.manual_adjustment), given back as the script ends.
CREDITED=0
credit() { # credit AMOUNT KEY
  remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger adjust --user $USER_ID --asset USDT --amount $1 --reason $(printf %q "e2e house.sh: the 5 BTC market orders") --key e2e-house-$RUN-$2" >/dev/null
  CREDITED=$(awk -v a="$CREDITED" -v b="$1" 'BEGIN { printf "%.2f", a + b }')
}
# give_back: the futures balance back to SPOT, then what was credited
# taken back (at most what SPOT holds); a failure only warns.
give_back() {
  [[ $CREDITED != 0 ]] || return 0
  local futures back
  call GET /v1/account/balances "" "${AUTH[@]}" || true
  futures=$(jq -r '[.balances[] | select(.asset == "USDT" and .account_type == "FUTURES")][0].available // "0"' <<<"$BODY")
  if awk -v f="$futures" 'BEGIN { exit !(f > 0) }'; then
    call POST /v1/account/transfers "{\"asset\":\"USDT\",\"amount\":\"$futures\",\"from_account_type\":\"FUTURES\",\"to_account_type\":\"SPOT\"}" \
      "${AUTH[@]}" -H "Idempotency-Key: house-out-$RUN" || true
  fi
  back=$(awk -v c="$CREDITED" -v s="$(balance USDT)" 'BEGIN { m = (s < c) ? s : c; printf "%.2f", int(m * 100) / 100 }')
  if ! remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger adjust --user $USER_ID --asset USDT --amount -$back --reason $(printf %q "e2e house.sh: the credit given back") --key e2e-house-$RUN-back" >/dev/null; then
    echo "WARN the $CREDITED USDT credited to $USER_ID were not taken back" >&2
  fi
  CREDITED=0
}
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'give_back'

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

# One market order of about 5 BTC each way on BTC-USDT fills whole (review
# FI, C46 5): HOUSE's room on a book is its inventory over the books that
# spend it, so the test server holds at least 10 BTC's worth for each
# (scripts/ops/house.sh seed, v4), and HOUSE offers the reference market's
# book past its best 20 levels (they held 2.6 BTC at times), the rest of it
# merged into a few levels more.
echo "== BTC-USDT: a market buy of about 5 BTC and a market sell of 5, each filled whole"
eventually 30 "BTC-USDT shows a two-sided book" shown BTC-USDT
SPEND=$(jq -r '.asks[0][0] | tonumber * 500 | ceil / 100' <<<"$BODY")
BTC_BEFORE=$(balance BTC)
# sell_btc_back: whatever BTC the steps below leave above what there was
# before them is sold back, so a run that stops between the two orders
# leaves no BTC behind (review FT, C50).
sell_btc_back() {
  local left
  left=$(awk -v b="$(balance BTC)" -v a="$BTC_BEFORE" 'BEGIN { q = int((b - a) * 10000) / 10000; if (q >= 0.0001) printf "%.4f", q }')
  [[ -n $left ]] || return 0
  call POST /v1/orders "{\"symbol\":\"BTC-USDT\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$left\"}" "${AUTH[@]}" \
    -H "Idempotency-Key: house-back-$RUN" || true
}
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'sell_btc_back'
credit "$(awk -v s="$SPEND" 'BEGIN { printf "%.2f", s + 100 }')" spot
place "{\"symbol\":\"BTC-USDT\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quote_amount\":\"$SPEND\"}"
BIG=$ORDER
eventually 40 "the market buy of $SPEND USDT is FILLED" status_is "$BIG" FILLED
check '(.filled_quantity | tonumber) > 4.9' "about 5 BTC bought in one order ($(jq -r .filled_quantity <<<"$BODY"))"
place '{"symbol":"BTC-USDT","side":"SELL","type":"MARKET","quantity":"5"}'
BIG=$ORDER
eventually 40 "the market sell of 5 BTC is FILLED" status_is "$BIG" FILLED
check '(.filled_quantity | tonumber) == 5' "all 5 sold in one order"

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

# One market order of 5 BTC, opening and closing, fills whole (review FE,
# C44: with levels of at most 20,000 USDT a reduce-only market sell of 5
# BTC filled 1.567 and the user had to repeat it). A market buy reserves
# at its protection price, the mark plus the contract's band: the margin
# at 50x (the most for 5 BTC's notional) and the taker fee there, 2% over
# (review FI, C46 3: a fixed 9,500 USDT was refused with BTC above about
# 90,000), credited.
echo "== BTC-USDT-PERP: a market buy of 5 and a reduce-only market sell of 5, each filled whole"
call GET /v1/market/contracts/BTC-USDT-PERP ""
expect 200 - "BTC-USDT-PERP's terms"
SPEC=$BODY
call GET /v1/market/BTC-USDT-PERP/mark-price ""
expect 200 - "BTC-USDT-PERP's mark price"
NEED=$(jq -rn --argjson spec "$SPEC" --argjson m "$(jq .mark_price <<<"$BODY")" \
  '5 * ($m | tonumber) * (1 + ($spec.price_band | tonumber)) * (1 / 50 + ($spec.taker_fee_rate | tonumber)) * 1.02 | ceil')
credit "$NEED" perp
call POST /v1/account/transfers "{\"asset\":\"USDT\",\"amount\":\"$NEED\",\"from_account_type\":\"SPOT\",\"to_account_type\":\"FUTURES\"}" \
  "${AUTH[@]}" -H "Idempotency-Key: house-big-in-$RUN"
expect 201 - "$NEED USDT more to FUTURES"
call PUT /v1/derivatives/settings/BTC-USDT-PERP '{"leverage":50}' "${AUTH[@]}"
expect 200 - "50x"
whole() { # whole ORDER: FILLED, all 5
  call GET "/v1/derivatives/orders/$1" "" "${AUTH[@]}" &&
    jq -e '.status == "FILLED" and .filled_quantity == "5"' <<<"$BODY" >/dev/null
}
call POST /v1/derivatives/orders '{"symbol":"BTC-USDT-PERP","side":"BUY","type":"MARKET","quantity":"5"}' "${AUTH[@]}"
expect 202 - "a market buy of 5 BTC"
BIG=$(jq -r .order_id <<<"$BODY")
eventually 40 "the buy FILLED, all 5" whole "$BIG"
eventually 40 "a long of 5" position BTC-USDT-PERP 5
call POST /v1/derivatives/orders '{"symbol":"BTC-USDT-PERP","side":"SELL","type":"MARKET","quantity":"5","reduce_only":true}' "${AUTH[@]}"
expect 202 - "a reduce-only market sell of 5"
BIG=$(jq -r .order_id <<<"$BODY")
eventually 40 "the sell FILLED, all 5, in one order" whole "$BIG"
eventually 40 "flat again" position BTC-USDT-PERP 0

echo "== the ledger after HOUSE's trades"
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | sed 's/^/     /'
echo "ok   the ledger invariants hold"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all HOUSE checks passed"
