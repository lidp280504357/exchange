#!/usr/bin/env bash
# Price events on any pair (docs/设计-通用价格控制-2026-10-07.md, J0
# contract §3.5; docs/runbook/market-sim.md): a pair a reference market
# follows (BTC-USDT) gets an OVERLAY - its reference data on the platform
# times a factor that ramps to the target, holds and ramps back to 1, the
# reference market's price again; the simulated market's own pair
# (ASTRA-USDT) a JUMP of its model over the ramp up. Through exchangectl in
# market-sim's container (the operators' key: one operator's share of the
# guards, 30% at once and 50% an hour a pair; beyond, the admin console
# with a second operator).
#
#   scripts/ops/price-event.sh start SYMBOL... (--price P | --pct N) --up S [--hold S] --down S
#                              [--no-risk] [--at TIME] "REASON"
#                                 an event on each SYMBOL: the target as a
#                                 price (one pair) or a change in percent
#                                 (16, -20), reached in --up seconds, held
#                                 --hold (0), back in --down (3 at least);
#                                 600 seconds in all at most. --no-risk
#                                 leaves the perpetuals and the leverage on
#                                 the reference price (default: they follow,
#                                 as in a real spike). --at schedules it
#                                 (RFC 3339, within 24 hours).
#   scripts/ops/price-event.sh stop ID ["REASON"]
#                                 cancels a scheduled event, or brings a
#                                 running one back to 1 in 3 seconds.
#   scripts/ops/price-event.sh list [--all]
#                                 the open events on followed pairs (--all:
#                                 the latest 50 of any status).
#   scripts/ops/price-event.sh overlays
#                                 the factors market-data has now.
#   scripts/ops/price-event.sh on|off
#                                 switch the events on followed pairs (flag
#                                 market.overlay; off puts every pair back
#                                 at 1 at once and ends the running ones).
#
# OPS_ACTOR names the operator (default ops:<local user>).
set -euo pipefail

INFRA=/opt/exchange/infra
COMPOSE="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml"
ACTOR="${OPS_ACTOR:-ops:$(whoami)}"

# ctl SERVICE ARGS... runs exchangectl in a service's container.
ctl() {
  local service=$1 args
  shift
  printf -v args ' %q' "$@"
  ssh exchange "cd $INFRA && $COMPOSE exec -T $service /app/exchangectl$args" </dev/null
}

usage() {
  sed -n '2,37p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

start() {
  local symbols=() target="" up="" hold=0 down="" risk=true at="" reason=""
  while [[ $# -gt 0 ]]; do
    case $1 in
    --price) target="\"target_price\":\"$2\"" && shift 2 ;;
    --pct) target="\"target_pct\":$2" && shift 2 ;;
    --up) up=$2 && shift 2 ;;
    --hold) hold=$2 && shift 2 ;;
    --down) down=$2 && shift 2 ;;
    --no-risk) risk=false && shift ;;
    --at) at=$2 && shift 2 ;;
    --*) usage ;;
    *)
      if [[ $# -eq 1 && ${#symbols[@]} -gt 0 ]]; then
        reason=$1
      else
        symbols+=("$1")
      fi
      shift
      ;;
    esac
  done
  [[ ${#symbols[@]} -gt 0 && -n $target && -n $up && -n $down && -n $reason ]] || usage
  local body
  body=$(jq -cn --args '$ARGS.positional' "${symbols[@]}" | jq -c \
    --argjson up "$up" --argjson hold "$hold" --argjson down "$down" --argjson risk "$risk" \
    --arg at "$at" --arg actor "$ACTOR" --arg reason "$reason" --argjson target "{$target}" \
    '{type: "OVERLAY", symbols: ., ramp_up_seconds: $up, hold_seconds: $hold, ramp_down_seconds: $down, risk: $risk,
      actor: $actor, reason: $reason} + $target + (if $at == "" then {} else {starts_at: $at} end)')
  ctl market-sim sim call POST /internal/sim/events "$body"
}

case ${1:-} in
start)
  shift
  start "$@"
  ;;
stop)
  [[ $# -ge 2 ]] || usage
  ctl market-sim sim call POST "/internal/sim/events/$2/end" \
    "$(jq -cn --arg actor "$ACTOR" --arg reason "${3:-restored by an operator (price-event.sh stop)}" '{actor: $actor, reason: $reason}')"
  ;;
list)
  query=""
  [[ ${2:-} == --all ]] && query="?all=1&limit=50"
  ctl market-sim sim call GET "/internal/sim/events$query" 2>/dev/null |
    jq '[.items[] | select(.type == "OVERLAY") | {id, symbol, status, result, target_factor, factor_now, progress, risk,
      starts_at, base_price, peak_price, end_reference_price, end_platform_price, created_by, ended_by}]'
  ;;
overlays)
  ssh exchange "cd $INFRA && $COMPOSE exec -T market-data-service wget -qO- http://127.0.0.1:8090/internal/market/overlay" | jq .
  ;;
on)
  ctl market-sim flags set market.overlay --on --reason "price events on followed pairs (price-event.sh on)"
  ;;
off)
  ctl market-sim flags set market.overlay --off --reason "price events on followed pairs stop; every pair back at 1 (price-event.sh off)"
  ;;
*) usage ;;
esac
