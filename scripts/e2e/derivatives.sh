#!/usr/bin/env bash
# Perpetual contract trading end to end (implementation plan §7.3 task 5)
# on ETH-USDT-PERP, against HOUSE (every order trades against HOUSE, user
# decision 2026-10-02): two new users move USDT to FUTURES; leverage stops
# at the contract's 125x; a bid under the market rests with its margin
# reserved until canceled; the buyer (cross, 10x) opens a long with a limit
# buy over the ask, the seller (isolated, 20x) a short with a market sell.
# Then the USDT-margined line closes for a moment as the console closes it
# (design 2026-10-07 product switches, K1b; product.usdt_m off and
# derivatives-service's cancel-open, every user's open orders on the line
# canceled, the market makers' kept): the buyer's resting bid and
# take-profit are canceled, an opening order and a new take-profit are
# PRODUCT_CLOSED, a reduce-only sell of half the long trades with HOUSE;
# opened again, an opening order is taken. Both then close with
# reduce-only market orders. The PnL of each is what its
# fills say ((sold - bought) x quantity), the positions are flat, FUTURES
# holds nothing frozen and its balance is 500 plus the PnL less the fees;
# the rest moves back to SPOT; neither gave the insurance fund anything (the
# liquidation clearance fee is a liquidation's, review C68; liquidations
# run in price-event.sh); the derivatives reconciliation (invariant 6)
# passes. Prices come from the mark price and HOUSE's book (Binance's).
# Needs derivatives.trading on, the contract TRADING with HOUSE liquidity
# and a mark price (docs/runbook/derivatives.md), and ssh to the server.
#
#   scripts/e2e/derivatives.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"
# shellcheck source=lib/products.sh
source "$(dirname "$0")/lib/products.sh"

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
call PUT /v1/derivatives/settings/$SYMBOL '{"leverage":126}' "${SELLER[@]}"
expect 400 DERIV_LEVERAGE_EXCEEDED "above the contract's 125x"
call GET /v1/market/$SYMBOL/mark-price ""
expect 200 - "mark price"
MARK=$(jq -r .mark_price <<<"$BODY")
[[ "$MARK" != null ]] || { echo "FAIL $SYMBOL has no mark price (market.reference_feed?)" >&2; exit 1; }
book() { # sets ASK from HOUSE's book (Binance's)
  call GET "/v1/market/$SYMBOL/depth?limit=5" "" && [[ $STATUS == 200 ]] || return 1
  ASK=$(jq -r '.asks[0][0] // empty' <<<"$BODY")
  [[ -n $ASK ]]
}
eventually 40 "$SYMBOL shows HOUSE's asks" book
LOW=$(awk -v m="$MARK" 'BEGIN { printf "%.2f", m * 0.97 }')
HIGH=$(awk -v m="$MARK" 'BEGIN { printf "%.2f", m * 1.05 }')
OVER=$(awk -v a="$ASK" 'BEGIN { printf "%.2f", a * 1.002 }')
echo "ok   mark $MARK, ask $ASK"

echo "== a bid under the market rests with its margin reserved"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.10\",\"reduce_only\":true}" "${BUYER[@]}"
expect 422 DERIV_REDUCE_ONLY_REJECTED "a reduce-only order without a position"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.10\"}" "${BUYER[@]}"
expect 202 - "a bid of 0.10 at $LOW"
check '.status == "NEW" and .position_side == "BOTH" and (.reserved | tonumber) > 0' "its margin and fee are reserved"
REST=$(jq -r .order_id <<<"$BODY")
order_is() { # order_is ORDER STATUS AUTH...
  local id=$1 want=$2
  shift 2
  call GET "/v1/derivatives/orders/$id" "" "$@" && [[ $(jq -r .status <<<"$BODY") == "$want" ]]
}
eventually 40 "it rests (OPEN)" order_is "$REST" OPEN "${BUYER[@]}"
call DELETE "/v1/derivatives/orders/$REST" "" "${BUYER[@]}"
expect 202 - "cancel it"
eventually 40 "it is canceled" order_is "$REST" CANCELED "${BUYER[@]}"
check '.reserved == "0"' "nothing reserved any more"

