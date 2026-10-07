#!/usr/bin/env bash
# Fault injection: a price event on a followed pair through restarts
# (docs/设计-通用价格控制-2026-10-07.md §5 J2; J0 contract §2.1, §3.4). An
# event without risk holds BTC-USDT's factor (+2%, less when HOUSE's loss
# cap says so) for four minutes. market-sim stops: market-data puts the
# pair back at 1 within 5 seconds of the last push (nothing of an overlay
# outlives its pusher). market-sim starts again and goes on with the
# running event along its stored schedule: the factor is back. Then
# market-data-service restarts (nothing stored: at 1) and market-sim's next
# pushes bring the factor back. Restoring it ends the event, DONE and
# CANCELED, the pair at 1 within 3 seconds. Turns market.overlay on for the
# run and back as it was; about three minutes. Both services are always
# started again.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"

SYMBOL=BTC-USDT
md() { remote "sudo docker compose $COMPOSE_FILES exec -T market-data-service wget -qO- 'http://127.0.0.1:8090$1'"; }
factor() { md "/internal/market/$SYMBOL/reference" | jq -r .overlay_factor; }
simpost() {
  local out
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST $1 $(printf %q "$2") 2>&1" || true)
  SIM_STATUS=$(grep -oE '^HTTP [0-9]+' <<<"$out" | tail -1 | cut -d' ' -f2)
  SIM_BODY=$(sed -n '/^{/,/^}/p' <<<"$out")
}
simget() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- 'http://127.0.0.1:8098$1'"; }

call GET "/v1/market/pairs/$SYMBOL" ""
if [[ $(jq -r .status <<<"$BODY") != TRADING || $(md "/internal/market/$SYMBOL/reference" | jq -r '.followed and .fresh') != true ]]; then
  echo "skip: $SYMBOL is not trading or no fresh reference price follows it"
  exit 0
fi

if ! STATE=$(exchangectl flags show market.overlay 2>&1); then
  [[ $STATE == *"is not set"* ]] || { echo "FAIL could not read market.overlay: $STATE" >&2; exit 1; }
  STATE='{"enabled":false}'
fi
if [[ $(jq -r .enabled <<<"$STATE") != true ]]; then
  exchangectl flags set market.overlay --on --reason "fault overlay-restart.sh: price events on followed pairs for the drill" >/dev/null
  at_exit 'exchangectl flags set market.overlay --off --reason "fault overlay-restart.sh: back as it was" >/dev/null || echo "WARN market.overlay left on" >&2'
  sleep 6
fi
EVENT=""
# shellcheck disable=SC2016 # expanded when the script ends
at_exit '[[ -z $EVENT ]] || simpost "/internal/sim/events/$EVENT/end" "{\"actor\":\"fault-ops\",\"reason\":\"fault: the drill ended\"}"'
at_exit 'compose "start market-sim market-data-service" >/dev/null 2>&1 || true'

event_body() {
  jq -cn --argjson pct "$1" --arg symbol "$SYMBOL" '{type: "OVERLAY", symbols: [$symbol], target_pct: $pct, ramp_up_seconds: 10,
    hold_seconds: 240, ramp_down_seconds: 5, risk: false, actor: "fault-ops", reason: "fault: a price event through restarts (overlay-restart.sh)"}'
}
PCT=2
simpost /internal/sim/events "$(event_body $PCT)"
if [[ $SIM_STATUS == 409 && $(jq -r .code <<<"$SIM_BODY") == SIM_OVERLAY_LOSS_CAP ]]; then
  PCT=$(jq -n "2 * $(jq -r .details.cap_usdt <<<"$SIM_BODY") / $(jq -r .details.estimate_usdt <<<"$SIM_BODY") * 0.8 * 100 | floor / 100")
  echo "note: +$PCT% within HOUSE's loss cap"
  simpost /internal/sim/events "$(event_body "$PCT")"
fi
if [[ $SIM_STATUS == 403 && $(jq -r .code <<<"$SIM_BODY") == SIM_EVENT_NEEDS_APPROVAL ]]; then
  echo "skip: one operator's hourly budget on $SYMBOL is spent"
  exit 0
fi
[[ $SIM_STATUS == 201 ]] || { echo "FAIL the event: $SIM_STATUS $SIM_BODY" >&2; exit 1; }
EVENT=$(jq -r '.items[0].event_id' <<<"$SIM_BODY")
F=$(jq -r '.items[0].factor_target' <<<"$SIM_BODY")
echo "ok   event $EVENT: factor $F, held four minutes"
at_f() { [[ $(factor) == "$F" ]]; }
at_1() { [[ $(factor) == 1 ]]; }
eventually 40 "$SYMBOL at $F" at_f

echo "== market-sim stops"
compose "stop market-sim" >/dev/null
STOPPED=$(date +%s)
eventually 30 "back at 1 without market-sim's pushes" at_1
WAITED=$(($(date +%s) - STOPPED))
((WAITED <= 10)) || { echo "FAIL back at 1 only ${WAITED}s after market-sim stopped" >&2; exit 1; }
echo "ok   within ${WAITED}s of the stop (5 s after the last push, and the polling)"
compose "start market-sim" >/dev/null
wait_healthy market-sim 120
eventually 60 "market-sim goes on with the event: $SYMBOL at $F again" at_f

echo "== market-data-service restarts"
compose "restart market-data-service" >/dev/null
wait_healthy market-data-service 180
eventually 60 "the new market-data takes market-sim's pushes: $SYMBOL at $F again" at_f

echo "== restore now"
simpost "/internal/sim/events/$EVENT/end" '{"actor":"fault-ops","reason":"fault: restore now"}'
[[ $SIM_STATUS == 200 ]] || { echo "FAIL ending the event: $SIM_STATUS $SIM_BODY" >&2; exit 1; }
eventually 20 "back at 1 within 3 seconds and the polling" at_1
done_event() {
  simget "/internal/sim/events?all=1&limit=20" | jq -e --arg id "$EVENT" '.items[] | select(.id == $id) | .status == "DONE" and .result == "CANCELED" and .ended_by == "fault-ops"' >/dev/null
}
eventually 20 "the event DONE, CANCELED by the operator" done_event
EVENT=""
echo "price events survive the restarts"
