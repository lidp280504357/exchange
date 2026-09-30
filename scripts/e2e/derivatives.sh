#!/usr/bin/env bash
# Perpetual contract trading end to end (implementation plan §7.3 task 5)
# on ETH-USDT-PERP, where HOUSE offers nothing: two new users move USDT to
# FUTURES; the buyer (cross, 10x) bids, the seller (isolated, 20x) sells
# into it; both see their positions, reservations and fees; the buyer
# closes with a reduce-only order that the seller's buy takes, 0.10 higher:
# the profit and the loss are booked, the positions are flat and FUTURES
# holds nothing frozen; the rest moves back to SPOT; the derivatives
# reconciliation (invariant 6) passes. Needs derivatives.trading on, the
# contract TRADING and a mark price (docs/runbook/derivatives.md), and ssh
# to the server.
#
#   scripts/e2e/derivatives.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

SYMBOL=ETH-USDT-PERP

signup() { # signup NAME: registers a user with USDT in FUTURES, sets NAME_TOKEN
  register "e2e-perp-$1-$RUN@example.com" "e2e-perp-$1-$RUN" "e2e perp $RUN"
  eval "${1}_TOKEN=$(jq -r .access_token <<<"$BODY")"
}
echo "== two traders with 500 USDT in FUTURES each"
signup buyer
signup seller
# shellcheck disable=SC2154 # set by signup
BUYER=(-H "Authorization: Bearer $buyer_TOKEN")
# shellcheck disable=SC2154
SELLER=(-H "Authorization: Bearer $seller_TOKEN")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/derivatives/orders?symbol='$SYMBOL'" "" "${BUYER[@]}"; call DELETE "/v1/derivatives/orders?symbol='$SYMBOL'" "" "${SELLER[@]}"'
funded() {
  call GET /v1/account/balances "" "$@"
  [[ $(jq -r '[.balances[] | select(.account_type == "SPOT" and .asset == "USDT")][0].available // "0"' <<<"$BODY") != "0" ]]
}
into_futures() { # into_futures WHO AUTH...
  local who=$1
  shift
  eventually 40 "welcome funds arrived for the $who" funded "$@"
  call POST /v1/account/transfers '{"asset":"USDT","amount":"500","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
    "$@" -H "Idempotency-Key: perp-in-$RUN-$who"
  expect 201 - "move 500 USDT to FUTURES ($who)"
}
into_futures buyer "${BUYER[@]}"
into_futures seller "${SELLER[@]}"

echo "== settings and the mark price"
call PUT /v1/derivatives/settings/$SYMBOL '{"leverage":10}' "${BUYER[@]}"
expect 200 - "buyer: cross, 10x"
check '.margin_mode == "CROSS" and .leverage == 10 and .position_mode == "ONE_WAY"' "defaults kept, leverage set"
call PUT /v1/derivatives/settings/$SYMBOL '{"margin_mode":"ISOLATED","leverage":20}' "${SELLER[@]}"
expect 200 - "seller: isolated, 20x"
call PUT /v1/derivatives/settings/$SYMBOL '{"leverage":51}' "${SELLER[@]}"
expect 400 DERIV_LEVERAGE_EXCEEDED "above the contract's 50x"
call GET /v1/market/$SYMBOL/mark-price ""
expect 200 - "mark price"
MARK=$(jq -r .mark_price <<<"$BODY")
[[ "$MARK" != null ]] || { echo "FAIL $SYMBOL has no mark price (market.reference_feed?)" >&2; exit 1; }
PRICE=$(awk -v m="$MARK" 'BEGIN { printf "%.2f", m }')
CLOSE=$(awk -v p="$PRICE" 'BEGIN { printf "%.2f", p + 0.10 }')
echo "ok   trading at $PRICE (mark $MARK), closing at $CLOSE"

