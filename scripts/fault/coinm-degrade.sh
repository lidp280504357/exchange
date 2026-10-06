#!/usr/bin/env bash
# Fault injection: a coin-margined contract's prices fail (coin-margined
# design 2026-10-06 §2, batch G1; contract-degrade.sh on BTC-USD-PERP).
# market-data-service's traffic to the internet (Binance) is dropped:
# Binance's COIN-M mark price and the spot index both go stale; after 10
# seconds without a mark market-data-service reports SystemDegraded and
# derivatives-service puts the contract under reduce-only. When Binance is
# back the mark price is fresh again but the contract stays reduce-only
# until a person lifts it (exchangectl derivatives resume): an opening
# order, in whole contracts against the user's BTC account, is refused
# before and accepted after. Needs derivatives.trading, derivatives.coin_m
# and market.reference_feed on and BTC-USD-PERP TRADING (skipped
# otherwise); about five minutes; the block is always lifted.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'unblock_egress market-data-service >/dev/null 2>&1 || true; cleanup_remote' EXIT

SYMBOL=BTC-USD-PERP
call GET "/v1/market/contracts/$SYMBOL" ""
if [[ $STATUS != 200 || $(jq -r .status <<<"$BODY") != TRADING ]]; then
  echo "SKIP $SYMBOL is not TRADING"
  exit 0
fi
derivatives() { remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives $*"; }
mark() { call GET "/v1/market/$SYMBOL/mark-price" "" && [[ $STATUS == 200 ]]; }
fresh() { mark && jq -e '.mark_price != null and .degraded == false' <<<"$BODY" >/dev/null; }
degraded() { mark && jq -e '.degraded == true' <<<"$BODY" >/dev/null; }
reduce_only() { derivatives states | grep -Eq "^$SYMBOL +true "; }

echo "== a trader with 0.001 BTC in FUTURES"
register "e2e-coindegrade-$RUN@example.com" "e2e-coindegrade-$RUN" "e2e coin degrade $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/derivatives/orders?symbol='$SYMBOL'" "" "${AUTH[@]}"'
balance() { # balance ASSET: the SPOT account's available amount
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '[.balances[] | select(.account_type == "SPOT" and .asset == $a)][0].available // "0"' <<<"$BODY"
}
funded() { [[ $(balance USDT) != 0 ]]; }
eventually 40 "welcome funds arrived" funded
call POST /v1/orders '{"symbol":"BTC-USDT","side":"BUY","type":"MARKET","quote_amount":"200"}' "${AUTH[@]}" -H "Idempotency-Key: coindegrade-btc-$RUN"
expect 202 - "a market buy of 200 USDT of BTC"
enough() { awk -v b="$(balance BTC)" 'BEGIN { exit !(b + 0 >= 0.001) }'; }
eventually 40 "the BTC arrived" enough
call POST /v1/account/transfers '{"asset":"BTC","amount":"0.001","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
  "${AUTH[@]}" -H "Idempotency-Key: coindegrade-in-$RUN"
expect 201 - "move 0.001 BTC to FUTURES"
eventually 60 "$SYMBOL has a fresh mark price" fresh
if reduce_only; then
  echo "     $SYMBOL was under reduce-only already; lifting it first"
  derivatives resume "$SYMBOL" | sed 's/^/     /'
fi
# A bid of one contract 3% under the mark (inside the 5% price band) rests
# without filling: HOUSE's ask is at the reference market's, far above it.
bid() { # bid: an opening order under the mark price in BODY
  local price
  price=$(jq -r '.mark_price | tonumber * 0.97 | floor' <<<"$BODY")
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$price\",\"quantity\":\"1\"}" "${AUTH[@]}"
}
bid
expect 202 - "an opening bid is accepted while the prices are sound"
check '.settle_asset == "BTC"' "its margin is in BTC"
call DELETE "/v1/derivatives/orders?symbol=$SYMBOL" "" "${AUTH[@]}"

echo "== Binance goes silent"
block_egress market-data-service
eventually 60 "the mark price is reported degraded" degraded
eventually 40 "derivatives-service puts $SYMBOL under reduce-only" reduce_only
derivatives states | sed 's/^/     /'
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"1000\",\"quantity\":\"1\"}" "${AUTH[@]}"
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
# The outage degraded every contract on the same index sources: lift the rest.
for other in $(derivatives states | awk 'NR > 1 && $2 == "true" {print $1}'); do
  derivatives resume "$other" | sed 's/^/     /'
done
echo "coin-margined contract degradation survived"
