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
# target and another brings it back (A3), and a jump of 12%, beyond the
# price band, is reached by the quotes walking the band. Threshold
# targets and spikes (A6): a target too fast for its window is refused
# with the shortest one; +3% in twelve minutes is HIT, some of its 1m
# candles against it (the share shown; a quarter or more is the seeded
# unit tests' to prove), no jump nor a spike in its closing window
# meanwhile; -2% in three minutes is HIT; a spike of -4% reaches its tip
# and comes back to the plan within half a percent. With the bots on
# ASTRA-USDT-PERP, the user opens a long against them and closes it, and
# an operator's jump 4% down liquidates a 50x long (A4). With
# market.flat_minutes on, both have a candles_1m row in ClickHouse for
# each of the ten minutes before the last two (flat when no trade). The
# moves take about 47% of one operator's 50% an hour: with events in the
# hour before, they are skipped. Skipped while ASTRA-USDT is not
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
REFRESH=$(jq -r .refresh_token <<<"$BODY")
# fresh_auth takes a new access token: the price events outlast one.
fresh_auth() {
  call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
  expect 200 - "a fresh access token"
  REFRESH=$(jq -r .refresh_token <<<"$BODY")
  AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
}
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
  # The moves below (2% and back, 12% and back, targets of 3% and 2%, a
  # spike of 4%, 4% and back) take about 47% of the 50% one operator may
  # move the price in any hour, counted with every event and settings
  # change within an hour of now.
  RECENT=$(pg "SELECT (SELECT count(*) FROM marketsim.events WHERE status <> 'CANCELED' AND type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY') AND starts_at BETWEEN now() - interval '1 hour' AND now() + interval '1 hour') + (SELECT count(*) FROM marketsim.param_changes WHERE at > now() - interval '1 hour' AND (move <> 0 OR volume <> 0))")
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
# refused JSON STATUS CODE WHAT: one operator's event, refused so.
refused() {
  simpost /internal/sim/events "$1"
  [[ $SIM_STATUS == "$2" && $(jq -r .code <<<"$SIM_BODY") == "$3" ]] || { echo "FAIL $4: HTTP $SIM_STATUS $SIM_BODY" >&2; exit 1; }
  echo "ok   $4 ($2 $3)"
}
# target_now: the simulation's target price; back_by SHARE PRICE: the
# jump (a share, 4 decimals) that takes it to PRICE.
target_now() { simget /internal/sim | jq -r .target_price; }
back_by() { jq -rn --argjson to "$1" --argjson now "$(target_now)" '($to / $now - 1) * 10000 | round / 10000'; }
# in_seconds N: the time N seconds from now (RFC 3339, UTC).
in_seconds() { jq -rn --argjson n "$1" 'now + $n | floor | todateiso8601'; }
# event_field ID FIELD: an event's field, as the latest events show it.
event_field() { simget "/internal/sim/events?all=1&limit=50" | jq -r --arg id "$1" --arg f "$2" '.items[] | select(.id == $id) | .[$f] // ""'; }
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
  solo "{\"type\":\"JUMP\",\"size\":$(back_by "$FROM"),\"duration_seconds\":10,\"actor\":\"e2e-ops\",\"reason\":\"e2e: back\"}" "back to $FROM"
  echo "ok   a jump back to $FROM"
  audited() { [[ $(pg "SELECT count(*) FROM marketsim.events WHERE created_by = 'e2e-ops' AND approved_by = '' AND created_at > now() - interval '5 minutes'") -ge 2 ]]; }
  eventually 20 "both events recorded" audited

  # The price band (10% around the last trade) must not lock the market
  # (ASTRA design §4): a jump of 12%, at once; without anyone's help the
  # quotes walk the band there within three minutes, and back.
  echo "== a jump beyond the price band"
  # last_at PRICE: the simulation's last trade, and its book of 8 levels a side.
  last_at() {
    local st
    st=$(simget /internal/sim)
    [[ $(jq --argjson want "$1" '.last_price != null and ((.last_price | tonumber) / $want - 1 | fabs) <= 0.03' <<<"$st") == true ]] && booked
  }
  UP=$(jq -rn --argjson p "$FROM" '$p * 1.12 * 10000 | floor / 10000 | tostring')
  FIRED=$(simget /internal/sim | jq '.watchdog.fired')
  solo "{\"type\":\"JUMP\",\"size\":$(back_by "$UP"),\"actor\":\"e2e-ops\",\"reason\":\"e2e: beyond the band\"}" "a jump to $UP"
  eventually 180 "the market walked the band 12% up to $UP, with 8 levels a side" last_at "$UP"
  solo "{\"type\":\"JUMP\",\"size\":$(back_by "$FROM"),\"actor\":\"e2e-ops\",\"reason\":\"e2e: back inside the band\"}" "back to $FROM"
  eventually 180 "and back down to $FROM" last_at "$FROM"
  [[ $(simget /internal/sim | jq '.watchdog.fired') == "$FIRED" ]] ||
    { echo "FAIL the watchdog had to unlock the market" >&2; exit 1; }
  echo "ok   the quotes walked the band; the watchdog stayed out of it"

  # Threshold targets and spikes (A6; design §3, §6.2, §8.8): the price
  # gets above or below a level by the end of a window, slowly and both
  # ways; a spike moves the printed price for seconds, the plan unchanged.
  echo "== threshold targets and a spike"
  P=$(target_now)
  refused "{\"type\":\"TARGET\",\"direction\":\"ABOVE\",\"price\":\"$(jq -rn --argjson p "$P" '$p * 1.2 * 10000 | ceil / 10000')\",\"duration_seconds\":120,\"actor\":\"e2e-ops\",\"reason\":\"e2e: too fast\"}" \
    400 SIM_TARGET_INFEASIBLE "+20% in two minutes"
  [[ $(jq -r .details.min_duration_seconds <<<"$SIM_BODY") -gt 120 ]] || { echo "FAIL the shortest window: $SIM_BODY" >&2; exit 1; }
  echo "ok   the shortest window given: $(jq -r .details.min_duration_seconds <<<"$SIM_BODY") s"
  LEVEL=$(jq -rn --argjson p "$P" '$p * 1.03 * 10000 | ceil / 10000')
  solo "{\"type\":\"TARGET\",\"direction\":\"ABOVE\",\"price\":\"$LEVEL\",\"duration_seconds\":720,\"actor\":\"e2e-ops\",\"reason\":\"e2e: +3% in twelve minutes\"}" "a target of $LEVEL"
  TID=$(jq -r .id <<<"$SIM_BODY")
  TSTART=$(jq -r .starts_at <<<"$SIM_BODY")
  TCLOSE=$(jq -r .closing_at <<<"$SIM_BODY")
  echo "ok   a target above $LEVEL in twelve minutes from $P (closing in at $TCLOSE)"
  refused '{"type":"JUMP","size":0.01,"actor":"e2e-ops","reason":"e2e: a jump over a target"}' 409 SIM_TARGET_RUNNING "a jump while it runs"
  refused "{\"type\":\"SPIKE\",\"size\":0.01,\"starts_at\":\"$(jq -rn --arg t "$TCLOSE" '$t | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601 + 20 | todateiso8601')\",\"actor\":\"e2e-ops\",\"reason\":\"e2e: a spike while it closes in\"}" \
    409 SIM_SPIKE_IN_CLOSING "a spike in its closing window"
  hit() { [[ $(event_field "$1" result) == HIT ]]; }
  eventually 1200 "HIT within its window" hit "$TID"
  CROSSED=$(event_field "$TID" crossed_at)
  # Both ways (§8.8): of its 1m candles from its first whole minute to
  # its crossing, some down. A quarter or more is proven by the seeded unit
  # tests (in 97% of seeds for this target): one run asserts one and shows
  # the share (coordinator 2026-10-04 06:55).
  call GET "/v1/market/$SYMBOL/candles?interval=1m&limit=20" ""
  expect 200 - "its 1m candles"
  WAYS=$(jq -r --arg from "$TSTART" --arg to "$CROSSED" '[.candles[] | select(.open_time > $from and .open_time < $to)]
    | "\(length) \(map(select((.close | tonumber) < (.open | tonumber))) | length)"' <<<"$BODY")
  read -r N DOWNS <<<"$WAYS"
  (( N >= 8 && DOWNS >= 1 )) || { echo "FAIL $DOWNS of $N candles against the target" >&2; exit 1; }
  echo "ok   crossed at $CROSSED: $DOWNS of its $N whole 1m candles went against it ($((100 * DOWNS / N))%)"
  P=$(target_now)
  LOW=$(jq -rn --argjson p "$P" '$p / 1.02 * 10000 | floor / 10000')
  solo "{\"type\":\"TARGET\",\"direction\":\"BELOW\",\"price\":\"$LOW\",\"duration_seconds\":180,\"actor\":\"e2e-ops\",\"reason\":\"e2e: -2% in three minutes\"}" "a target of $LOW"
  eventually 600 "below $LOW within three minutes: HIT" hit "$(jq -r .id <<<"$SIM_BODY")"
  AT=$(in_seconds 15)
  solo "{\"type\":\"SPIKE\",\"size\":-0.04,\"width_seconds\":20,\"starts_at\":\"$AT\",\"actor\":\"e2e-ops\",\"reason\":\"e2e: a spike\"}" "a spike of -4% at $AT"
  # tip: the last trade at least 2.5% under the plan; on_plan: within half
  # a percent of it.
  tip() { [[ $(simget /internal/sim | jq '(.last_price | tonumber) <= (.target_price | tonumber) * 0.975') == true ]]; }
  on_plan() { [[ $(simget /internal/sim | jq '((.last_price | tonumber) / (.target_price | tonumber) - 1 | fabs) <= 0.005') == true ]]; }
  sleep 13
  eventually 30 "the spike's tip" tip
  eventually 120 "back on the plan, within half a percent" on_plan
fi

echo "== the perpetual (sim.perp)"
fresh_auth
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
  # down); a jump 4% down moves the spot pair, the index (its minute's
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
    solo "{\"type\":\"JUMP\",\"size\":$(back_by "$DOWN"),\"actor\":\"e2e-ops\",\"reason\":\"e2e: liquidate a long\"}" "a jump to $DOWN"
    echo "     the target goes from $BACK to $DOWN; the long's liquidation price is $LIQ"
    liquidated() {
      flat && call GET "/v1/derivatives/fills?symbol=ASTRA-USDT-PERP&limit=20" "" "${AUTH[@]}" &&
        [[ $(jq '[.items[] | select(.liquidation)] | length' <<<"$BODY") -gt 0 ]]
    }
    eventually 240 "the long is liquidated against the bots" liquidated
    check '[.items[] | select(.liquidation)] | all(.side == "SELL" and (.realized_pnl | tonumber) < 0)' "a liquidation sell at a loss"
    solo "{\"type\":\"JUMP\",\"size\":$(back_by "$BACK"),\"actor\":\"e2e-ops\",\"reason\":\"e2e: back after the liquidation\"}" "back to $BACK"
    echo "ok   the target goes back to $BACK"
  fi
fi
echo "== the coin-margined perpetual ASTRA-USD-PERP (design 2026-10-06 §2.3, G2)"
fresh_auth
call GET /v1/market/contracts/ASTRA-USD-PERP ""
COIN_STATUS=$(jq -r '.status // ""' <<<"$BODY")
COIN_ON=$(simget /internal/sim | jq -r '[.perps[]? | select(.symbol == "ASTRA-USD-PERP")][0].running // false')
if [[ $COIN_STATUS != TRADING || $COIN_ON != true ]]; then
  echo "skip: ASTRA-USD-PERP is not trading or the bots are not on it (scripts/ops/astra.sh perp-open ASTRA-USD-PERP, perp-on ASTRA-USDT-PERP ASTRA-USD-PERP)"
else
  coin_book() {
    call GET "/v1/market/ASTRA-USD-PERP/depth?limit=5" "" && [[ $STATUS == 200 ]] && jq -e '(.bids | length) > 0 and (.asks | length) > 0' <<<"$BODY" >/dev/null
  }
  eventually 60 "the bots quote both sides of ASTRA-USD-PERP" coin_book
  check '[.bids[], .asks[]] | all(.[1] | tonumber | . == floor)' "in whole contracts"
  check '(.asks[0][0] | tonumber) > (.bids[0][0] | tonumber)' "an ask above the bid"
  margined() {
    simget /internal/sim | jq -e '[.bots[] | select(.role == "MAKER") | (.futures.ASTRA // "0" | tonumber)] | all(. > 0)' >/dev/null
  }
  eventually 60 "the makers' margin is in ASTRA" margined
  # A user buys ASTRA, moves it to FUTURES and trades three contracts
  # (30 USD) against the bots, margined in ASTRA.
  order "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quote_amount\":\"60\"}" coin-buy
  expect 202 - "a market buy of 60 USDT of ASTRA"
  eventually 80 "the buy filled against the bots" filled "$(jq -r .order_id <<<"$BODY")"
  COIN_MARGIN=$(jq -rn "$(balance ASTRA | cut -d' ' -f1) | floor")
  call POST /v1/account/transfers "{\"asset\":\"ASTRA\",\"amount\":\"$COIN_MARGIN\",\"from_account_type\":\"SPOT\",\"to_account_type\":\"FUTURES\"}" \
    "${AUTH[@]}" -H "Idempotency-Key: astra-coin-$RUN"
  expect 201 - "$COIN_MARGIN ASTRA to FUTURES"
  call POST /v1/derivatives/orders '{"symbol":"ASTRA-USD-PERP","side":"BUY","type":"MARKET","quantity":"3"}' "${AUTH[@]}"
  expect 202 - "a market buy of 3 ASTRA-USD-PERP"
  coin_long() {
    call GET /v1/derivatives/positions "" "${AUTH[@]}" &&
      [[ $(jq -r '[.positions[] | select(.symbol == "ASTRA-USD-PERP")][0].quantity // "0"' <<<"$BODY") == 3 ]]
  }
  eventually 80 "long 3 contracts against the bots" coin_long
  check '[.positions[] | select(.symbol == "ASTRA-USD-PERP")][0].settle_asset == "ASTRA"' "settled in ASTRA"
  call POST /v1/derivatives/orders '{"symbol":"ASTRA-USD-PERP","side":"SELL","type":"MARKET","quantity":"3","reduce_only":true}' "${AUTH[@]}"
  expect 202 - "closed at the market"
  coin_flat() {
    call GET /v1/derivatives/positions "" "${AUTH[@]}" &&
      [[ $(jq -r '[.positions[] | select(.symbol == "ASTRA-USD-PERP")] | length' <<<"$BODY") == 0 ]]
  }
  eventually 80 "flat again" coin_flat
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
