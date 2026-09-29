#!/usr/bin/env bash
# Withdrawals end to end on Sepolia (implementation plan §6.3 task 10). A
# new user binds an authenticator app (step-ups by TOTP from then on) and
# adds two addresses to the address book, a step-up each: the end-to-end
# sender's (docs/runbook/wallet.md) and another new user's deposit
# address; the test environment's cooling-off is one minute. Then:
#   - a withdrawal during the cooling-off period is refused;
#   - 0.0011 ETH to the sender goes to review (new account, new address),
#     an operator approves it (exchangectl over ssh), the signer signs it,
#     it is broadcast and settled (balance, fund flow) and confirmed after
#     12 confirmations; the sender receives exactly 0.0011 ETH on chain;
#   - 0.02 ETH to the other user completes in the ledger with no fee: the
#     payee gets an INTERNAL deposit;
#   - a third one is canceled while in review and its funds come back.
# The deposits of earlier runs are swept to the hot wallet first. Needs
# wallet.withdraw on (docs/runbook/wallet.md) and ssh to the server; about
# six minutes.
#
#   scripts/e2e/withdraw.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

go build -o "$WORK/sendeth" ./scripts/e2e/sendeth
read -r SENDER BEFORE < <("$WORK/sendeth" -balance)

echo "== sweep earlier deposits to the hot wallet"
exchangectl wallet sweep | head -1

echo "== the payee of the internal withdrawal"
register "e2e-payee-$RUN@example.com" "e2e-payee-$RUN" "e2e payee $RUN"
PAYEE_TOKEN=$(jq -r .access_token <<<"$BODY")
call GET "/v1/wallet/deposit-address?asset=ETH&network=ETH-SEPOLIA" "" -H "Authorization: Bearer $PAYEE_TOKEN"
expect 200 - "the payee's deposit address"
PAYEE_ADDR=$(jq -r .address <<<"$BODY")

