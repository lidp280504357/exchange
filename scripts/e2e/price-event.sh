#!/usr/bin/env bash
# Price events on a followed pair end to end (docs/设计-通用价格控制-2026-10-07.md
# §5 J2, J0 contract; docs/runbook/market-sim.md): an operator's event on
# BTC-USDT ramps up in 15 seconds, holds 0 and comes back in 5. While it
# runs, the platform's reference price, ticker and book are the reference
# market's times the event's factor, HOUSE quotes a quarter of each level
# (market_house_quoted_share against the reference market's book, before
# and during), BTC-USDT-PERP's index and mark follow (the mark computed,
# PLATFORM) and so does a margin account holding BTC; afterwards the event
# is DONE, the ticker within 0.05% of the reference market, the spike in
# the 1m candles, the perpetual's mark back on its own source once the
# reference market's mark has streamed 5 seconds (the C40 rule). The
# platform's market data after the event is read inside market-data's
# container, as during it. A second event "without risk" moves the spot
# pair only: the perpetual's mark and the margin account stay on the
# reference market's price. The target is +16% unless HOUSE's worst loss
# on it (its rooms on the pair, and on the perpetuals with risk) is beyond
# OVERLAY_MAX_LOSS_USDT: then the largest share within it (the refusal
# names the estimate). Turns market.overlay on for the run and back as it
# was; holds the ops lock. Skipped while BTC-USDT is not trading or not
# followed, or the pair's hourly budget is spent (one operator: 50%).
#
#   scripts/e2e/price-event.sh
set -euo pipefail
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "e2e $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

SYMBOL=BTC-USDT
PERP=BTC-USDT-PERP
call GET "/v1/market/pairs/$SYMBOL" ""
expect 200 - "$SYMBOL"
if [[ $(jq -r .status <<<"$BODY") != TRADING ]]; then
  echo "skip: $SYMBOL is not trading"
  exit 0
fi

# market-data's view, from inside its container (internal endpoints).
md() { remote "sudo docker compose $COMPOSE_FILES exec -T market-data-service wget -qO- 'http://127.0.0.1:8090$1'"; }
REF=$(md "/internal/market/$SYMBOL/reference")
if [[ $(jq -r '.followed and .fresh' <<<"$REF") != true ]]; then
  echo "skip: no fresh reference price follows $SYMBOL: $REF"
  exit 0
fi
echo "ok   $SYMBOL follows the reference market at $(jq -r .source_price <<<"$REF")"

# simpost PATH JSON posts to market-sim's management API, signed by
# exchangectl in its container (the operators' key): SIM_STATUS, SIM_BODY.
simpost() {
  local out
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST $1 $(printf %q "$2") 2>&1" || true)
  SIM_STATUS=$(grep -oE '^HTTP [0-9]+' <<<"$out" | tail -1 | cut -d' ' -f2)
  SIM_BODY=$(sed -n '/^{/,/^}/p' <<<"$out")
}
simget() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- 'http://127.0.0.1:8098$1'"; }
if ! simget /internal/sim >/dev/null 2>&1; then
  echo "skip: market-sim does not answer (a standby, or down)"
  exit 0
fi

# market.overlay on for the run, as it was afterwards. Services see a
# change within 5 seconds (flags.RefreshInterval).
if ! STATE=$(exchangectl flags show market.overlay 2>&1); then
  [[ $STATE == *"is not set"* ]] || { echo "FAIL could not read market.overlay: $STATE" >&2; exit 1; }
  STATE='{"enabled":false}'
fi
if [[ $(jq -r .enabled <<<"$STATE") != true ]]; then
  exchangectl flags set market.overlay --on --reason "e2e price-event.sh: price events on followed pairs for the run" >/dev/null
  at_exit 'exchangectl flags set market.overlay --off --reason "e2e price-event.sh: back as it was" >/dev/null || echo "WARN market.overlay left on" >&2'
  echo "note: market.overlay on until the script ends"
  sleep 6
fi

EMAIL="e2e-price-event-$RUN@example.com"
echo "== register $EMAIL, BTC on a margin account"
register "$EMAIL" "e2e-price-event-$RUN" "e2e price event $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
USER_ID=$(jq -r .user_id <<<"$BODY")
if ! MSTATE=$(exchangectl flags show margin.enabled 2>&1); then
  [[ $MSTATE == *"is not set"* ]] || { echo "FAIL could not read margin.enabled: $MSTATE" >&2; exit 1; }
  MSTATE='{"enabled":false,"rules":{}}'
