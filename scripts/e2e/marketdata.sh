#!/usr/bin/env bash
# Market data end to end (implementation plan §6.3 task 5): the public
# REST endpoints answer and refuse bad input; the reference market's data
# (ADR-0010); then lib/md-check.mjs follows ETH-BTC's WebSocket channels
# (depth, trades, ticker, candles without sign-in: Binance's ETHBTC) while
# a user buys at the market against HOUSE (orders and fills pushed to it),
# and checks REST afterwards. Needs ETH-BTC in TRADING with HOUSE liquidity.
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
call GET /v1/market/NOPE-USDT/ticker ""
expect 404 COMMON_NOT_FOUND "a pair that does not exist"
# The test environment shows the reference source's K-lines for BTC-USDT
# (market.reference_kline): a day of hourly candles that traded.
call GET "/v1/market/BTC-USDT/candles?interval=1h&limit=24" ""
expect 200 - "BTC-USDT hourly candles"
check '(.candles | length) == 24 and all(.candles[]; (.volume | tonumber) > 0)' "reference K-lines: 24 hours, each with volume"
call GET "/v1/market/BTC-USDT/candles?interval=2d" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown interval"
call GET "/v1/market/BTC-USDT/candles?interval=1m&from=yesterday" ""
expect 400 COMMON_INVALID_ARGUMENT "a time that is not RFC 3339"
call GET "/v1/market/BTC-USDT/depth?limit=5" ""
expect 200 - "depth"
check '(.bids | length) <= 5 and (.asks | length) <= 5' "at most the asked levels"

echo "== reference market data (ADR-0010)"
call GET /v1/market/pairs ""
expect 200 - "pairs"
check '(.pairs[] | select(.symbol == "BTC-USDT")) | .reference_symbol == "BTCUSDT" and .reference_multiplier == "1" and .base_name == "Bitcoin" and .rank == 1 and .price_decimals == 2 and .qty_decimals == 4' "BTC-USDT follows BTCUSDT, with its listing data"
check '(.pairs[] | select(.symbol == "ETH-BTC")) | .reference_symbol == "ETHBTC"' "ETH-BTC follows ETHBTC (every pair trades against HOUSE)"
# market.reference_ticker: Binance's 24-hour ticker, updated every second.
call GET /v1/market/BTC-USDT/ticker ""
expect 200 - "BTC-USDT ticker"
check '.rank == 1 and .trade_count > 10000 and ((now - (.updated_at | sub("\\.[0-9]+"; "") | fromdateiso8601)) < 60)' "the reference market's: a busy day, fresh"
call GET /v1/market/summary ""
expect 200 - "market summary"
check '(.gainers | length) >= 1 and (.gainers | length) <= 5 and (.turnover | map(.symbol) | index("BTC-USDT")) != null' "movers and turnover of the trading USDT pairs"
call GET "/v1/market/BTC-USDT/candles?interval=1h&limit=5" ""
FIRST_OPEN=$(jq -r '.candles[0].open_time' <<<"$BODY")
call GET "/v1/market/BTC-USDT/candles?interval=1h&limit=5&to=$FIRST_OPEN" ""
expect 200 - "the page before"
check "(.candles | length) == 5 and .candles[4].open_time < \"$FIRST_OPEN\"" "five older candles, none repeated"
node "$(dirname "$0")/lib/tickers-check.mjs" "$BASE"

echo "== a trader"
register "e2e-md-buyer-$RUN@example.com" "e2e-md-buyer-$RUN" "e2e md $RUN"
TOKEN=$(jq -r .access_token <<<"$BODY")
BUYER=(-H "Authorization: Bearer $TOKEN")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${BUYER[@]}"'
funded() {
  call GET /v1/account/balances "" "${BUYER[@]}"
  [[ $(jq -r '[.balances[] | select(.account_type == "SPOT" and .asset == "BTC")][0].available' <<<"$BODY") != "null" ]]
}
eventually 40 "welcome funds arrived" funded

echo "== WebSocket and REST around a trade against HOUSE"
node "$(dirname "$0")/lib/md-check.mjs" "$BASE" "$TOKEN"