EMAIL="e2e-withdraw-$RUN@example.com"
DEVICE="e2e-withdraw-$RUN"
echo "== register $EMAIL and bind an authenticator app"
register "$EMAIL" "$DEVICE" "e2e withdraw $RUN"
TOKEN=$(jq -r .access_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $TOKEN")
wait_resend "$EMAIL"
otp STEP_UP "$EMAIL" "$DEVICE" "$TOKEN"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
expect 200 - "step-up by mail"
call POST /v1/auth/totp/setup "" "${AUTH[@]}" -H "X-Step-Up-Token: $(jq -r .step_up_token <<<"$BODY")"
expect 200 - "authenticator setup"
SECRET=$(jq -r .secret <<<"$BODY")
LAST=$(($(date +%s) / 30))
call POST /v1/auth/totp/confirm "{\"code\":\"$(node "$(dirname "$0")/lib/totp.mjs" "$SECRET")\"}" "${AUTH[@]}"
expect 204 - "bound"

# step_up sets STEP to a token proven with a step the server has not seen
# (each step works once; it accepts the next one early).
step_up() {
  while (($(date +%s) / 30 + 1 <= LAST)); do sleep 1; done
  LAST=$(($(date +%s) / 30 + 1))
  call POST /v1/auth/step-up "{\"totp_code\":\"$(node "$(dirname "$0")/lib/totp.mjs" "$SECRET" 1)\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
  expect 200 - "step-up by the app"
  STEP=$(jq -r .step_up_token <<<"$BODY")
}

echo "== address book"
call POST /v1/wallet/withdraw-addresses "{\"network\":\"ETH-SEPOLIA\",\"address\":\"$SENDER\",\"label\":\"e2e sender\"}" "${AUTH[@]}"
expect 403 AUTH_STEP_UP_REQUIRED "adding an address needs a step-up"
step_up
call POST /v1/wallet/withdraw-addresses "{\"network\":\"ETH-SEPOLIA\",\"address\":\"$SENDER\",\"label\":\"e2e sender\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP"
expect 201 - "the sender's address added"
check '.usable_at > .created_at' "with a cooling-off period"
step_up
call POST /v1/wallet/withdraw-addresses "{\"network\":\"ETH-SEPOLIA\",\"address\":\"$PAYEE_ADDR\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP"
expect 201 - "the payee's deposit address added"
call GET /v1/wallet/withdraw-addresses "" "${AUTH[@]}"
check '.items | length == 2' "two entries"

withdraw() { # withdraw ADDRESS AMOUNT [curl args...]
  local to=$1 amount=$2
  shift 2
  call POST /v1/wallet/withdrawals "{\"asset\":\"ETH\",\"network\":\"ETH-SEPOLIA\",\"address\":\"$to\",\"amount\":\"$amount\"}" "${AUTH[@]}" "$@"
}
withdraw "$SENDER" 0.0011 -H "X-Step-Up-Token: none"
expect 422 WALLET_ADDRESS_COOLDOWN "refused during the cooling-off period"
withdraw 0x000000000000000000000000000000000000dEaD 0.0011 -H "X-Step-Up-Token: none"
expect 422 WALLET_ADDRESS_NOT_WHITELISTED "an address outside the book"
sleep 62

echo "== withdraw 0.0011 ETH to the sender"
node "$(dirname "$0")/lib/deposit-watch.mjs" "$BASE" "$TOKEN" withdrawals,balances >"$WORK/ws.log" 2>&1 &
at_exit "kill $! 2>/dev/null"
disown
step_up
withdraw "$SENDER" 0.0011 -H "X-Step-Up-Token: $STEP" -H "Idempotency-Key: e2e-wd-$RUN"
expect 201 - "requested"
CHAIN_ID=$(jq -r .id <<<"$BODY")
check '.status == "PENDING_REVIEW" and (.risk_reasons | index("NEW_ACCOUNT") != null) and (.risk_reasons | index("NEW_ADDRESS") != null)
  and .fee == "0.0002" and .internal == false' "in review: a new account and a new address"

step_up
withdraw "$PAYEE_ADDR" 0.02 -H "X-Step-Up-Token: $STEP"
expect 201 - "an internal withdrawal requested"
INTERNAL_ID=$(jq -r .id <<<"$BODY")
check '.internal == true and .fee == "0" and .status == "PENDING_REVIEW"' "no fee inside the platform"

step_up
withdraw "$SENDER" 0.001 -H "X-Step-Up-Token: $STEP"
expect 201 - "a third withdrawal"
CANCEL_ID=$(jq -r .id <<<"$BODY")
call DELETE "/v1/wallet/withdrawals/$CANCEL_ID" "" "${AUTH[@]}"
expect 200 - "canceled"
check '.status == "CANCELED"' "CANCELED"

echo "== an operator approves"
exchangectl wallet approve "$CHAIN_ID" --reviewer e2e-ops --reason "end-to-end test"
exchangectl wallet approve "$INTERNAL_ID" --reviewer e2e-ops --reason "end-to-end test"

wait_withdrawal() { # wait_withdrawal ID SECONDS CONDITION WHAT
  local id=$1 secs=$2 cond=$3 what=$4 start=$SECONDS
  while ((SECONDS - start < secs)); do
    if call GET "/v1/wallet/withdrawals/$id" "" "${AUTH[@]}" && [[ $STATUS == 200 && $(jq -r "$cond" <<<"$BODY") == true ]]; then
      printf 'ok   %s (%ss)\n' "$what" "$((SECONDS - start))"
      return 0
    fi
    sleep 5
  done
  printf 'FAIL %s: %s does not hold after %ss for\n%s\n' "$what" "$cond" "$secs" "$BODY" >&2
  exit 1
}
wait_withdrawal "$INTERNAL_ID" 90 '.status == "CONFIRMED" and .tx_hash == null' "the internal withdrawal completes in the ledger"
wait_withdrawal "$CHAIN_ID" 120 '.status == "BROADCAST" or .status == "CONFIRMING" or .status == "CONFIRMED"' "signed and broadcast"
TX=$(jq -r .tx_hash <<<"$BODY")
echo "     transaction $TX"
wait_withdrawal "$CHAIN_ID" 420 '.status == "CONFIRMED" and .confirmations == 12' "confirmed after 12 confirmations"

echo "== balances, fund flow and the chain"
call GET /v1/account/balances "" "${AUTH[@]}"
check '[.balances[] | select(.account_type == "SPOT" and .asset == "ETH")][0] | (.available | tonumber) == 1.9787 and (.frozen | tonumber) == 0' \
  "2 ETH less 0.0011 + 0.0002 fee and 0.02 internal; the canceled one came back"
call GET "/v1/account/ledger?asset=ETH" "" "${AUTH[@]}"
check '[.items[].entry_type] | (index("WITHDRAW_FREEZE") != null and index("WITHDRAW_SETTLE") != null and index("WITHDRAW_UNFREEZE") != null and index("INTERNAL_TRANSFER") != null)' \
  "freeze, settle, release and internal transfer in the fund flow"
call GET /v1/account/balances "" -H "Authorization: Bearer $PAYEE_TOKEN"
check '[.balances[] | select(.account_type == "SPOT" and .asset == "ETH")][0].available | tonumber == 2.02' "the payee got 0.02 ETH"
call GET /v1/wallet/deposits "" -H "Authorization: Bearer $PAYEE_TOKEN"
check ".items[0] | .kind == \"INTERNAL\" and .status == \"CREDITED\" and .tx_hash == \"internal:$INTERNAL_ID\"" "the payee's internal deposit"
read -r _ AFTER < <("$WORK/sendeth" -balance)
# Floats: exact to far below a wei's worth of the check.
[[ $(jq -n "($AFTER - $BEFORE - 0.0011) | fabs < 1e-12") == true ]] ||
  { echo "FAIL the sender's balance went from $BEFORE to $AFTER, not up by 0.0011" >&2; exit 1; }
echo "ok   the sender received 0.0011 ETH on chain ($BEFORE -> $AFTER)"

sleep 3
call GET /v1/notifications "" "${AUTH[@]}"
check '([.items[].type] | index("WITHDRAWAL_REQUESTED") != null) and ([.items[].type] | index("WITHDRAWAL_COMPLETED") != null) and ([.items[].type] | index("WITHDRAWAL_CANCELED") != null)' \
  "notices: requested, completed, canceled"
[[ $(jq -rs "[.[] | select(.channel == \"withdrawals\" and .data.withdrawal_id == \"$CHAIN_ID\") | .data.status] | index(\"CONFIRMED\") != null" "$WORK/ws.log") == true ]] ||
  { echo "FAIL no CONFIRMED push on the withdrawals channel" >&2; cat "$WORK/ws.log" >&2; exit 1; }
echo "ok   the withdrawals channel pushed the confirmation"
echo "all withdrawal checks passed"
