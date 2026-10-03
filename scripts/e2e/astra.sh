#!/usr/bin/env bash
# The platform coin's simulated market end to end (ASTRA design §4, batch
# A2; docs/runbook/market-sim.md): with market-sim's bots on, ASTRA-USDT
# has a book of at least 8 levels a side within 0.3% of each other, public
# trades and 1m candles of its own (no reference market); a new user buys
# ASTRA at the market from the bots and sells it back, settled in the
# ledger like any trade; a limit order below the book rests and is
# canceled. With sim.events on, a change without a signature, an approver
# named with exchangectl's key (only the admin console's service names
# one) and a jump of 35% by one operator are refused; one of 2% moves the
# target and a target event brings it back (A3), and a target 12% away,
# beyond the price band, is reached by the quotes walking the band. With
# the bots on ASTRA-USDT-PERP, the user opens a long against them and
# closes it, and an operator's target 4% down liquidates a 50x long (A4).
# With market.flat_minutes on, both have a candles_1m row in ClickHouse
# for each of the ten minutes before the last two (flat when no trade).
# The moves take about 35% of one operator's 50% an hour: with events in
# the hour before, they are skipped. Skipped while ASTRA-USDT is not
# trading or the bots are off (scripts/ops/astra.sh seed, open, on,
# events-on, perp-open, perp-on).
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

# simpost PATH JSON posts JSON to market-sim's management API, signed by
# exchangectl in its container with the operators' key (SIM_API_SECRET):
# SIM_STATUS is the HTTP status, SIM_BODY the answer.
simpost() {
  local out
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST $1 $(printf %q "$2") 2>&1" || true)
  SIM_STATUS=$(grep -oE '^HTTP [0-9]+' <<<"$out" | tail -1 | cut -d' ' -f2)
  SIM_BODY=$(sed -n '/^{/,/^}/p' <<<"$out")
}
simget() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- 'http://127.0.0.1:8098$1'"; }

echo "== an operator's price event (sim.events)"
EVENTS_ON=$(pg "SELECT enabled FROM config.flags WHERE key = 'sim.events'")
MOVES=f # whether this run moves the price
if [[ $EVENTS_ON != t ]]; then
  echo "skip: the operators' price events are off (scripts/ops/astra.sh events-on)"
else
  FROM=$(simget /internal/sim | jq -r .target_price)
  # Only a caller holding one of market-sim's keys may change anything,
  # and only the admin console's service, which signs both operators in,
  # may name an approver (ASTRA design §6.2).
  unsigned=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -S -qO- --header 'Content-Type: application/json' --post-data '{\"type\":\"JUMP\",\"size\":0.01,\"actor\":\"x\",\"reason\":\"unsigned\"}' http://127.0.0.1:8098/internal/sim/events 2>&1" || true)
  grep -q 'HTTP/1.1 401' <<<"$unsigned" || { echo "FAIL an unsigned change: $unsigned" >&2; exit 1; }
  echo "ok   an unsigned change is refused (401)"
  simpost /internal/sim/events '{"type":"JUMP","size":0.01,"actor":"e2e-ops","approved_by":"e2e-ops-2","reason":"e2e: an approver from the operators key"}'
  [[ $SIM_STATUS == 403 && $(jq -r .code <<<"$SIM_BODY") == SIM_APPROVAL_NEEDS_ADMIN ]] ||
    { echo "FAIL an approver named with the operators' key: HTTP $SIM_STATUS $SIM_BODY" >&2; exit 1; }
  echo "ok   only the admin console's service names an approver (403 SIM_APPROVAL_NEEDS_ADMIN)"
  simpost /internal/sim/events '{"type":"JUMP","size":0.35,"actor":"e2e-ops","reason":"e2e: beyond one operator"}'
  [[ $SIM_STATUS == 403 && $(jq -r .code <<<"$SIM_BODY") == SIM_EVENT_NEEDS_APPROVAL ]] ||
    { echo "FAIL a jump of 35% alone: HTTP $SIM_STATUS $SIM_BODY" >&2; exit 1; }
  echo "ok   a jump of 35% needs a second operator (403 SIM_EVENT_NEEDS_APPROVAL)"
  # The moves below (2% and back, 12% and back, 4% and back) take about
  # 35% of the 50% one operator may move the price in any hour, counted
  # with every event and settings change within an hour of now.
  RECENT=$(pg "SELECT (SELECT count(*) FROM marketsim.events WHERE status <> 'CANCELED' AND type IN ('JUMP', 'TARGET', 'TREND', 'VOLATILITY') AND starts_at BETWEEN now() - interval '1 hour' AND now() + interval '1 hour') + (SELECT count(*) FROM marketsim.param_changes WHERE at > now() - interval '1 hour' AND (move <> 0 OR volume <> 0))")
  if (( RECENT > 0 )); then
    echo "skip: the price moves ($RECENT events or settings changes within the hour count toward one operator's 50%; they run again an hour after them)"
  else
    MOVES=t
  fi
