#!/usr/bin/env bash
# Fault injection: Binance's mark price stream goes silent (coin-margined
# design 2026-10-06 §3.1, batch G3a). market-data-service's traffic to
# Binance's USDⓈ-M futures streams (fstream.binance.com, every address it
# resolves to) is dropped at the host's firewall; the spot streams and the
# REST endpoints stay reachable. BTC-USDT-PERP follows Binance's mark
# price (market.reference_mark; the drill turns it on for the contract
# when it is off and off again at the end): about 10 seconds into the
# silence its prices are the self-computed ones (the index from the spot
# streams), market_mark_source_degraded is 1 (MarkPriceSourceDegraded)
# and the contract is not degraded: its mark price goes on every second
# and trading is not reduce-only. When the stream is back the contract
# follows Binance again. The contracts' books stall with the stream and
# come back too (HOUSE stops quoting the contracts meanwhile).
# Needs market.reference_feed on; about three minutes; the block is always
# lifted and the flag restored.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"

SYMBOL=BTC-USDT-PERP
FLAG=market.reference_mark
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
SET_FLAG=""
ctl() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T -e EXCHANGECTL_ACTOR=fault-mark-source-outage market-data-service /app/exchangectl $args"
}
restore() {
  unblock_egress market-data-service >/dev/null 2>&1 || true
  if [[ -n $SET_FLAG ]]; then
    ctl flags set "$FLAG" --off --force --reason "mark-source-outage drill ends" >/dev/null 2>&1 ||
      echo "FAIL $FLAG not turned off again; run: exchangectl flags set $FLAG --off --force --reason ..." >&2
  fi
  cleanup_remote
}
trap restore EXIT

gauge() { metric market-data-service 9090 "$1" "symbol=\"$SYMBOL\"" | awk '{printf "%d", $1}'; }
following() { [[ $(gauge market_mark_source) == 1 && $(gauge market_mark_source_degraded) == 0 ]]; }
standing_in() { [[ $(gauge market_mark_source) == 0 && $(gauge market_mark_source_degraded) == 1 ]]; }
# The public mark price, not degraded: when it was computed.
mark_at() {
  call GET "/v1/market/$SYMBOL/mark-price" "" && [[ $STATUS == 200 && $(jq -r .degraded <<<"$BODY") == false ]] || return 1
  jq -r '.updated_at // ""' <<<"$BODY"
}
# It is computed again within two seconds.
moving() {
  local a b
  a=$(mark_at) && [[ -n $a ]] && sleep 2 && b=$(mark_at) && [[ -n $b && $b != "$a" ]]
}
reduce_only() {
  remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives states" |
    awk -v s="$SYMBOL" -v since="$STARTED" 'NR > 1 && $1 == s && $2 == "true" && $4 >= since' | grep -q .
}

echo "== $SYMBOL follows Binance's mark price"
fresh() { local a; a=$(gauge market_mark_reference_age_seconds) && ((a >= 0 && a < 5)); }
eventually 60 "Binance's mark price of $SYMBOL arrives" fresh
if ! following; then
  state=$(ctl flags list | awk -v k="$FLAG" '$1 == k {print $2}')
  if [[ $state == on ]]; then
    echo "SKIP $FLAG is on but not for $SYMBOL; the drill leaves its rules alone" >&2
    exit 0
  fi
  ctl flags set "$FLAG" --on --allow-symbols "$SYMBOL" --force --reason "mark-source-outage drill" >/dev/null
  SET_FLAG=1
fi
eventually 60 "$SYMBOL follows Binance's mark price" following
eventually 20 "its mark price is computed every second" moving

echo "== Binance's futures streams go silent"
IP=$(remote "sudo docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' exchange-infra-market-data-service-1")
[[ $IP =~ ^[0-9.]+$ ]] || {
  echo "FAIL no IP address for market-data-service" >&2
  exit 1
}
block_fstream() {
  local addr
  for addr in $(remote 'for i in 1 2 3; do getent ahostsv4 fstream.binance.com; sleep 1; done' | awk '{print $1}' | sort -u); do
    remote "sudo iptables -C DOCKER-USER -s $IP -d $addr -m comment --comment exchange-fault-market-data-service -j DROP 2>/dev/null ||
      sudo iptables -I DOCKER-USER -s $IP -d $addr -m comment --comment exchange-fault-market-data-service -j DROP"
  done
}
block_fstream
blocked=$(date +%s)
eventually 60 "within seconds $SYMBOL stands in with the self-computed prices (MarkPriceSourceDegraded)" standing_in
echo "     switched about $(($(date +%s) - blocked)) s after the block (the stream is stale after 10 s)"
block_fstream # the host may have answered with other addresses since
age=$(gauge market_mark_reference_age_seconds)
((age >= 10)) || {
  echo "FAIL the self-computed prices stood in while Binance's were $age s old" >&2
  exit 1
}
eventually 20 "the mark price goes on every second, not degraded" moving
[[ $(gauge market_contract_degraded) == 0 ]] || {
  echo "FAIL $SYMBOL degraded while the self-computed prices stood in" >&2
  exit 1
}
sleep 20
eventually 10 "20 seconds on it still goes on" moving
if reduce_only; then
  echo "FAIL $SYMBOL went reduce-only" >&2
  exit 1
fi
echo "ok   $SYMBOL is not reduce-only"

echo "== Binance is back"
unblock_egress market-data-service
eventually 180 "$SYMBOL follows Binance's mark price again" following
eventually 20 "its mark price is computed every second" moving
echo "mark price source outage survived"
