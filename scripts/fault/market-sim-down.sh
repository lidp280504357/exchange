#!/usr/bin/env bash
# Fault injection: the platform coin's simulated market goes down (ASTRA
# design §9, batch A5). market-sim is stopped: the makers' orders stay on
# ASTRA-USDT's book, the bots stop trading. A minute without its heartbeat
# (the target market-sim reports to market-data-service every 5 seconds)
# halts ASTRA-USDT and ASTRA-USDT-PERP (sim.halt_on_loss); the gauge
# market_sim_heartbeat_age_seconds behind MarketSimHeartbeatLost passes 60.
# When market-sim is back its heartbeat resumes, 30 seconds later the pair
# and the perpetual trade again, and the bots quote again (the price band
# watchdog stays out of it). Needs the bots on (scripts/ops/astra.sh on)
# and sim.halt_on_loss on; about four minutes; market-sim is always started
# again.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'compose "start market-sim" >/dev/null 2>&1 || true; cleanup_remote' EXIT

SYMBOL=ASTRA-USDT
PERP=ASTRA-USDT-PERP
status() { call GET "/v1/market/pairs/$SYMBOL" "" && jq -r .status <<<"$BODY"; }
perp_status() { call GET "/v1/market/contracts/$PERP" "" && jq -r .status <<<"$BODY"; }
booked() {
  call GET "/v1/market/$SYMBOL/depth?limit=20" "" &&
    [[ $(jq '(.bids | length) >= 8 and (.asks | length) >= 8' <<<"$BODY") == true ]]
}
silence() { metric market-data-service 9090 market_sim_heartbeat_age_seconds "symbol=\"$SYMBOL\"" | awk '{printf "%d", $1}'; }
sim() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- http://127.0.0.1:8098/internal/sim"; }

if [[ $(status) != TRADING || $(pg "SELECT enabled FROM config.flags WHERE key = 'sim.halt_on_loss'") != t ]]; then
  echo "skip: $SYMBOL is not trading or sim.halt_on_loss is off"
  exit 0
fi
PERP_WAS=$(perp_status)
eventually 60 "$SYMBOL has a book of 8 levels a side" booked
alive() { local s; s=$(silence) && [[ -n $s ]] && ((s < 15)); }
eventually 30 "market-sim's heartbeat is fresh" alive

echo "== market-sim stops"
compose "stop market-sim" >/dev/null
booked || { echo "FAIL the makers' orders left the book with market-sim" >&2; exit 1; }
echo "ok   the makers' orders stay on the book"
halted() { [[ $(status) == HALT ]] && { [[ $PERP_WAS != TRADING ]] || [[ $(perp_status) == HALT ]]; }; }
eventually 120 "a minute without its heartbeat: $SYMBOL (and $PERP) halted" halted
silent() { local s; s=$(silence) && ((s >= 60)); }
eventually 10 "the heartbeat's age is past a minute (MarketSimHeartbeatLost)" silent

echo "== market-sim starts again"
compose "start market-sim" >/dev/null
wait_healthy market-sim 120
resumed() { [[ $(status) == TRADING ]] && { [[ $PERP_WAS != TRADING ]] || [[ $(perp_status) == TRADING ]]; }; }
eventually 150 "30 seconds after the heartbeat is back: $SYMBOL (and $PERP) trade again" resumed
eventually 180 "the bots quote again: 8 levels a side" booked
[[ $(sim | jq '.watchdog.fired') == 0 ]] || { echo "FAIL the price band watchdog fired after the restart" >&2; exit 1; }
echo "market-sim outage survived"