fi
# solo JSON WHAT: one operator's event, 201.
solo() {
  simpost /internal/sim/events "$1"
  [[ $SIM_STATUS == 201 ]] || { echo "FAIL $2: HTTP $SIM_STATUS $SIM_BODY" >&2; exit 1; }
}
if [[ $MOVES == t ]]; then
  solo '{"type":"JUMP","size":0.02,"duration_seconds":10,"actor":"e2e-ops","reason":"e2e: a small jump"}' "a jump of 2%"
  JUMP=$(jq -r .id <<<"$SIM_BODY")
  jumped() {
    local st
    st=$(simget "/internal/sim/events?all=1&limit=10")
    [[ $(jq -r --arg id "$JUMP" '.items[] | select(.id == $id) | .status' <<<"$st") == DONE ]]
  }
  eventually 60 "the jump ran its 10 seconds" jumped
  TO=$(simget /internal/sim | jq -r .target_price)
  [[ $(jq -n "$TO > $FROM * 1.01") == true ]] || { echo "FAIL the target went from $FROM to $TO" >&2; exit 1; }
  echo "ok   the target went from $FROM to $TO"
  solo "{\"type\":\"TARGET\",\"price\":\"$FROM\",\"duration_seconds\":10,\"actor\":\"e2e-ops\",\"reason\":\"e2e: back\"}" "back to $FROM"
  echo "ok   a target back to $FROM"
  audited() { [[ $(pg "SELECT count(*) FROM marketsim.events WHERE created_by = 'e2e-ops' AND approved_by = '' AND created_at > now() - interval '5 minutes'") -ge 2 ]]; }
  eventually 20 "both events recorded" audited

  # The price band (10% around the last trade) must not lock the market
  # (ASTRA design §4): a target 12% away, at once; without anyone's help
  # the quotes walk the band there within three minutes, and back.
  echo "== a target beyond the price band"
  # last_at PRICE: the simulation's last trade, and its book of 8 levels a side.
  last_at() {
    local st
    st=$(simget /internal/sim)
    [[ $(jq --argjson want "$1" '.last_price != null and ((.last_price | tonumber) / $want - 1 | fabs) <= 0.03' <<<"$st") == true ]] && booked
  }
  UP=$(jq -rn --argjson p "$FROM" '$p * 1.12 * 10000 | floor / 10000 | tostring')
  FIRED=$(simget /internal/sim | jq '.watchdog.fired')
  solo "{\"type\":\"TARGET\",\"price\":\"$UP\",\"actor\":\"e2e-ops\",\"reason\":\"e2e: beyond the band\"}" "a target of $UP"
  eventually 180 "the market walked the band 12% up to $UP, with 8 levels a side" last_at "$UP"
  solo "{\"type\":\"TARGET\",\"price\":\"$FROM\",\"actor\":\"e2e-ops\",\"reason\":\"e2e: back inside the band\"}" "back to $FROM"
  eventually 180 "and back down to $FROM" last_at "$FROM"
  [[ $(simget /internal/sim | jq '.watchdog.fired') == "$FIRED" ]] ||
    { echo "FAIL the watchdog had to unlock the market" >&2; exit 1; }
  echo "ok   the quotes walked the band; the watchdog stayed out of it"
fi

echo "== the perpetual (sim.perp)"
call GET /v1/market/contracts/ASTRA-USDT-PERP ""
if [[ $STATUS != 200 || $(jq -r .status <<<"$BODY") != TRADING || $(jq -r .perp_running <<<"$(simget /internal/sim)") != true ]]; then
  echo "skip: ASTRA-USDT-PERP is not trading or the bots are not on it (scripts/ops/astra.sh perp-open, perp-on)"