echo "== open against HOUSE: the buyer's limit buy over the ask, the seller's market sell"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$OVER\",\"quantity\":\"0.10\"}" "${BUYER[@]}"
expect 202 - "a buy of 0.10 at $OVER"
OPEN=$(jq -r .order_id <<<"$BODY")
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"0.10\"}" "${SELLER[@]}"
expect 202 - "the seller's market sell of 0.10"
position() { # position AUTH... : the only position of the contract in BODY
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "$@" && jq -e '.positions | length == 1' <<<"$BODY"
}
eventually 40 "the buyer holds a long" position "${BUYER[@]}"
check '.positions[0].quantity == "0.1" and .positions[0].margin_mode == "CROSS" and .positions[0].leverage == 10' "0.1 long, cross, 10x"
eventually 40 "the seller holds a short" position "${SELLER[@]}"
check '.positions[0].quantity == "-0.1" and .positions[0].margin_mode == "ISOLATED" and (.positions[0].liquidation_price | tonumber) > 0' "0.1 short, isolated, with a liquidation price"
call GET "/v1/derivatives/orders/$OPEN" "" "${BUYER[@]}"
check '.status == "FILLED" and .reserved == "0" and (.fee | tonumber) > 0' "the buy filled, nothing left reserved, a fee paid"

echo "== the USDT-margined line closed: its open orders canceled, only closes taken (product switches, K1b)"
BID_BODY="{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.10\"}"
TP_BODY="{\"symbol\":\"$SYMBOL\",\"kind\":\"TAKE_PROFIT\",\"trigger_price\":\"$HIGH\",\"quantity\":\"0.05\"}"
call POST /v1/derivatives/orders "$BID_BODY" "${BUYER[@]}"
expect 202 - "a bid of 0.10 at $LOW"
BID=$(jq -r .order_id <<<"$BODY")
eventually 40 "it rests (OPEN)" order_is "$BID" OPEN "${BUYER[@]}"
call POST /v1/derivatives/conditional-orders "$TP_BODY" "${BUYER[@]}"
expect 201 - "a take-profit of 0.05 at $HIGH"
TP=$(jq -r .conditional_id <<<"$BODY")
# derivatives-service's sweep takes what came in from 30 s before a line
# closed (an order in flight as it closed, with room for the clocks);
# older ones are the console's call to cancel, which is what this checks.
sleep 31
product_close usdt_m
check "([.orders[] | select(.order_id == \"$BID\" and .type == \"ORDER\")] | length) == 1 and ([.orders[] | select(.order_id == \"$TP\" and .type == \"CONDITIONAL\")] | length) == 1 and .canceled == (.orders | length)" \
  "closing it canceled the bid and the take-profit ($(jq -r .canceled <<<"$BODY") orders of the line)"
eventually 40 "the bid is canceled" order_is "$BID" CANCELED "${BUYER[@]}"
call GET "/v1/derivatives/conditional-orders?symbol=$SYMBOL" "" "${BUYER[@]}"
check "[.items[] | select(.conditional_id == \"$TP\")][0] | .status == \"CANCELED\" and .reason == \"PRODUCT_CLOSED\"" "the take-profit CANCELED, PRODUCT_CLOSED"
call POST /v1/derivatives/orders "$BID_BODY" "${BUYER[@]}"
expect 403 PRODUCT_CLOSED "an opening order"
check '.details.product == "usdt_m"' "naming the line"
call POST /v1/derivatives/conditional-orders "$TP_BODY" "${BUYER[@]}"
expect 403 PRODUCT_CLOSED "a new take-profit"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"0.05\",\"reduce_only\":true}" "${BUYER[@]}"
expect 202 - "a reduce-only market sell of 0.05: the holder closes"
half() { position "${BUYER[@]}" >/dev/null && [[ $(jq -r '.positions[0].quantity' <<<"$BODY") == 0.05 ]]; }
eventually 40 "the long is down to 0.05, against HOUSE" half
product_state usdt_m
check '.closed == true and .open_positions >= 2' "derivatives-service counts the line closed with the two positions on it"
product_open usdt_m
reopened() { call POST /v1/derivatives/orders "$BID_BODY" "${BUYER[@]}" && [[ $STATUS == 202 ]]; }
eventually 20 "open again: an opening order is taken" reopened
AGAIN=$(jq -r .order_id <<<"$BODY")
call DELETE "/v1/derivatives/orders/$AGAIN" "" "${BUYER[@]}"
expect 202 - "cancel it"
eventually 40 "it is canceled" order_is "$AGAIN" CANCELED "${BUYER[@]}"