fi
if [[ $(jq -r '.enabled and (.rules == {} or .rules == null)' <<<"$MSTATE") != true ]]; then
  MUSERS=$(jq -r '(.rules.users.allow // []) | join(",")' <<<"$MSTATE")
  at_exit "exchangectl flags set margin.enabled $([[ $(jq -r .enabled <<<"$MSTATE") == true ]] && echo --on || echo --off) --allow-users '$MUSERS' --reason 'e2e price-event.sh: back as it was' >/dev/null || echo 'WARN margin.enabled not put back' >&2"
  exchangectl flags set margin.enabled --on --allow-users "${MUSERS:+$MUSERS,}$USER_ID" --reason "e2e price-event.sh: on for its user $USER_ID only" >/dev/null
  sleep 6
fi
spot() {
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '[.balances[] | select(.asset == $a and .account_type == "SPOT")][0].available // "0"' <<<"$BODY"
}
funded() { [[ $(spot USDT) != 0 ]]; }
eventually 40 "welcome funds arrived" funded
call POST /v1/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quote_amount\":\"300\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-pe-$RUN-buy"
expect 202 - "a market buy of 300 USDT of BTC"
has_btc() { [[ $(jq -n "$(spot BTC) > 0") == true ]]; }
eventually 40 "the BTC arrived" has_btc
QTY=$(spot BTC)
# The account counts BTC at its haircut as collateral (total_asset).
call GET /v1/margin/assets ""
expect 200 - "the margin assets"
HAIRCUT=$(jq -r '.items[] | select(.asset == "BTC") | .haircut' <<<"$BODY")
[[ $HAIRCUT =~ ^[0-9.]+$ ]] || { echo "FAIL BTC is not margin collateral: $BODY" >&2; exit 1; }
call POST /v1/margin/transfer "{\"direction\":\"IN\",\"account\":\"MARGIN_CROSS\",\"asset\":\"BTC\",\"amount\":\"$QTY\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-pe-$RUN-in"
expect 200 - "$QTY BTC into the cross margin account"

MARK_BEFORE=$(md "/v1/market/$PERP/mark-price" | jq -r .source)
echo "     $PERP's mark from $MARK_BEFORE"
# HOUSE's quantity on BTC-USDT's first levels over the reference market's,
# before any event: the share an event's quarter is compared with.
SHARE_BEFORE=$(metric market-maker 9091 market_house_quoted_share "symbol=\"$SYMBOL\"")
[[ $(jq -n "${SHARE_BEFORE:-0} >= 0.5") == true ]] || { echo "FAIL HOUSE quotes ${SHARE_BEFORE:-nothing} of $SYMBOL's book before the event" >&2; exit 1; }
echo "     HOUSE quotes $SHARE_BEFORE of the reference book's first levels"

