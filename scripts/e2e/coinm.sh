#!/usr/bin/env bash
# Coin-margined perpetuals end to end (coin-margined design 2026-10-06 §2,
# batch G1) on BTC-USD-PERP, against HOUSE (every order trades against
# HOUSE): a new user buys BTC on BTC-USDT with its welcome USDT and moves
# 0.002 BTC to FUTURES. Its BTC account answers on ?asset=BTC apart from
# the USDT one, and an asset no contract settles in is
# DERIV_SETTLE_ASSET_MISMATCH; 1.5 contracts are
# DERIV_CONTRACTS_NOT_INTEGER. A market buy of 3 contracts (300 USD) opens
# a long against HOUSE (cross, 20x): 3 contracts of 100 USD settled in
# BTC, worth 300 USD and 300 / mark in BTC, the reservation in BTC; a
# reduce-only market sell closes it. A market sell of 3 opens a short the
# same way and a reduce-only market buy closes it. With 2 contracts long,
# the coin-margined line closes for a moment as the console closes it
# (design 2026-10-07 product switches, K1b; product.coin_m off and
# derivatives-service's cancel-open, every user's open orders on the line
# canceled, the market makers' kept): an opening order is PRODUCT_CLOSED,
# a reduce-only sell of 1 trades with HOUSE; opened again, an opening buy
# of 1 is taken and a reduce-only sell of 2 closes. Every fill is settled in
# BTC; the realized PnL and the fees are in BTC, the FUTURES BTC holds
# nothing frozen and is what went in plus the PnL less the fees. Then HOUSE
# has no room for the contract (market.house_liquidity off for it, put back
# when the script ends): a market buy fills nothing, ends canceled and
# leaves nothing frozen. The BTC moves back to SPOT and the derivatives
# reconciliation (invariant 6 per settlement asset) passes. Prices come
# from the mark price and HOUSE's book (Binance COIN-M's). Liquidation and
# ADL are covered by the application tests (internal/derivatives/
# application/coinm_test.go): no price here can be moved on purpose.
# Needs derivatives.trading and derivatives.coin_m on, BTC-USD-PERP TRADING
# with HOUSE liquidity (scripts/ops/house.sh seed and flags) and a mark
# price (docs/runbook/derivatives.md), and ssh to the server.
#
#   scripts/e2e/coinm.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"
# shellcheck source=lib/products.sh
source "$(dirname "$0")/lib/products.sh"

SYMBOL=BTC-USD-PERP
IN=0.002
fail() { echo "FAIL $1" >&2; exit 1; }

# The coin-margined contracts open once the C39 gate is passed (design
# section 0): until then there is nothing to trade.
call GET "/v1/market/contracts/$SYMBOL" ""
if [[ $STATUS != 200 || $(jq -r .status <<<"$BODY") != TRADING ]]; then
  echo "SKIP $SYMBOL is not TRADING (status $(jq -r '.status // "unknown"' <<<"$BODY" 2>/dev/null))"
  exit 0
fi

register "e2e-coinm-$RUN@example.com" "e2e-coinm-$RUN" "e2e coin-m $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/derivatives/orders?symbol='$SYMBOL'" "" "${AUTH[@]}"'
balance() { # balance ACCOUNT ASSET prints the available amount
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg t "$1" --arg a "$2" '[.balances[] | select(.account_type == $t and .asset == $a)][0].available // "0"' <<<"$BODY"
}
funded() { [[ $(balance SPOT USDT) != 0 ]]; }
eventually 40 "welcome funds arrived" funded

echo "== BTC for the BTC FUTURES account"
call POST /v1/orders '{"symbol":"BTC-USDT","side":"BUY","type":"MARKET","quote_amount":"300"}' "${AUTH[@]}" -H "Idempotency-Key: coinm-btc-$RUN"
expect 202 - "a market buy of 300 USDT of BTC"
enough() { awk -v b="$(balance SPOT BTC)" -v n="$IN" 'BEGIN { exit !(b + 0 >= n + 0) }'; }
eventually 40 "the BTC arrived" enough
call POST /v1/account/transfers "{\"asset\":\"BTC\",\"amount\":\"$IN\",\"from_account_type\":\"SPOT\",\"to_account_type\":\"FUTURES\"}" \
  "${AUTH[@]}" -H "Idempotency-Key: coinm-in-$RUN"
expect 201 - "move $IN BTC to FUTURES"
call GET "/v1/derivatives/account?asset=BTC" "" "${AUTH[@]}"
expect 200 - "the BTC FUTURES account"
check ".asset == \"BTC\" and .available == \"$IN\" and .frozen == \"0\"" "$IN BTC available"
call GET /v1/derivatives/account "" "${AUTH[@]}"
expect 200 - "the USDT FUTURES account, the default"
check '.asset == "USDT" and .available == "0"' "apart from the BTC one"
# An asset no contract settles in (DOGE was one until DOGE-USD-PERP came
# with the other COIN-M contracts, G1c).
call GET "/v1/market/contracts?margin_type=ALL" ""
NONE=$(jq -r '[.contracts[].settle_asset] as $s | first(("HYPE", "PEPE", "SHIB", "WLD", "TON") | select(. as $a | $s | any(.[]; . == $a) | not)) // empty' <<<"$BODY")
[[ -n $NONE ]] || fail "every candidate asset is a contract's settlement asset"
call GET "/v1/derivatives/account?asset=$NONE" "" "${AUTH[@]}"
expect 400 DERIV_SETTLE_ASSET_MISMATCH "no contract settles in $NONE"

