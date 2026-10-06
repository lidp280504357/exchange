#!/usr/bin/env bash
# The platform coin ASTRA on the test server (docs/设计-平台币ASTRA与模拟做市-2026-10-02.md):
#
#   scripts/ops/astra.sh profile  its display name, introductions, site and
#                                 default logo (deploy/instruments/astra.svg)
#                                 through exchangectl; operators change them
#                                 later in the admin console. Each run is a
#                                 new profile version.
#   scripts/ops/astra.sh open     ASTRA-USDT moves from PREPARE to TRADING.
#   scripts/ops/astra.sh seed     the simulated market's bots (design §4): 24
#                                 users registered like anyone (astra-bot-NN
#                                 @example.com), handed to market-sim (6
#                                 makers, 12 takers, 4 trend followers, 2
#                                 executors), credited the whole supply of
#                                 1,000,000,000 ASTRA and 100,000 USDT each
#                                 (ledger adjustments, keyed, so a rerun adds
#                                 nothing), and made fee-free
#                                 (MARKET_MAKER_USER_IDS; the trading services
#                                 restart, under the ops lock). Registers only
#                                 the bots market-sim does not know yet.
#   scripts/ops/astra.sh mint AMOUNT [ASSET]
#                                 more for the bots (ASTRA by default), spread
#                                 evenly, audited (ledger adjustments).
#   scripts/ops/astra.sh on|off   switch the bots (flag sim.enabled).
#   scripts/ops/astra.sh perp-open
#                                 ASTRA-USDT-PERP moves from PREPARE to
#                                 TRADING (its index is the platform's
#                                 ASTRA-USDT; HOUSE does not quote it).
#   scripts/ops/astra.sh perp-on|perp-off
#                                 switch the bots on the perpetual (flag
#                                 sim.perp); they top up their margin from
#                                 their spot USDT.
#   scripts/ops/astra.sh events-on|events-off
#                                 allow the operators' price events (flag
#                                 sim.events).
#   scripts/ops/astra.sh guard-on|guard-off
#                                 halt ASTRA-USDT and its perpetual a minute
#                                 after market-sim's heartbeat stopped
#                                 (flag sim.halt_on_loss).
#   scripts/ops/astra.sh status   what market-sim reports.
#
# Adjustments need the flag ledger.manual_adjustment.
set -euo pipefail

INFRA=/opt/exchange/infra
ROOT="$(dirname "$0")/../.."
COMPOSE="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml"
SYMBOL=ASTRA-USDT
PERP=ASTRA-USDT-PERP

# ctl SERVICE ARGS... runs exchangectl in a service's container, stdin passed on.
ctl() {
  local service=$1 args
  shift
  printf -v args ' %q' "$@"
  ssh exchange "cd $INFRA && $COMPOSE exec -T $service /app/exchangectl$args"
}

# sim [JSON] reads market-sim's state, or registers a bot with JSON: a
# change, signed with SIM_API_SECRET by exchangectl in market-sim's
# container.
sim() {
  if [[ $# -eq 0 ]]; then
    ssh exchange "cd $INFRA && $COMPOSE exec -T market-sim wget -qO- http://127.0.0.1:8098/internal/sim"
  else
    ctl market-sim sim call POST /internal/sim/bots "$1" </dev/null
  fi
}

# role N is the role of bot number N (1-based): 6 makers, 12 takers, 4
# trend followers, 2 executors; supply N its share of the 1,000,000,000 ASTRA.
role() {
  if (($1 <= 6)); then echo MAKER; elif (($1 <= 18)); then echo TAKER; elif (($1 <= 22)); then echo TREND; else echo EXECUTOR; fi
}
supply() {
  case $(role "$1") in
  MAKER) echo 50000000 ;;
  TAKER | TREND) echo 40000000 ;;
  EXECUTOR) echo 30000000 ;;
  esac
}