echo "== close: reduce-only market orders"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"0.05\",\"reduce_only\":true}" "${BUYER[@]}"
expect 202 - "the buyer's reduce-only sell of the rest"
check '.reserved == "0"' "a closing order reserves nothing"
call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"0.10\",\"reduce_only\":true}" "${SELLER[@]}"
expect 202 - "the seller's reduce-only buy-back"
flat() { # flat AUTH...
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "$@" && jq -e '.positions | length == 0' <<<"$BODY"
}
eventually 40 "the buyer is flat" flat "${BUYER[@]}"
eventually 40 "the seller is flat" flat "${SELLER[@]}"
# books AUTH...: every fill settled, 0.1 closed, the PnL is (sold - bought) x
# quantity, all taker fills against HOUSE; sets PNL and FEES.
books() {
  call GET "/v1/derivatives/fills?symbol=$SYMBOL&limit=50" "" "$@"
  jq -e '
    def value(side): [.items[] | select(.side == side) | (.price | tonumber) * (.quantity | tonumber)] | add // 0;
    (.items | length) >= 2 and all(.items[]; .settled and .role == "TAKER")
    and ([.items[].closed_quantity | tonumber] | add) == 0.1
    and ((value("SELL") - value("BUY")) - ([.items[].realized_pnl | tonumber] | add) | fabs) < 1e-6' <<<"$BODY" >/dev/null || return 1
  PNL=$(jq '[.items[].realized_pnl | tonumber] | add' <<<"$BODY")
  FEES=$(jq '[.items[].fee | tonumber] | add' <<<"$BODY")
}
eventually 20 "the buyer's fills: settled, 0.1 closed, PnL = (sold - bought) x quantity" books "${BUYER[@]}"
echo "ok   the buyer's PnL $PNL, fees $FEES"
settled() { # settled AUTH...: nothing frozen in FUTURES
  call GET /v1/derivatives/account "" "$@" && jq -e '.frozen == "0" and .order_margin == "0" and .position_margin == "0"' <<<"$BODY"
}
eventually 20 "the buyer's FUTURES holds nothing frozen" settled "${BUYER[@]}"
check "((.wallet_balance | tonumber) - (500 + $PNL - $FEES) | fabs) < 1e-6 and .transferable == .available" "500 + the PnL less the fees; all of it transferable"
eventually 20 "the seller's fills: settled, 0.1 closed, PnL = (sold - bought) x quantity" books "${SELLER[@]}"
echo "ok   the seller's PnL $PNL, fees $FEES"
eventually 20 "the seller's FUTURES holds nothing frozen" settled "${SELLER[@]}"
check "((.wallet_balance | tonumber) - (500 + $PNL - $FEES) | fabs) < 1e-6" "500 + the PnL less the fees"

call GET "/v1/derivatives/funding?symbol=$SYMBOL" "" "${BUYER[@]}"
expect 200 - "funding history (none unless a funding time passed while holding)"
check '(.items | type) == "array"' "a list"

echo "== back to SPOT"
call GET /v1/derivatives/account "" "${SELLER[@]}"
AVAILABLE=$(jq -r .available <<<"$BODY")
call POST /v1/account/transfers "{\"asset\":\"USDT\",\"amount\":\"$AVAILABLE\",\"from_account_type\":\"FUTURES\",\"to_account_type\":\"SPOT\"}" \
  "${SELLER[@]}" -H "Idempotency-Key: perp-out-$RUN"
expect 201 - "the seller moves $AVAILABLE back"

# The liquidation clearance fee (review C68) is a liquidation's alone:
# ordinary closes give the insurance fund nothing. Liquidations themselves
# run in price-event.sh, which can move the mark.
echo "== no clearance fee without a liquidation"
no_fee() { # no_fee WHO AUTH...
  local who=$1
  shift
  call GET "/v1/account/ledger?asset=USDT&type=INSURANCE_CONTRIBUTION" "" "$@"
  expect 200 - "the $who's insurance fund entries"
  check '.items | length == 0' "none for the $who"
}
no_fee buyer "${BUYER[@]}"
no_fee seller "${SELLER[@]}"

echo "== the derivatives reconciliation"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | sed 's/^/     /'
echo "ok   invariant 6 holds"
echo "all derivatives checks passed"
