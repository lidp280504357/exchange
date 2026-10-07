#!/usr/bin/env bash
# Margin trading end to end, batch E1 (margin design 2026-10-06 §9): the
# public terms (assets with their pools and rates, the cross account's and
# each pair's isolated terms); a new user moves 100 USDT into the cross
# account (a repeated Idempotency-Key replays it), may borrow 200 at 3x,
# borrows 150 with the first hour's interest charged at once, is refused
# beyond the leverage, sees the account valued (margin level 250 /
# 150.0015) and the loan with its hourly charge; may take back what keeps
# the margin level at the warning level and none of what the debt holds;
# repays ALL, interest first, and takes the rest back to SPOT. The hourly
# charging run is up to date; the ledger reconciliation (margin invariants
# 8 and 9) and invariant 7 (the ledger's debts = margin-service's loans)
# hold. An isolated BTC-USDT account takes only its pair's two assets.
# Batch E2: with 100 USDT on the cross account, a limit buy of 1.2 SOL over
# the ask is refused without AUTO_BORROW, fills against HOUSE with it (the
# loan is what the freeze lacked, the SOL lands on the margin account), and
# a sell with AUTO_REPAY repays the loan and its interest from what it
# brings (the settlement's automatic repayment, margin-service's loans
# following it); invariant 5 counts the margin trades. B160: a market buy
# by quantity with AUTO_BORROW borrows its freeze, the band above the
# market included, and about 10 s after it fills the loan is down to what
# the fill cost beyond the free USDT. Batch E3: with
# 50 USDT in, 0.6 SOL bought with AUTO_BORROW, an administrators'
# liquidation (exchangectl margin liquidate, as an approved request) sells
# to HOUSE as much SOL as the loan and the 2% fee need (review DD C19),
# repays the loan, charges the fee and frees the account with the rest of
# its SOL.
# Margin trading stays off for everyone else: the script switches
# margin.enabled and margin.auto_borrow on for its own user only and puts
# them back as they were when it ends, also after a failure (the
# coordination decision of 2026-10-06 03:24); it holds the ops lock for
# that, as the other scripts that change switches. Needs ssh to the
# server.
#
#   scripts/e2e/margin.sh
set -euo pipefail
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "e2e $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