seed() {
  [[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/lock.sh" run --owner "astra.sh seed" -- bash "$0" seed
  # shellcheck source=../e2e/lib/common.sh
  source "$ROOT/scripts/e2e/lib/common.sh"
  local known n label email user
  known=$(sim | jq -r '.bots[].label')
  for n in $(seq 1 24); do
    label=$(printf 'bot-%02d' "$n")
    if grep -qx "$label" <<<"$known"; then
      continue
    fi
    email="astra-$label@example.com"
    echo "== register $email ($(role "$n"))"
    register "$email" "astra-$label" "$(openssl rand -base64 24)"
    user=$(jq -r .user_id <<<"$BODY")
    sim "{\"user_id\":\"$user\",\"role\":\"$(role "$n")\",\"label\":\"$label\"}" >/dev/null
  done

  echo "== funds"
  sim | jq -r '.bots[] | "\(.label) \(.user_id)"' | while read -r label user; do
    n=$((10#${label#bot-}))
    ctl ledger-service ledger adjust --user "$user" --asset ASTRA --amount "$(supply "$n")" \
      --reason "the simulated market's supply (ASTRA design §5.2)" --key "seed-astra-$label-ASTRA-v1" </dev/null
    ctl ledger-service ledger adjust --user "$user" --asset USDT --amount 100000 \
      --reason "the simulated market's USDT (ASTRA design §4)" --key "seed-astra-$label-USDT-v1" </dev/null
  done

  echo "== fee-free (MARKET_MAKER_USER_IDS)"
  local ids
  ids=$(sim | jq -r '[.bots[].user_id] | join(",")')
  ssh exchange "cd $INFRA && sudo sed -i 's/^MARKET_MAKER_USER_IDS=.*/MARKET_MAKER_USER_IDS=$ids/' apps.env &&
    (grep -q '^MARKET_MAKER_USER_IDS=' apps.env || echo 'MARKET_MAKER_USER_IDS=$ids' | sudo tee -a apps.env >/dev/null) &&
    $COMPOSE up -d --wait --wait-timeout 180 spot-trading-service derivatives-service"
  sim | jq '{bots: (.bots | length), roles: ([.bots[].role] | group_by(.) | map({(.[0]): length}) | add)}'
}

mint() {
  local amount=${1:?amount} asset=${2:-ASTRA} count each stamp
  count=$(sim | jq '.bots | length')
  ((count > 0)) || { echo "no bots" >&2; exit 1; }
  each=$(jq -rn --arg a "$amount" --argjson n "$count" '($a | tonumber) / $n | floor | tostring')
  stamp=$(date -u +%Y%m%dT%H%M%S)
  sim | jq -r '.bots[] | "\(.label) \(.user_id)"' | while read -r label user; do
    ctl ledger-service ledger adjust --user "$user" --asset "$asset" --amount "$each" \
      --reason "more for the simulated market's bots (astra.sh mint)" --key "mint-$asset-$stamp-$label" </dev/null
  done
}

case "${1:-}" in
profile)
  ctl instrument-service instruments profile ASTRA --display-name Astra \
    --zh "ASTRA 是 Astras 的平台币，只在本站交易，不能充值或提现；它的行情来自平台自己的市场。" \
    --zh-tw "ASTRA 是 Astras 的平台幣，只在本站交易，不能充值或提現；它的行情來自平台自己的市場。" \
    --en "ASTRA is the Astras platform coin. It trades only here and cannot be deposited or withdrawn; its market is the platform's own." \
    --website https://astras.vip --logo - --logo-type image/svg+xml \
    --reason "ASTRA's default profile (design 2026-10-02 §5.3)" <"$ROOT/deploy/instruments/astra.svg"
  ;;
open)
  ctl instrument-service instruments pair-status "$SYMBOL" --to TRADING --reason "ASTRA-USDT opens (design 2026-10-02)" </dev/null
  ;;
seed) seed ;;
mint)
  shift
  mint "$@"
  ;;
on)
  ctl market-sim flags set sim.enabled --on --allow-symbols "$SYMBOL" --reason "the simulated market's bots trade (astra.sh on)" </dev/null
  ;;
off)
  ctl market-sim flags set sim.enabled --off --reason "the simulated market's bots stop (astra.sh off)" </dev/null
  ;;
perp-open)
  ctl instrument-service instruments contract-status "$PERP" --to TRADING --reason "ASTRA-USDT-PERP opens (design 2026-10-02 batch A4)" </dev/null
  ;;
perp-on)
  ctl market-sim flags set sim.perp --on --allow-symbols "$PERP" --reason "the bots make the platform coin's perpetual (astra.sh perp-on)" </dev/null
  ;;
perp-off)
  ctl market-sim flags set sim.perp --off --reason "the bots leave the perpetual (astra.sh perp-off)" </dev/null
  ;;
events-on)
  ctl market-sim flags set sim.events --on --allow-symbols "$SYMBOL" --reason "operators' price events allowed (astra.sh events-on)" </dev/null
  ;;
events-off)
  ctl market-sim flags set sim.events --off --reason "operators' price events stopped (astra.sh events-off)" </dev/null
  ;;
guard-on)
  ctl market-sim flags set sim.halt_on_loss --on --reason "a silent simulated market halts its pair (astra.sh guard-on)" </dev/null
  ;;
guard-off)
  ctl market-sim flags set sim.halt_on_loss --off --reason "no halt on a silent simulated market (astra.sh guard-off)" </dev/null
  ;;
status) sim | jq . ;;
*)
  sed -n '2,42p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
