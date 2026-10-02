#!/usr/bin/env bash
# The platform coin's simulated market end to end (ASTRA design §4, batch
# A2; docs/runbook/market-sim.md): with market-sim's bots on, ASTRA-USDT
# has a book of at least 8 levels a side within 0.3% of each other, public
# trades and 1m candles of its own (no reference market); a new user buys
# ASTRA at the market from the bots and sells it back, settled in the
# ledger like any trade; a limit order below the book rests and is
# canceled. Skipped while ASTRA-USDT is not trading or the bots are off
# (scripts/ops/astra.sh seed, open, on).
#
#   scripts/e2e/astra.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

SYMBOL=ASTRA-USDT
call GET "/v1/market/pairs/$SYMBOL" ""
expect 200 - "$SYMBOL"
if [[ $(jq -r .status <<<"$BODY") != TRADING ]]; then
  echo "skip: $SYMBOL is not trading (scripts/ops/astra.sh open)"
  exit 0
fi
check '.reference_symbol == null' "it follows no reference market"
SIM=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- http://127.0.0.1:8098/internal/sim")
if [[ $(jq -r .running <<<"$SIM") != true ]]; then
  echo "skip: the simulated market is not running (scripts/ops/astra.sh on)"
  exit 0
fi
echo "ok   market-sim runs: target $(jq -r .target_price <<<"$SIM"), $(jq '.bots | length' <<<"$SIM") bots"

echo "== the market the bots make"
booked() {
  call GET "/v1/market/$SYMBOL/depth?limit=20" "" &&
    [[ $(jq '(.bids | length) >= 8 and (.asks | length) >= 8 and ((.asks[0][0] | tonumber) - (.bids[0][0] | tonumber)) / (.bids[0][0] | tonumber) <= 0.003' <<<"$BODY") == true ]]
}
eventually 240 "a book of 8 levels a side, within 0.3%" booked
BID=$(jq -r '.bids[0][0]' <<<"$BODY")
ASK=$(jq -r '.asks[0][0]' <<<"$BODY")
echo "     best bid $BID, best ask $ASK"
traded() { call GET "/v1/market/$SYMBOL/trades?limit=10" "" && [[ $(jq '.trades | length' <<<"$BODY") -gt 0 ]]; }
eventually 480 "public trades of its own" traded
charted() { call GET "/v1/market/$SYMBOL/candles?interval=1m&limit=5" "" && [[ $(jq '.candles | length' <<<"$BODY") -gt 0 ]]; }
eventually 240 "1m candles" charted
call GET "/v1/market/$SYMBOL/ticker" ""
expect 200 - "the ticker"
check '.last != null' "a last price"

EMAIL="e2e-astra-$RUN@example.com"
DEVICE="e2e-astra-$RUN"
echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "e2e astra $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/orders?symbol=ASTRA-USDT" "" "${AUTH[@]}"'
balance() { # balance ASSET prints "available frozen" of the SPOT account
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '[.balances[] | select(.asset == $a and .account_type == "SPOT")][0] // {available: "0", frozen: "0"} | "\(.available) \(.frozen)"' <<<"$BODY"
}
funded() { [[ $(balance USDT) == "10000 0" ]]; }
eventually 80 "welcome funds arrived" funded

order() { call POST /v1/orders "$1" "${AUTH[@]}" -H "Idempotency-Key: e2e-astra-$RUN-$2"; }
echo "== buy at the market, sell back"
order "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quote_amount\":\"50\"}" buy
expect 202 - "a market buy of 50 USDT"
BUY_ID=$(jq -r .order_id <<<"$BODY")
filled() { call GET "/v1/orders/$1" "" "${AUTH[@]}" && [[ $(jq -r .status <<<"$BODY") =~ ^(FILLED|CANCELED)$ && $(jq -r '.filled_quantity | tonumber > 0' <<<"$BODY") == true ]]; }
eventually 80 "the buy filled against the bots" filled "$BUY_ID"
BOUGHT=$(jq -r .filled_quantity <<<"$BODY")
AVG=$(jq -r '(.filled_quote | tonumber) / (.filled_quantity | tonumber) * 10000 | round / 10000' <<<"$BODY")
echo "     bought $BOUGHT ASTRA at $AVG"
[[ $(jq -n "$AVG <= $ASK * 1.01") == true ]] || { echo "FAIL bought at $AVG, the best ask was $ASK" >&2; exit 1; }
echo "ok   within 1% of the best ask"
# The taker fee of a buy is paid in the coin bought.
has_coin() {
  local held
  held=$(balance ASTRA)
  [[ $(jq -n "${held% *} > $BOUGHT * 0.99 and ${held% *} <= $BOUGHT and ${held#* } == 0") == true ]]
}
eventually 40 "the ASTRA, less the fee, is in the account" has_coin
HELD=$(balance ASTRA | cut -d' ' -f1)
WHOLE=$(jq -n "$HELD | floor")
order "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$WHOLE\"}" sell
expect 202 - "a market sell of the $WHOLE whole lots"
SELL_ID=$(jq -r .order_id <<<"$BODY")
eventually 80 "the sell filled" filled "$SELL_ID"
dust_left() { [[ $(jq -n "$(balance ASTRA | cut -d' ' -f1) < 1") == true ]]; }
eventually 40 "less than a lot of ASTRA left" dust_left
USDT=$(balance USDT | cut -d' ' -f1)
[[ $(jq -n "$USDT > 9990 and $USDT < 10000") == true ]] || { echo "FAIL $USDT USDT after the round trip" >&2; exit 1; }
echo "ok   back to $USDT USDT (the spread and the fees)"
call GET "/v1/account/ledger?asset=ASTRA" "" "${AUTH[@]}"
check '[.items[].entry_type] | (map(select(. == "TRADE_SETTLE")) | length) >= 2' "both trades settled in the ledger"

echo "== a limit order below the book rests"
LOW=$(jq -n "($BID | tonumber) * 0.95 * 10000 | floor / 10000")
order "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"10\"}" rest
expect 202 - "a limit buy of 10 at $LOW"
REST_ID=$(jq -r .order_id <<<"$BODY")
resting() { call GET "/v1/orders/$REST_ID" "" "${AUTH[@]}" && [[ $(jq -r .status <<<"$BODY") == OPEN ]]; }
eventually 40 "it rests" resting
call DELETE "/v1/orders/$REST_ID" "" "${AUTH[@]}"
[[ $STATUS == 202 || $STATUS == 200 ]] || { echo "FAIL cancel: $STATUS $BODY" >&2; exit 1; }
canceled() { call GET "/v1/orders/$REST_ID" "" "${AUTH[@]}" && [[ $(jq -r .status <<<"$BODY") == CANCELED ]]; }
eventually 40 "canceled" canceled
released() { [[ $(balance USDT | cut -d' ' -f2) == 0 ]]; }
eventually 40 "its funds released" released
echo "all platform coin checks passed"