echo "== settings, the mark price and HOUSE's book"
call PUT "/v1/derivatives/settings/$SYMBOL" '{"leverage":20}' "${AUTH[@]}"
expect 200 - "cross, 20x"
call GET "/v1/market/$SYMBOL/mark-price" ""
expect 200 - "mark price"
MARK=$(jq -r .mark_price <<<"$BODY")
[[ "$MARK" != null ]] || fail "$SYMBOL has no mark price (market.reference_feed?)"
offered() {
  call GET "/v1/market/$SYMBOL/depth?limit=5" "" && [[ $STATUS == 200 ]] && [[ -n $(jq -r '.asks[0][0] // empty' <<<"$BODY") ]]
}
eventually 40 "$SYMBOL shows the reference book's asks" offered
quoting() { [[ $(metric market-maker 9091 market_house_active "symbol=\"$SYMBOL\"") == "$1" ]]; }
eventually 60 "HOUSE quotes $SYMBOL" quoting 1
echo "ok   mark $MARK"

echo "== whole contracts only"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"1.5\"}" "${AUTH[@]}"
expect 400 DERIV_CONTRACTS_NOT_INTEGER "1.5 contracts"

position() {
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "${AUTH[@]}" && jq -e '.positions | length == 1' <<<"$BODY" >/dev/null
}
flat() {
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "${AUTH[@]}" && jq -e '.positions | length == 0' <<<"$BODY" >/dev/null
}
# round_trip SIDE CLOSE SIGN: a market order of 3 contracts on SIDE opens a
# position (quantity SIGN3) against HOUSE; a reduce-only market order on
# CLOSE takes it back to flat.
round_trip() {
  local side=$1 close=$2 sign=$3 open
  echo "== open: a market $side of 3 contracts against HOUSE"
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"$side\",\"type\":\"MARKET\",\"quantity\":\"3\"}" "${AUTH[@]}"
  expect 202 - "a market $side of 3"
  check '.settle_asset == "BTC" and (.reserved | tonumber) > 0' "its margin and fee reserved in BTC"
  open=$(jq -r .order_id <<<"$BODY")
  eventually 40 "the position is open" position
  check ".positions[0].quantity == \"${sign}3\" and .positions[0].contracts == \"${sign}3\" and .positions[0].settle_asset == \"BTC\" and .positions[0].value_usd == \"300\"" \
    "${sign}3 contracts of 100 USD settled in BTC"
  check '((.positions[0].value_coin | tonumber) - 300 / (.positions[0].mark_price | tonumber) | fabs) < 1e-8' "worth 300 / mark in BTC"
  call GET "/v1/derivatives/orders/$open" "" "${AUTH[@]}"
  check '.status == "FILLED" and .reserved == "0" and (.fee | tonumber) > 0 and .settle_asset == "BTC"' "filled, a fee paid in BTC"

  echo "== close: a reduce-only market $close"
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"$close\",\"type\":\"MARKET\",\"quantity\":\"3\",\"reduce_only\":true}" "${AUTH[@]}"
  expect 202 - "the reduce-only $close of 3"
  eventually 40 "flat" flat
}
round_trip BUY SELL ""
round_trip SELL BUY -

echo "== the coin-margined line closed: only closes taken (product switches, K1b)"
market() { # market SIDE QUANTITY [REDUCE_ONLY]: a market order's request
  printf '{"symbol":"%s","side":"%s","type":"MARKET","quantity":"%s","reduce_only":%s}' "$SYMBOL" "$1" "$2" "${3:-false}"
}
contracts() { position && [[ $(jq -r '.positions[0].quantity' <<<"$BODY") == "$1" ]]; }
call POST /v1/derivatives/orders "$(market BUY 2)" "${AUTH[@]}"
expect 202 - "a market buy of 2 contracts"
eventually 40 "2 contracts long" contracts 2
product_close coin_m
check '.canceled == (.orders | length)' "closed: $(jq -r .canceled <<<"$BODY") open orders of the line canceled"
call POST /v1/derivatives/orders "$(market BUY 1)" "${AUTH[@]}"
expect 403 PRODUCT_CLOSED "an opening order"
check '.details.product == "coin_m"' "naming the line"
call POST /v1/derivatives/orders "$(market SELL 1 true)" "${AUTH[@]}"
expect 202 - "a reduce-only market sell of 1: the holder closes"
eventually 40 "1 contract left, against HOUSE" contracts 1
product_open coin_m
reopened() { call POST /v1/derivatives/orders "$(market BUY 1)" "${AUTH[@]}" && [[ $STATUS == 202 ]]; }
eventually 20 "open again: an opening buy of 1 is taken" reopened
eventually 40 "2 contracts long again" contracts 2
call POST /v1/derivatives/orders "$(market SELL 2 true)" "${AUTH[@]}"
expect 202 - "a reduce-only market sell of 2"
eventually 40 "flat" flat

