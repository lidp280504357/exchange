#!/usr/bin/env bash
# Coin-margined perpetuals end to end (coin-margined design 2026-10-06 §2,
# batch G1) on BTC-USD-PERP, against HOUSE (every order trades against
# HOUSE): a new user buys BTC on BTC-USDT with its welcome USDT and moves
# 0.002 BTC to FUTURES. Its BTC account answers on ?asset=BTC apart from
# the USDT one, and an asset no contract settles in is
# DERIV_SETTLE_ASSET_MISMATCH; 1.5 contracts are
# DERIV_CONTRACTS_NOT_INTEGER. A market buy of 3 contracts (300 USD) opens
# a long against HOUSE (cross, 20x): 3 contracts of 100 USD settled in
# BTC, worth 300 USD and 300 / mark in BTC, the reservation in BTC. A
# reduce-only market sell closes it: the realized PnL and the fees are in
# BTC, the FUTURES BTC holds nothing frozen and is what went in plus the
# PnL less the fees, and it moves back to SPOT. The derivatives
# reconciliation (invariant 6 per settlement asset) passes. Prices come
# from the mark price and HOUSE's book (Binance COIN-M's).
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
call GET "/v1/derivatives/account?asset=DOGE" "" "${AUTH[@]}"
expect 400 DERIV_SETTLE_ASSET_MISMATCH "no contract settles in DOGE"

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
eventually 40 "$SYMBOL shows HOUSE's asks" offered
echo "ok   mark $MARK"

echo "== whole contracts only"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"1.5\"}" "${AUTH[@]}"
expect 400 DERIV_CONTRACTS_NOT_INTEGER "1.5 contracts"

echo "== open: a market buy of 3 contracts against HOUSE"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"3\"}" "${AUTH[@]}"
expect 202 - "a market buy of 3"
check '.settle_asset == "BTC" and (.reserved | tonumber) > 0' "its margin and fee reserved in BTC"
OPEN=$(jq -r .order_id <<<"$BODY")
position() {
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "${AUTH[@]}" && jq -e '.positions | length == 1' <<<"$BODY" >/dev/null
}
eventually 40 "the long is open" position
check '.positions[0].quantity == "3" and .positions[0].contracts == "3" and .positions[0].settle_asset == "BTC" and .positions[0].value_usd == "300"' \
  "3 contracts of 100 USD settled in BTC"
check '((.positions[0].value_coin | tonumber) - 300 / (.positions[0].mark_price | tonumber) | fabs) < 1e-8' "worth 300 / mark in BTC"
call GET "/v1/derivatives/orders/$OPEN" "" "${AUTH[@]}"
check '.status == "FILLED" and .reserved == "0" and (.fee | tonumber) > 0 and .settle_asset == "BTC"' "filled, a fee paid in BTC"

echo "== close: a reduce-only market sell"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"3\",\"reduce_only\":true}" "${AUTH[@]}"
expect 202 - "the reduce-only sell of 3"
flat() {
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "${AUTH[@]}" && jq -e '.positions | length == 0' <<<"$BODY" >/dev/null
}
eventually 40 "flat" flat
# books: every fill settled in BTC, 3 closed; sets PNL and FEES.
books() {
  call GET "/v1/derivatives/fills?symbol=$SYMBOL&limit=50" "" "${AUTH[@]}"
  jq -e '(.items | length) >= 2 and all(.items[]; .settled and .settle_asset == "BTC")
    and ([.items[].closed_quantity | tonumber] | add) == 3' <<<"$BODY" >/dev/null || return 1
  PNL=$(jq '[.items[].realized_pnl | tonumber] | add' <<<"$BODY")
  FEES=$(jq '[.items[].fee | tonumber] | add' <<<"$BODY")
}
eventually 20 "the fills: settled in BTC, 3 closed" books
echo "ok   PnL $PNL BTC, fees $FEES BTC"
settled() {
  call GET "/v1/derivatives/account?asset=BTC" "" "${AUTH[@]}" && jq -e '.frozen == "0" and .order_margin == "0" and .position_margin == "0"' <<<"$BODY" >/dev/null
}
eventually 20 "the BTC FUTURES holds nothing frozen" settled
check "((.wallet_balance | tonumber) - ($IN + $PNL - $FEES) | fabs) < 1e-8" "$IN + the PnL less the fees"

echo "== back to SPOT"
AVAILABLE=$(jq -r .available <<<"$BODY")
call POST /v1/account/transfers "{\"asset\":\"BTC\",\"amount\":\"$AVAILABLE\",\"from_account_type\":\"FUTURES\",\"to_account_type\":\"SPOT\"}" \
  "${AUTH[@]}" -H "Idempotency-Key: coinm-out-$RUN"
expect 201 - "move $AVAILABLE BTC back"

echo "== the derivatives reconciliation, every settlement asset"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all coin-margined checks passed"