# The sampler runs in market-data's container: every second the reference
# price (again after the rest: a sample whose factor moved meanwhile is
# not compared), the ticker, the book's top, the perpetual's mark, the
# user's margin accounts (margin-service, as the gateway would ask) and
# HOUSE's reduced quoting and share of the book, one JSON line.
cat >"$WORK/sampler.sh" <<EOF
for i in \$(seq 26); do
  m=\$(wget -qO- http://market-maker:9091/metrics 2>/dev/null)
  q=\$(echo "\$m" | grep '^market_house_overlay_quoting{symbol="$SYMBOL"}' | cut -d' ' -f2)
  s=\$(echo "\$m" | grep '^market_house_quoted_share{symbol="$SYMBOL"}' | cut -d' ' -f2)
  printf '{"t":%s,"ref":%s,"ticker":%s,"depth":%s,"mark":%s,"margin":%s,"ref2":%s,"quoting":"%s","share":"%s"}\n' "\$(date +%s)" \
    "\$(wget -qO- http://127.0.0.1:8090/internal/market/$SYMBOL/reference || echo null)" \
    "\$(wget -qO- http://127.0.0.1:8090/v1/market/$SYMBOL/ticker || echo null)" \
    "\$(wget -qO- 'http://127.0.0.1:8090/v1/market/$SYMBOL/depth?limit=1' || echo null)" \
    "\$(wget -qO- http://127.0.0.1:8090/v1/market/$PERP/mark-price || echo null)" \
    "\$(wget -qO- --header 'X-User-Id: $USER_ID' http://margin-service:8099/v1/margin/accounts || echo null)" \
    "\$(wget -qO- http://127.0.0.1:8090/internal/market/$SYMBOL/reference || echo null)" "\$q" "\$s"
  sleep 1
done
EOF
sample() { remote "sudo docker compose $COMPOSE_FILES exec -T market-data-service sh -s" "$(cat "$WORK/sampler.sh")" | jq -c 'select(.ref != null and .ref2 != null)' >"$1"; }
# steady: the samples during the event whose factor held while they were
# taken.
steady='map(select((.ref.overlay_factor | tonumber) > 1.0001 and .ref.overlay_factor == .ref2.overlay_factor))'

EVENT=""
# shellcheck disable=SC2016 # expanded when the script ends
at_exit '[[ -z $EVENT ]] || simpost "/internal/sim/events/$EVENT/end" "{\"actor\":\"e2e-ops\",\"reason\":\"e2e: the run ended\"}"'
# event_body PCT RISK is the event's request.
event_body() {
  jq -cn --argjson pct "$1" --argjson risk "$2" --arg symbol "$SYMBOL" '{type: "OVERLAY", symbols: [$symbol], target_pct: $pct,
    ramp_up_seconds: 15, hold_seconds: 0, ramp_down_seconds: 5, risk: $risk, actor: "e2e-ops", reason: "e2e: a price event (price-event.sh)"}'
}
# start RISK: an event of +16%, or the largest share HOUSE's loss cap
# allows; sets EVENT, F (the target factor) and BASE.
start() {
  local pct=16 est cap
  simpost /internal/sim/events "$(event_body $pct "$1")"
  if [[ $SIM_STATUS == 409 && $(jq -r .code <<<"$SIM_BODY") == SIM_OVERLAY_LOSS_CAP ]]; then
    est=$(jq -r .details.estimate_usdt <<<"$SIM_BODY")
    cap=$(jq -r .details.cap_usdt <<<"$SIM_BODY")
    pct=$(jq -n "16 * $cap / $est * 0.8 * 100 | floor / 100")
    echo "note: HOUSE's worst loss on +16% would be $est USDT, beyond the cap of $cap: +$pct% instead"
    [[ $(jq -n "$pct >= 0.3") == true ]] || { echo "skip: HOUSE's rooms leave less than +0.3%"; exit 0; }
    simpost /internal/sim/events "$(event_body "$pct" "$1")"
  fi
  if [[ $SIM_STATUS == 403 && $(jq -r .code <<<"$SIM_BODY") == SIM_EVENT_NEEDS_APPROVAL ]]; then
    echo "skip: one operator's hourly budget on $SYMBOL is spent (earlier runs): $SIM_BODY"
    exit 0
  fi
  [[ $SIM_STATUS == 201 ]] || { echo "FAIL the event: $SIM_STATUS $SIM_BODY" >&2; exit 1; }
  EVENT=$(jq -r '.items[0].event_id' <<<"$SIM_BODY")
  F=$(jq -r '.items[0].factor_target' <<<"$SIM_BODY")
  BASE=$(jq -r '.items[0].base_price' <<<"$SIM_BODY")
  [[ $(jq -r '.items[0].status' <<<"$SIM_BODY") == RUNNING ]] || { echo "FAIL not running: $SIM_BODY" >&2; exit 1; }
  echo "ok   event $EVENT: factor $F from $BASE (+$pct%, risk $1)"
}
# holds FILE JQ WHAT: JQ holds over the samples (an array).
holds() {
  if [[ $(jq -s "$2" "$1") != true ]]; then
    echo "FAIL $3; the samples:" >&2
    jq -c '{t, f: .ref.overlay_factor, src: .ref.source_price, last: .ticker.last, mark: .mark.mark_price, index: .mark.index_price,
      ms: .mark.source, asset: .margin.cross.total_asset, q: .quoting, share: .share}' "$1" >&2
    exit 1
  fi
  echo "ok   $3"
}
done_event() { # done_event: the event DONE, its record as JSON in EVENT_JSON
  EVENT_JSON=$(simget "/internal/sim/events?all=1&limit=20" | jq -c --arg id "$EVENT" '.items[] | select(.id == $id)')
  [[ $(jq -r .status <<<"$EVENT_JSON") == DONE ]]
}

echo "== an event with risk (the default)"
start true
sample "$WORK/risk"
ratio='((.ticker.last | tonumber) / (.ref.source_price | tonumber))'
holds "$WORK/risk" "map(.ref.overlay_factor | tonumber) | max >= $F - 0.0015" "the factor reached $F"
holds "$WORK/risk" "$steady | length > 0 and all($ratio / (.ref.overlay_factor | tonumber) | . > 0.998 and . < 1.002)" \
  "the ticker is the reference market's times the factor"
