#!/usr/bin/env bash
# Fault injection: a contract's prices fail (implementation plan §7.3 task
# 10, requirements §11.7). market-data-service's traffic to the internet
# (Binance) is dropped: the reference, and with it the index and mark
# price of BTC-USDT-PERP, go stale; after 10 seconds without a mark
# market-data-service reports SystemDegraded and derivatives-service puts
# the contract under reduce-only. When Binance is back the mark price is
# fresh again but the contract stays reduce-only until a person lifts it
# (here exchangectl derivatives resume; the admin console does the same):
# an opening order is refused before and accepted after. Needs
# derivatives.trading and market.reference_feed on; about five minutes;
# the block is always lifted.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
# Through common.sh's EXIT trap, not a trap of our own: replacing it would
# skip the at_exit commands (the bid's cancel below). Registered first, the
# egress comes back last.
at_exit 'unblock_egress market-data-service >/dev/null 2>&1 || true'

SYMBOL=BTC-USDT-PERP
derivatives() { remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives $*"; }
mark() { call GET "/v1/market/$SYMBOL/mark-price" "" && [[ $STATUS == 200 ]]; }
fresh() { mark && jq -e '.mark_price != null and .degraded == false' <<<"$BODY" >/dev/null; }
degraded() { mark && jq -e '.degraded == true' <<<"$BODY" >/dev/null; }
reduce_only() { derivatives states | grep -Eq "^$SYMBOL +true "; }

echo "== a trader with 200 USDT in FUTURES"
register "e2e-degrade-$RUN@example.com" "e2e-degrade-$RUN" "e2e degrade $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/derivatives/orders?symbol='$SYMBOL'" "" "${AUTH[@]}"'
funded() {
  call GET /v1/account/balances "" "${AUTH[@]}"
  [[ $(jq -r '[.balances[] | select(.account_type == "SPOT" and .asset == "USDT")][0].available // "0"' <<<"$BODY") != "0" ]]
}
eventually 40 "welcome funds arrived" funded
call POST /v1/account/transfers '{"asset":"USDT","amount":"200","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
  "${AUTH[@]}" -H "Idempotency-Key: degrade-in-$RUN"
expect 201 - "move 200 USDT to FUTURES"
eventually 60 "$SYMBOL has a fresh mark price" fresh
if reduce_only; then
  echo "     $SYMBOL was under reduce-only already; lifting it first"
  derivatives resume "$SYMBOL" | sed 's/^/     /'
fi
# A bid 3% under the mark (inside the 5% price band) rests without filling:
# HOUSE's ask is at the reference market's, far above it.
bid() { # bid: an opening order under the mark price in BODY
  local price
  price=$(jq -r '.mark_price | tonumber * 0.97 | floor' <<<"$BODY")
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$price\",\"quantity\":\"0.001\"}" "${AUTH[@]}"
}
bid
expect 202 - "an opening bid is accepted while the prices are sound"
call DELETE "/v1/derivatives/orders?symbol=$SYMBOL" "" "${AUTH[@]}"

echo "== Binance goes silent"
block_egress market-data-service
eventually 60 "the mark price is reported degraded" degraded
eventually 40 "derivatives-service puts $SYMBOL under reduce-only" reduce_only
derivatives states | sed 's/^/     /'
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"1000\",\"quantity\":\"0.001\"}" "${AUTH[@]}"
[[ $STATUS == 422 || $STATUS == 503 ]] || { echo "FAIL an opening order during the outage: $STATUS $BODY" >&2; exit 1; }
echo "ok   opening orders are refused ($(jq -r .code <<<"$BODY"))"

echo "== Binance is back"
unblock_egress market-data-service
eventually 360 "the mark price is fresh again" fresh
reduce_only || { echo "FAIL $SYMBOL left reduce-only by itself" >&2; exit 1; }
echo "ok   $SYMBOL stays reduce-only until a person lifts it"
bid
expect 422 DERIV_REDUCE_ONLY_MODE "an opening bid is still refused"
derivatives resume "$SYMBOL" | sed 's/^/     /'
lifted() { ! reduce_only; }
eventually 10 "reduce-only is lifted" lifted
call GET "/v1/market/$SYMBOL/mark-price" ""
bid
expect 202 - "an opening bid is accepted again"
# The outage degraded every contract on the same index source: lift the rest.
for other in $(derivatives states | awk 'NR > 1 && $2 == "true" {print $1}'); do
  derivatives resume "$other" | sed 's/^/     /'
done
echo "contract degradation survived"