# The hourly charge would add to the first hour's interest the checks
# below expect: keep clear of the hour.
if (( 10#$(date -u +%M) >= 56 )); then
  wait=$(( (60 - 10#$(date -u +%M)) * 60 - 10#$(date -u +%S) + 75 ))
  echo "note: waiting ${wait}s for the hour's interest run"
  sleep "$wait"
fi
# same A B: the decimals A and B are equal.
same() { jq -en --arg a "$1" --arg b "$2" '(($a | tonumber) - ($b | tonumber)) | fabs < 1e-9' >/dev/null; }

# on_for_user KEY USER_ID switches KEY on for the user only, unless it is
# on for everyone already: the user joins KEY's allowed users, and KEY
# goes back as it was when the script ends. Services see a change within
# 5 seconds (flags.RefreshInterval).
on_for_user() {
  local key=$1 user=$2 state enabled users
  # Only "not set" reads as off (review CY C16): a failed ssh would record
  # off as the state to put back and switch the flag off for everyone.
  if ! state=$(exchangectl flags show "$key" 2>&1); then
    if [[ $state == *"is not set"* ]]; then
      state='{"enabled":false,"rules":{}}'
    else
      echo "FAIL could not read $key: $state" >&2
      exit 1
    fi
  fi
  enabled=$(jq -r .enabled <<<"$state")
  users=$(jq -r '(.rules.users.allow // []) | join(",")' <<<"$state")
  if [[ $enabled == true ]] && jq -e '.rules == {} or .rules == null' <<<"$state" >/dev/null; then
    echo "note: $key is on for everyone"
    return
  fi
  at_exit "put_back $key $enabled '$users'"
  exchangectl flags set "$key" --on --allow-users "${users:+$users,}$user" --reason "e2e margin.sh: on for its user $user only" >/dev/null
  echo "note: $key on for $user only until the script ends"
}
put_back() { # put_back KEY ENABLED USERS
  local state=--off
  [[ $2 == true ]] && state=--on
  exchangectl flags set "$1" "$state" --allow-users "$3" --reason "e2e margin.sh: back as it was" >/dev/null ||
    echo "WARN could not put $1 back (enabled $2, allowed users '$3')" >&2
}

echo "== the public terms"
call GET /v1/margin/assets ""
expect 200 - "margin assets"
check '[.items[].asset] | contains(["USDT","BTC","ETH","ASTRA"])' "USDT, BTC, ETH and ASTRA are on the margin list"
check '(.items[] | select(.asset == "USDT")) | .borrowable and .collateral and .haircut == "1" and .interest_model == "FIXED" and (.hourly_rate | tonumber) > 0 and (.utilization | tonumber) >= 0 and (.floating.kink | tonumber) > 0' "USDT: borrowable at a fixed hourly rate, its pool's use"
check '(.items[] | select(.asset == "ASTRA")) | .haircut == "0.7"' "ASTRA counts at 0.7 as margin"
call GET /v1/margin/pairs ""
expect 200 - "margin pairs"
check '.cross.leverage == 3 and .cross.warn_level == "1.3" and .cross.liquidation_level == "1.1" and .cross.liquidation_fee_rate == "0.02"' "the cross account: 3x, warned at 1.3, liquidated at 1.1, a 2% fee"
check '(.items[] | select(.symbol == "BTC-USDT")) | .isolated and .leverage == 10 and .warn_level == "1.1" and .liquidation_level == "1.05"' "BTC-USDT isolated at 10x: 1.10 and 1.05"

EMAIL="e2e-margin-$RUN@example.com"
echo "== register $EMAIL"
register "$EMAIL" "e2e-margin-$RUN" "e2e margin $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
USER_ID=$(jq -r .user_id <<<"$BODY")
on_for_user margin.enabled "$USER_ID"
on_for_user margin.auto_borrow "$USER_ID"
sleep 6
spot() { # spot ASSET prints the SPOT account's available amount
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '[.balances[] | select(.asset == $a and .account_type == "SPOT")][0].available // "0"' <<<"$BODY"
}
funded() { [[ $(spot USDT) != 0 ]]; }
eventually 40 "welcome funds arrived" funded
START=$(spot USDT)

call GET /v1/margin/accounts "" "${AUTH[@]}"
expect 200 - "margin accounts"
check '.cross.account == "MARGIN_CROSS" and .cross.margin_level == null and (.cross.balances | length) == 0 and .isolated == []' "an empty cross account, no isolated ones"
call GET "/v1/margin/max-borrowable?account=MARGIN_CROSS&asset=USDT" "" "${AUTH[@]}"
expect 200 - "max borrowable before a transfer"
check '.amount == "0"' "nothing to borrow against yet"
call POST /v1/margin/borrow '{"account":"MARGIN_CROSS","asset":"USDT","amount":"10"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-b0"
expect 422 MARGIN_LIMIT "a borrow without collateral"

echo "== transfer in"
IN='{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"100"}'
call POST /v1/margin/transfer "$IN" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-in"
expect 200 - "100 USDT into the cross account"
TRANSFER=$(jq -r .transfer_id <<<"$BODY")
call POST /v1/margin/transfer "$IN" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-in"
expect 200 - "the same key again"
check ".transfer_id == \"$TRANSFER\"" "replays the first transfer"
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"101"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-in"
expect 409 COMMON_IDEMPOTENCY_CONFLICT "the same key with another amount"
same "$(spot USDT)" "$(jq -rn --arg s "$START" '($s | tonumber) - 100')" || { echo "FAIL SPOT is not 100 lower" >&2; exit 1; }
echo "ok   SPOT is 100 lower"
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_ISOLATED","symbol":"BTC-USDT","asset":"ETH","amount":"1"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-eth"
expect 422 MARGIN_ASSET_NOT_BORROWABLE "an isolated BTC-USDT account takes no ETH"

echo "== borrow"
call GET "/v1/margin/max-borrowable?account=MARGIN_CROSS&asset=USDT" "" "${AUTH[@]}"
expect 200 - "max borrowable"
check '.amount == "200" and .limited_by == "LEVERAGE"' "100 at 3x: 200 more"
call POST /v1/margin/borrow '{"account":"MARGIN_CROSS","asset":"USDT","amount":"150"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-b1"
expect 200 - "borrow 150 USDT"
check '.principal == "150" and .interest == "0.0015" and .interest_model == "FIXED"' "owing 150 and the first hour, 150 x 0.0010%"
call POST /v1/margin/borrow '{"account":"MARGIN_CROSS","asset":"USDT","amount":"60"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-b2"
expect 422 MARGIN_LIMIT "60 more is beyond 3x"
check '.details.max_borrowable == "49.9955"' "what is left: (250 - 150.0015) x 2 - 150.0015"
call GET /v1/margin/accounts "" "${AUTH[@]}"
expect 200 - "the account after the loan"
check '.cross.total_asset == "250" and .cross.total_liability == "150.0015" and .cross.margin_level == "1.66665"' "250 against 150.0015: margin level 1.66665"
check '.cross.balances[0] | .asset == "USDT" and .free == "250" and .borrowed == "150" and .interest == "0.0015" and .net == "99.9985"' "the USDT row: held, owed, net"
call GET /v1/margin/loans "" "${AUTH[@]}"
expect 200 - "loans"
check '.items | length == 1 and .[0].principal == "150" and .[0].hourly_rate == "0.00001"' "one loan at 0.0010% an hour"
call GET "/v1/margin/interest?account=MARGIN_CROSS&asset=USDT" "" "${AUTH[@]}"
expect 200 - "interest charges"
check '.items | length == 1 and .[0].principal == "150" and .[0].interest == "0.0015"' "the first hour, charged when borrowing"

echo "== transfer out"
call POST /v1/margin/transfer '{"direction":"OUT","account":"MARGIN_CROSS","asset":"USDT","amount":"60"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out1"
expect 422 MARGIN_LEVEL_TOO_LOW "60 out would leave the level at 1.267"
check '.details.max_transferable == "54.99805"' "at most what keeps 1.3: 250 - 1.3 x 150.0015"
call POST /v1/margin/transfer '{"direction":"OUT","account":"MARGIN_CROSS","asset":"USDT","amount":"50"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out2"
expect 200 - "50 out"

echo "== repay"
call POST /v1/margin/repay '{"account":"MARGIN_CROSS","asset":"USDT","amount":"ALL"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-r1"
expect 200 - "repay ALL"
check '.interest_repaid == "0.0015" and .principal_repaid == "150" and .loan.principal == "0" and .loan.interest == "0"' "interest first, then the principal: nothing owed"
call GET /v1/margin/accounts "" "${AUTH[@]}"
check '.cross.margin_level == null and .cross.total_liability == "0" and .cross.total_asset == "49.9985"' "no debt: no margin level, 49.9985 left"
call POST /v1/margin/transfer '{"direction":"OUT","account":"MARGIN_CROSS","asset":"USDT","amount":"49.9985"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out3"
expect 200 - "the rest back to SPOT"
same "$(spot USDT)" "$(jq -rn --arg s "$START" '($s | tonumber) - 0.0015')" || { echo "FAIL SPOT is not the start less the interest" >&2; exit 1; }
echo "ok   SPOT is back, less the 0.0015 of interest"

echo "== the hourly interest and the invariants"
charged() { # the current hour's charging run finished
  [[ $(pg "SELECT count(*) FROM margin.interest_runs WHERE hour = date_trunc('hour', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AND status = 'DONE'" | tr -d '[:space:]') == 1 ]]
}
eventually 80 "the current hour's interest run is done" charged
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | sed 's/^/     /'
echo "ok   the ledger's invariants hold, margin 8 and 9 among them"
remote "sudo docker compose $COMPOSE_FILES exec -T margin-service /app/exchangectl margin reconcile" | tail -1 | sed 's/^/     /'
echo "ok   margin invariant 7: the ledger's debts are the loans"

echo "== orders on the cross account (E2)"
SYMBOL=SOL-USDT
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"100"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-in2"
expect 200 - "100 USDT into the cross account again"
place() { # place JSON; sets ORDER
  call POST /v1/orders "$1" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-$(date +%s%N)"
  expect 202 - "place $(jq -r '"\(.side) \(.type) \(.quantity) \(.symbol) @ \(.price) on \(.account) \(.side_effect // "NONE")"' <<<"$1")"
  ORDER=$(jq -r .order_id <<<"$BODY")
}
status_is() { # status_is ORDER STATUS
  call GET "/v1/orders/$1" "" "${AUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == "$2" ]]
}
book() { # book: BODY holds SYMBOL's two-sided book
  call GET "/v1/market/$SYMBOL/depth?limit=5" "" && [[ $STATUS == 200 ]] &&
    jq -e '(.bids | length) > 0 and (.asks | length) > 0' <<<"$BODY" >/dev/null
}
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/orders?symbol=$SYMBOL" "" "${AUTH[@]}"'
eventually 30 "$SYMBOL shows a two-sided book" book
HIGH=$(jq -r '.asks[0][0] | tonumber * 1.005 * 100 | floor / 100' <<<"$BODY")
call POST /v1/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"1.2\",\"account\":\"MARGIN_CROSS\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-poor"
expect 422 LEDGER_INSUFFICIENT_BALANCE "1.2 SOL at $HIGH needs more than the 100 free without AUTO_BORROW"
place "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"1.2\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_BORROW\"}"
BUY=$ORDER
eventually 40 "the margin buy is FILLED against HOUSE" status_is "$BUY" FILLED
check '.account == "MARGIN_CROSS" and .side_effect == "AUTO_BORROW"' "the order says its account and side effect"
call GET /v1/margin/loans "" "${AUTH[@]}"
expect 200 - "loans"
check "(.items | length) == 1 and .items[0].asset == \"USDT\" and ((.items[0].principal | tonumber) - ($HIGH * 1.2 - 100) | fabs) < 0.000001" "AUTO_BORROW borrowed what 100 lacked of the freeze"
call GET /v1/margin/accounts "" "${AUTH[@]}"
check '[.cross.balances[] | select(.asset == "SOL")][0] | (.free | tonumber) == 1.1988' "1.2 SOL bought, the 0.1% fee off: on the margin account"
check '(.cross.margin_level | tonumber) > 1.3' "the margin level stays above the warning level"

echo "== a sell with AUTO_REPAY repays the loan"
eventually 30 "$SYMBOL shows a two-sided book" book
LOW=$(jq -r '.bids[0][0] | tonumber * 0.995 * 100 | ceil / 100' <<<"$BODY")
place "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"1.198\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_REPAY\"}"
SELL=$ORDER
eventually 40 "the margin sell is FILLED against HOUSE" status_is "$SELL" FILLED
repaid() {
  call GET /v1/margin/loans "" "${AUTH[@]}" && [[ $STATUS == 200 ]] && jq -e '.items == []' <<<"$BODY" >/dev/null
}
eventually 40 "the proceeds repaid the loan and its interest" repaid
call GET "/v1/margin/accounts" "" "${AUTH[@]}"
check '.cross.total_liability == "0" and .cross.margin_level == null' "nothing owed"
LEFT=$(jq -r '[.cross.balances[] | select(.asset == "USDT")][0].free' <<<"$BODY")
call POST /v1/margin/transfer "{\"direction\":\"OUT\",\"account\":\"MARGIN_CROSS\",\"asset\":\"USDT\",\"amount\":\"$LEFT\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out4"
expect 200 - "the $LEFT USDT left back to SPOT"

echo "== a market buy by quantity borrows what its fill cost, not its band (B160)"
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"20"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-in-b160"
expect 200 - "20 USDT into the cross account"
eventually 30 "$SYMBOL shows a two-sided book" book
# About 30 USDT of SOL, more than the 20 free: the buy borrows, and its
# freeze (the quantity at the protection price, the band above the market)
# borrows the band too, which comes back as the order ends.
BYQ=$(jq -r '30 / (.asks[0][0] | tonumber) * 1000 | floor / 1000' <<<"$BODY")
place "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"$BYQ\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_BORROW\"}"
eventually 40 "the market buy by quantity is FILLED against HOUSE" status_is "$ORDER" FILLED
COST=$(jq -r .filled_quote <<<"$BODY")
cost_borrowed() { # the loan: what the fill cost beyond the 20 free (its first hour's interest paid first)
  call GET /v1/margin/loans "" "${AUTH[@]}" && [[ $STATUS == 200 ]] &&
    jq -e --argjson cost "$COST" '[.items[] | select(.asset == "USDT")][0] | (.principal | tonumber) <= $cost - 20 + 0.001' <<<"$BODY" >/dev/null
}
eventually 45 "the band repaid about 10 s after the fill: the loan is what $COST USDT cost beyond the 20 free" cost_borrowed
SOLD=$(jq -rn --argjson q "$BYQ" '$q * 0.999 * 1000 | floor / 1000')
place "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$SOLD\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_REPAY\"}"
eventually 40 "the SOL sold with AUTO_REPAY" status_is "$ORDER" FILLED
eventually 40 "the proceeds repaid the rest of the loan" repaid
call GET "/v1/margin/accounts" "" "${AUTH[@]}"
LEFT=$(jq -r '[.cross.balances[] | select(.asset == "USDT")][0].free' <<<"$BODY")
call POST /v1/margin/transfer "{\"direction\":\"OUT\",\"account\":\"MARGIN_CROSS\",\"asset\":\"USDT\",\"amount\":\"$LEFT\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out-b160"
expect 200 - "the $LEFT USDT left back to SPOT"

echo "== a liquidation (E3), as an administrators' approved request"
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"50"}' "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-in3"
expect 200 - "50 USDT into the cross account"
eventually 30 "$SYMBOL shows a two-sided book" book
HIGH=$(jq -r '.asks[0][0] | tonumber * 1.005 * 100 | floor / 100' <<<"$BODY")
place "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$HIGH\",\"quantity\":\"0.6\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_BORROW\"}"
eventually 40 "the margin buy is FILLED against HOUSE" status_is "$ORDER" FILLED
call GET /v1/margin/loans "" "${AUTH[@]}"
check '(.items | length) == 1 and .items[0].asset == "USDT" and (.items[0].principal | tonumber) > 0' "a USDT loan to liquidate"
remote "sudo docker compose $COMPOSE_FILES exec -T margin-service /app/exchangectl margin liquidate --user $USER_ID --account MARGIN_CROSS" | tail -1 | cut -c1-160 | sed 's/^/     /'
liquidated() {
  call GET /v1/margin/liquidations "" "${AUTH[@]}" && [[ $STATUS == 200 ]] && jq -e '.items[0].status == "COMPLETED"' <<<"$BODY" >/dev/null
}
eventually 90 "the liquidation completed" liquidated
check '(.items | length) == 1 and .items[0].account == "MARGIN_CROSS" and .items[0].repaid[0].asset == "USDT" and (.items[0].repaid[0].amount | tonumber) > 0 and (.items[0].fee | tonumber) > 0 and .items[0].insurance_covered == "0" and .items[0].completed_at != null' "the SOL sold to HOUSE, the loan repaid, the 2% fee charged, nothing from the insurance fund"
call GET /v1/margin/loans "" "${AUTH[@]}"
check '.items == []' "nothing owed after it"
call GET /v1/margin/accounts "" "${AUTH[@]}"
check '.cross.status == "NORMAL" and .cross.total_liability == "0" and ([.cross.balances[] | select(.asset == "SOL")][0].free // "0" | tonumber) > 0.1' "the account free again with the SOL the debt did not need"
LEFT=$(jq -r '[.cross.balances[] | select(.asset == "USDT")][0].free' <<<"$BODY")
SOL_LEFT=$(jq -r '[.cross.balances[] | select(.asset == "SOL")][0].free' <<<"$BODY")
call POST /v1/margin/transfer "{\"direction\":\"OUT\",\"account\":\"MARGIN_CROSS\",\"asset\":\"USDT\",\"amount\":\"$LEFT\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out5"
expect 200 - "the $LEFT USDT left after the liquidation back to SPOT"
call POST /v1/margin/transfer "{\"direction\":\"OUT\",\"account\":\"MARGIN_CROSS\",\"asset\":\"SOL\",\"amount\":\"$SOL_LEFT\"}" "${AUTH[@]}" -H "Idempotency-Key: e2e-margin-$RUN-out6"
expect 200 - "the $SOL_LEFT SOL left after the liquidation back to SPOT"
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | grep -E "TRADE_SETTLE_MATCHES_TRADES|MARGIN_" | sed 's/^/     /'
echo "ok   the margin trades settle in invariant 5's sums"
remote "sudo docker compose $COMPOSE_FILES exec -T margin-service /app/exchangectl margin reconcile" | tail -1 | sed 's/^/     /'
echo "ok   margin invariant 7 after the automatic repayment and the liquidation"