holds "$WORK/risk" "$steady | all(((.depth.bids[0][0] | tonumber) / (.ref.source_price | tonumber)) / (.ref.overlay_factor | tonumber) | . > 0.997 and . < 1.003)" \
  "the book's best bid too"
holds "$WORK/risk" "map(select(.quoting == \"1\" and .share != \"\") | .share | tonumber) | length > 0 and max <= 0.4 * $SHARE_BEFORE" \
  "HOUSE quoted a quarter of each level meanwhile (of $SHARE_BEFORE before)"
holds "$WORK/risk" "map(select(.mark.source == \"PLATFORM\")) | length > 0" "$PERP's mark computed during the event"
holds "$WORK/risk" "map((.mark.index_price | tonumber) / (.ref.source_price | tonumber)) | max >= 1 + 0.7 * ($F - 1) and max <= $F + 0.004" \
  "$PERP's index followed"
holds "$WORK/risk" "map((.mark.mark_price | tonumber) / (.ref.source_price | tonumber)) | max >= 1 + 0.6 * ($F - 1)" "and its mark"
holds "$WORK/risk" "map((.margin.cross.total_asset | tonumber) / ($QTY * $HAIRCUT * (.ref.source_price | tonumber))) | max >= 1 + 0.7 * ($F - 1)" \
  "the margin account's BTC valued with the event"
eventually 20 "the event is DONE" done_event
check_event() {
  [[ $(jq -r "$1" <<<"$EVENT_JSON") == true ]] || { echo "FAIL $2: $EVENT_JSON" >&2; exit 1; }
  echo "ok   $2"
}
check_event "(.end_platform_price | tonumber) / (.end_reference_price | tonumber) - 1 | fabs < 0.0005" \
  "back at the reference price: $(jq -r .end_platform_price <<<"$EVENT_JSON") against $(jq -r .end_reference_price <<<"$EVENT_JSON")"
check_event "(.peak_price | tonumber) / (.base_price | tonumber) >= 1 + 0.7 * ($F - 1)" "the platform's peak $(jq -r .peak_price <<<"$EVENT_JSON")"
REF=$(md "/internal/market/$SYMBOL/reference")
LAST=$(md "/v1/market/$SYMBOL/ticker" | jq -r .last)
[[ $(jq -n "($LAST / $(jq -r .source_price <<<"$REF") - 1) | fabs < 0.0005") == true && $(jq -r .overlay_factor <<<"$REF") == 1 ]] ||
  { echo "FAIL after the event: ticker $LAST, reference $REF" >&2; exit 1; }
echo "ok   the ticker $LAST within 0.05% of the reference market"
BODY=$(md "/v1/market/$SYMBOL/candles?interval=1m&limit=3")
check "[.candles[].high | tonumber] | max >= $BASE * (1 + 0.7 * ($F - 1))" "the spike stays in the 1m candles"
mark_back() { [[ $(md "/v1/market/$PERP/mark-price" | jq -r .source) == "$MARK_BEFORE" ]]; }
eventually 120 "$PERP's mark back on $MARK_BEFORE" mark_back
EVENT=""

echo "== an event without risk"
start false
sample "$WORK/plain"
holds "$WORK/plain" "map(.ref.overlay_factor | tonumber) | max >= $F - 0.0015" "the factor reached $F"
holds "$WORK/plain" "$steady | length > 0 and all($ratio / (.ref.overlay_factor | tonumber) | . > 0.998 and . < 1.002)" \
  "the ticker is the reference market's times the factor"
if [[ $MARK_BEFORE != PLATFORM ]]; then
  holds "$WORK/plain" "all(.mark.source == \"$MARK_BEFORE\")" "$PERP's mark stays on $MARK_BEFORE"
fi
holds "$WORK/plain" "all((.mark.index_price | tonumber) / (.ref.source_price | tonumber) - 1 | fabs < 0.003)" "$PERP's index stays on the reference market"
# margin-service reads the ticker and the factor one after the other: a
# push between the two may value one poll a step off.
holds "$WORK/plain" "map((.margin.cross.total_asset | tonumber) / ($QTY * $HAIRCUT * (.ref.source_price | tonumber)) - 1 | fabs) |
  (map(select(. < 0.003)) | length) >= 0.9 * length and all(. < 0.01)" "the margin account stays valued at the reference market's price"
eventually 20 "the event is DONE" done_event
check_event "(.end_platform_price | tonumber) / (.end_reference_price | tonumber) - 1 | fabs < 0.0005" "back at the reference price"
EVENT=""
echo "all price event checks passed"
