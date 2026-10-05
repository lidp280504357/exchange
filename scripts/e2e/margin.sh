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
# Needs margin.enabled on (docs/runbook/margin.md) and ssh to the server.
#
#   scripts/e2e/margin.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

# The hourly charge would add to the first hour's interest the checks
# below expect: keep clear of the hour.
if (( 10#$(date -u +%M) >= 56 )); then
  wait=$(( (60 - 10#$(date -u +%M)) * 60 - 10#$(date -u +%S) + 45 ))
  echo "note: waiting ${wait}s for the hour's interest run"
  sleep "$wait"
fi
# same A B: the decimals A and B are equal.
same() { jq -en --arg a "$1" --arg b "$2" '(($a | tonumber) - ($b | tonumber)) | fabs < 1e-9' >/dev/null; }

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
  [[ $(pg "SELECT count(*) FROM margin.interest_runs WHERE hour = date_trunc('hour', now()) AND status = 'DONE'" | tr -d '[:space:]') == 1 ]]
}
eventually 80 "the current hour's interest run is done" charged
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | sed 's/^/     /'
echo "ok   the ledger's invariants hold, margin 8 and 9 among them"
remote "sudo docker compose $COMPOSE_FILES exec -T margin-service /app/exchangectl margin reconcile" | tail -1 | sed 's/^/     /'
echo "ok   margin invariant 7: the ledger's debts are the loans"
