#!/usr/bin/env bash
# Market data end to end (implementation plan §6.3 task 5): the public
# REST endpoints answer and refuse bad input; then two users trade 0.01 ETH
# on ETH-BTC at 0.0402 while lib/md-check.mjs follows the WebSocket
# channels (depth, trades, ticker, candles without sign-in; the buyer's
# orders and fills) and checks REST trades, ticker, candles and depth
# afterwards. ETH-BTC has no market maker; needs it in TRADING and no other
# resting order at 0.0402.
#
#   scripts/e2e/marketdata.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

echo "== REST"
call GET /v1/market/tickers ""
expect 200 - "tickers"
check '[.tickers[].symbol] | index("BTC-USDT") != null' "every listed pair has a ticker"
call GET /v1/market/btc-usdt/ticker ""
expect 200 - "a ticker (symbols are case-insensitive)"
check '.symbol == "BTC-USDT" and (.volume | type) == "string"' "amounts are strings"
call GET /v1/market/DOGE-USDT/ticker ""
expect 404 COMMON_NOT_FOUND "a pair that does not exist"
call GET "/v1/market/BTC-USDT/candles?interval=2d" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown interval"
call GET "/v1/market/BTC-USDT/candles?interval=1m&from=yesterday" ""
expect 400 COMMON_INVALID_ARGUMENT "a time that is not RFC 3339"
call GET "/v1/market/BTC-USDT/depth?limit=5" ""
expect 200 - "depth"
check '(.bids | length) <= 5 and (.asks | length) <= 5' "at most the asked levels"

signup() { # signup NAME: registers a user, sets NAME_TOKEN
  register "e2e-md-$1-$RUN@example.com" "e2e-md-$1-$RUN" "e2e md $RUN"
  eval "${1}_TOKEN=$(jq -r .access_token <<<"$BODY")"
}
echo "== two traders"
signup seller
signup buyer
# shellcheck disable=SC2154 # set by signup
SELLER=(-H "Authorization: Bearer $seller_TOKEN")
# shellcheck disable=SC2154
BUYER=(-H "Authorization: Bearer $buyer_TOKEN")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${SELLER[@]}"; call DELETE /v1/orders "" "${BUYER[@]}"'
funded() { # funded ASSET AUTH...
  local asset=$1
  shift
  call GET /v1/account/balances "" "$@"
  [[ $(jq -r --arg a "$asset" '[.balances[] | select(.account_type == "SPOT" and .asset == $a)][0].available' <<<"$BODY") != "null" ]]
}
eventually 40 "welcome funds arrived for the seller" funded ETH "${SELLER[@]}"
eventually 40 "welcome funds arrived for the buyer" funded BTC "${BUYER[@]}"

echo "== WebSocket and REST around one trade"
node "$(dirname "$0")/lib/md-check.mjs" "$BASE" "$seller_TOKEN" "$buyer_TOKEN"