else
  call POST /v1/account/transfers '{"asset":"USDT","amount":"100","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
    "${AUTH[@]}" -H "Idempotency-Key: astra-perp-$RUN"
  expect 201 - "100 USDT to FUTURES"
  call POST /v1/derivatives/orders '{"symbol":"ASTRA-USDT-PERP","side":"BUY","type":"MARKET","quantity":"20"}' "${AUTH[@]}"
  expect 202 - "a market buy of 20 ASTRA-USDT-PERP"
  long() { call GET /v1/derivatives/positions "" "${AUTH[@]}" && [[ $(jq -r '[.positions[] | select(.symbol == "ASTRA-USDT-PERP")][0].quantity // "0"' <<<"$BODY") == 20 ]]; }
  eventually 80 "long 20 against the bots" long
  call POST /v1/derivatives/orders '{"symbol":"ASTRA-USDT-PERP","side":"SELL","type":"MARKET","quantity":"20","reduce_only":true}' "${AUTH[@]}"
  expect 202 - "closed at the market"
  flat() { call GET /v1/derivatives/positions "" "${AUTH[@]}" && [[ $(jq -r '[.positions[] | select(.symbol == "ASTRA-USDT-PERP")] | length' <<<"$BODY") == 0 ]]; }
  eventually 80 "flat again" flat

  # An operator's event liquidates a leveraged long (design §7, A4): 50x
  # isolated, 1500 ASTRA (about 30 USDT of margin, liquidated about 1.6%
  # down); a target 4% down moves the spot pair, the index (its minute's
  # TWAP) and the mark follow, the position is taken over and closed by a
  # liquidation order against the bots. Then the target goes back.
  if [[ $MOVES == t ]]; then
    echo "== an event liquidates a leveraged long"
    call PUT /v1/derivatives/settings/ASTRA-USDT-PERP '{"margin_mode":"ISOLATED","leverage":50}' "${AUTH[@]}"
    expect 200 - "isolated, 50x"
    call POST /v1/derivatives/orders '{"symbol":"ASTRA-USDT-PERP","side":"BUY","type":"MARKET","quantity":"1500"}' "${AUTH[@]}"
    expect 202 - "a market buy of 1500 ASTRA-USDT-PERP"
    levered() { call GET /v1/derivatives/positions "" "${AUTH[@]}" && [[ $(jq -r '[.positions[] | select(.symbol == "ASTRA-USDT-PERP")][0].quantity // "0"' <<<"$BODY") == 1500 ]]; }
    eventually 80 "long 1500 at 50x" levered
    LIQ=$(jq -r '[.positions[] | select(.symbol == "ASTRA-USDT-PERP")][0].liquidation_price' <<<"$BODY")
    BACK=$(simget /internal/sim | jq -r .target_price)
    DOWN=$(jq -rn --argjson p "$BACK" '$p * 0.96 * 10000 | floor / 10000 | tostring')
    solo "{\"type\":\"TARGET\",\"price\":\"$DOWN\",\"actor\":\"e2e-ops\",\"reason\":\"e2e: liquidate a long\"}" "a target of $DOWN"
    echo "     the target goes from $BACK to $DOWN; the long's liquidation price is $LIQ"
    liquidated() {
      flat && call GET "/v1/derivatives/fills?symbol=ASTRA-USDT-PERP&limit=20" "" "${AUTH[@]}" &&
        [[ $(jq '[.items[] | select(.liquidation)] | length' <<<"$BODY") -gt 0 ]]
    }
    eventually 240 "the long is liquidated against the bots" liquidated
    check '[.items[] | select(.liquidation)] | all(.side == "SELL" and (.realized_pnl | tonumber) < 0)' "a liquidation sell at a loss"
    solo "{\"type\":\"TARGET\",\"price\":\"$BACK\",\"actor\":\"e2e-ops\",\"reason\":\"e2e: back after the liquidation\"}" "back to $BACK"
    echo "ok   the target goes back to $BACK"
  fi
fi
echo "== flat minutes in ClickHouse (market.flat_minutes)"
# continuous SYMBOL: a candles_1m row for each of the ten minutes before
# the last two of its latest (whose flats may still be on their way).
continuous() {
  [[ $(ch "WITH (SELECT max(open_time) FROM candles_1m WHERE symbol = '$1') AS last
    SELECT count(DISTINCT open_time) FROM candles_1m
    WHERE symbol = '$1' AND open_time BETWEEN last - INTERVAL 11 MINUTE AND last - INTERVAL 2 MINUTE") == 10 ]]
}
# The minutes before the switch was turned on may have gaps (forward only):
# twelve minutes after, the ten checked are all after it (review AV).
for S in ASTRA-USDT ASTRA-USDT-PERP; do
  case $(pg "SELECT CASE WHEN NOT (enabled AND coalesce(rules->'symbols'->'allow' ? '$S', true)) THEN 'off'
    WHEN updated_at > now() - interval '12 minutes' THEN 'recent' ELSE 'on' END FROM config.flags WHERE key = 'market.flat_minutes'") in
  on) eventually 120 "$S: a 1m candle in ClickHouse for each of ten minutes, flat when nothing traded" continuous "$S" ;;
  recent) echo "skip: market.flat_minutes changed less than 12 minutes ago ($S)" ;;
  *) echo "skip: market.flat_minutes is off for $S" ;;
  esac
done
echo "all platform coin checks passed"