echo "== open: the buyer's bid rests, the seller sells into it"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"0.10\"}" "${BUYER[@]}"
expect 202 - "a bid of 0.10"
check '.status == "NEW" and .position_side == "BOTH" and (.reserved | tonumber) > 0' "its margin and fee are reserved"
BID=$(jq -r .order_id <<<"$BODY")
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"0.10\",\"reduce_only\":true}" "${BUYER[@]}"
expect 422 DERIV_REDUCE_ONLY_REJECTED "a reduce-only order without a position"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"0.10\"}" "${SELLER[@]}"
expect 202 - "the seller's ask"
position() { # position AUTH... : the only position of the contract in BODY
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "$@" && jq -e '.positions | length == 1' <<<"$BODY"
}
eventually 40 "the buyer holds a long" position "${BUYER[@]}"
check '.positions[0].quantity == "0.1" and .positions[0].margin_mode == "CROSS" and .positions[0].leverage == 10' "0.1 long, cross, 10x"
eventually 40 "the seller holds a short" position "${SELLER[@]}"
check '.positions[0].quantity == "-0.1" and .positions[0].margin_mode == "ISOLATED" and (.positions[0].liquidation_price | tonumber) > 0' "0.1 short, isolated, with a liquidation price"
call GET "/v1/derivatives/orders/$BID" "" "${BUYER[@]}"
expect 200 - "the bid"
check '.status == "FILLED" and .reserved == "0" and (.fee | tonumber) > 0' "filled, nothing left reserved, a maker fee paid"

echo "== close: a reduce-only ask, taken by the seller's buy"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$CLOSE\",\"quantity\":\"0.10\",\"reduce_only\":true}" "${BUYER[@]}"
expect 202 - "the buyer's reduce-only ask"
check '.reserved == "0"' "a closing order reserves nothing"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$CLOSE\",\"quantity\":\"0.10\"}" "${SELLER[@]}"
expect 202 - "the seller's buy-back"
flat() { # flat AUTH...
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "$@" && jq -e '.positions | length == 0' <<<"$BODY"
}
eventually 40 "the buyer is flat" flat "${BUYER[@]}"
eventually 40 "the seller is flat" flat "${SELLER[@]}"
call GET "/v1/derivatives/fills?symbol=$SYMBOL" "" "${BUYER[@]}"
expect 200 - "the buyer's fills"
check '(.items | length) == 2 and .items[0].closed_quantity == "0.1" and .items[0].realized_pnl == "0.01" and all(.items[]; .settled)' "0.10 x 0.1 of profit, settled"
call GET "/v1/derivatives/fills?symbol=$SYMBOL" "" "${SELLER[@]}"
check '.items[0].realized_pnl == "-0.01"' "the seller's loss"
settled() { # settled AUTH...: nothing frozen in FUTURES
  call GET /v1/derivatives/account "" "$@" && jq -e '.frozen == "0" and .order_margin == "0" and .position_margin == "0"' <<<"$BODY"
}
eventually 20 "the buyer's FUTURES holds nothing frozen" settled "${BUYER[@]}"
check '(.wallet_balance | tonumber) < 500.01 and (.wallet_balance | tonumber) > 499.9 and .transferable == .available' "500 + 0.01 less the fees; all of it transferable"
eventually 20 "the seller's FUTURES holds nothing frozen" settled "${SELLER[@]}"

call GET "/v1/derivatives/funding?symbol=$SYMBOL" "" "${BUYER[@]}"
expect 200 - "funding history (none unless a funding time passed while holding)"
check '(.items | type) == "array"' "a list"

echo "== back to SPOT"
call GET /v1/derivatives/account "" "${SELLER[@]}"
AVAILABLE=$(jq -r .available <<<"$BODY")
call POST /v1/account/transfers "{\"asset\":\"USDT\",\"amount\":\"$AVAILABLE\",\"from_account_type\":\"FUTURES\",\"to_account_type\":\"SPOT\"}" \
  "${SELLER[@]}" -H "Idempotency-Key: perp-out-$RUN"
expect 201 - "the seller moves $AVAILABLE back"

echo "== the derivatives reconciliation"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all derivatives checks passed"