# books: every fill settled in BTC, 9 closed; sets PNL and FEES.
books() {
  call GET "/v1/derivatives/fills?symbol=$SYMBOL&limit=50" "" "${AUTH[@]}"
  jq -e '(.items | length) >= 4 and all(.items[]; .settled and .settle_asset == "BTC")
    and ([.items[].closed_quantity | tonumber] | add) == 9' <<<"$BODY" >/dev/null || return 1
  PNL=$(jq '[.items[].realized_pnl | tonumber] | add' <<<"$BODY")
  FEES=$(jq '[.items[].fee | tonumber] | add' <<<"$BODY")
}
eventually 20 "the fills: settled in BTC, 9 closed" books
echo "ok   PnL $PNL BTC, fees $FEES BTC"
settled() {
  call GET "/v1/derivatives/account?asset=BTC" "" "${AUTH[@]}" && jq -e '.frozen == "0" and .order_margin == "0" and .position_margin == "0"' <<<"$BODY" >/dev/null
}
eventually 20 "the BTC FUTURES holds nothing frozen" settled
check "((.wallet_balance | tonumber) - ($IN + $PNL - $FEES) | fabs) < 1e-8" "$IN + the PnL less the fees"
WALLET=$(jq -r .wallet_balance <<<"$BODY")

echo "== HOUSE without room: an order fills nothing and leaves nothing frozen"
# HOUSE's room for a contract comes from its coin account (the publisher's
# rooms, tested in internal/marketmaker and internal/matching); taking the
# contract off market.house_liquidity for a moment leaves HOUSE no room at
# all, as an account used up would.
HOUSE_STATE=$(exchangectl flags show market.house_liquidity)
HOUSE_ALLOW=$(jq -r '(.rules.symbols.allow // []) | join(",")' <<<"$HOUSE_STATE")
[[ $(jq -r .enabled <<<"$HOUSE_STATE") == true && ",$HOUSE_ALLOW," == *",$SYMBOL,"* ]] ||
  fail "market.house_liquidity does not name $SYMBOL (scripts/ops/house.sh flags)"
# house_back puts the switch back once; one that cannot go back fails the
# run.
HOUSE_OFF=""
house_back() {
  [[ -n $HOUSE_OFF ]] || return 0
  if exchangectl flags set market.house_liquidity --on --allow-symbols "$HOUSE_ALLOW" --reason "e2e coinm.sh: HOUSE quotes $SYMBOL again" >/dev/null; then
    HOUSE_OFF=""
    return 0
  fi
  echo "FAIL market.house_liquidity not put back; by hand: exchangectl flags set market.house_liquidity --on --allow-symbols '$HOUSE_ALLOW' --reason ..." >&2
  EXIT_FAILED=1
}
at_exit house_back
HOUSE_OFF=1
exchangectl flags set market.house_liquidity --on --allow-symbols "$(jq -r --arg s "$SYMBOL" '[(.rules.symbols.allow // [])[] | select(. != $s)] | join(",")' <<<"$HOUSE_STATE")" \
  --reason "e2e coinm.sh: HOUSE without room on $SYMBOL for a moment" >/dev/null
eventually 60 "HOUSE stops quoting $SYMBOL" quoting 0
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"1\"}" "${AUTH[@]}"
expect 202 - "a market buy of 1 is taken"
REFUSED=$(jq -r .order_id <<<"$BODY")
ended() {
  call GET "/v1/derivatives/orders/$REFUSED" "" "${AUTH[@]}" &&
    jq -e '(.status | IN("CANCELED", "REJECTED")) and .filled_quantity == "0" and .reserved == "0"' <<<"$BODY" >/dev/null
}
eventually 40 "it fills nothing and ends canceled, its reservation released" ended
eventually 20 "nothing frozen" settled
check ".wallet_balance == \"$WALLET\"" "the balance unchanged"
house_back
eventually 60 "HOUSE quotes $SYMBOL again" quoting 1

echo "== back to SPOT"
call GET "/v1/derivatives/account?asset=BTC" "" "${AUTH[@]}"
AVAILABLE=$(jq -r .available <<<"$BODY")
call POST /v1/account/transfers "{\"asset\":\"BTC\",\"amount\":\"$AVAILABLE\",\"from_account_type\":\"FUTURES\",\"to_account_type\":\"SPOT\"}" \
  "${AUTH[@]}" -H "Idempotency-Key: coinm-out-$RUN"
expect 201 - "move $AVAILABLE BTC back"

echo "== the derivatives reconciliation, every settlement asset"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all coin-margined checks passed"
