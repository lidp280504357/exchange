#!/usr/bin/env bash
# The custody wallet end to end (ADR-0011, docs/runbook/custody.md), with
# the test environment's stand-in for the custodian's gateway (udun-mock,
# driven over ssh). wallet-service's Udun adapter runs unchanged: requests
# and callbacks are signed and checked as with the real gateway.
#   - USDT moves on TRC20, BEP20 and ERC20, BTC and ETH on their chains;
#   - a new user gets a TRC20 address from the custodian (the same one
#     again) and a Bitcoin one;
#   - the custodian reports 30 USDT: credited once to the balance, the
#     custodian's retry and a replay change nothing; 0.5 USDT, below the
#     minimum, goes to UNCLAIMED_DEPOSIT;
#   - callbacks with a forged signature or a stale timestamp are refused
#     (also one forged over the internet) and logged;
#   - with an authenticator app bound, 12 USDT go to a TRON address: in
#     review, approved (exchangectl), handed to the custodian (SUBMITTED),
#     sent (CONFIRMED with its transaction) and settled; 10 USDT to an
#     address the custodian fails end FAILED with the funds back;
#   - a reconciliation of the custodian finds nothing missing.
# Needs wallet.withdraw on and ssh to the server; about three minutes.
#
#   scripts/e2e/custody.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

USDT_TRC20="195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
# Valid TRON addresses outside the platform: the custodian sends to the
# first and fails transfers to the second.
PAYEE=TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7
FAILING=TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj

# mock ARGS... drives the mock gateway on the server.
mock() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T udun-mock /app/udun-mock $args"
}

# eventually_call SECONDS PATH CONDITION WHAT polls a GET until the jq
# CONDITION holds for its body.
eventually_call() {
  local secs=$1 path=$2 cond=$3 what=$4 start=$SECONDS
  while ((SECONDS - start < secs)); do
    if call GET "$path" "" "${AUTH[@]}" && [[ $STATUS == 200 && $(jq -r "$cond" <<<"$BODY") == true ]]; then
      printf 'ok   %s (%ss)\n' "$what" "$((SECONDS - start))"
      return 0
    fi
    sleep 3
  done
  printf 'FAIL %s: %s does not hold after %ss for\n%s\n' "$what" "$cond" "$secs" "$BODY" >&2
  exit 1
}

EMAIL="e2e-custody-$RUN@example.com"
DEVICE="e2e-custody-$RUN"
echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "e2e custody $RUN"
TOKEN=$(jq -r .access_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $TOKEN")

echo "== networks"
call GET "/v1/wallet/networks?asset=USDT" "" "${AUTH[@]}"
expect 200 - "USDT's networks"
check '[.networks[] | select(.deposit_enabled and .withdraw_enabled) | .display_name] | sort == ["BEP20", "ERC20", "TRC20"]' \
  "TRC20, BEP20 and ERC20, open both ways"
call GET "/v1/wallet/networks" "" "${AUTH[@]}"
check '([.networks[] | select(.asset == "BTC" and .network == "BTC" and .address_format == "BTC")] | length == 1)
  and ([.networks[] | select(.asset == "ETH") | .network] | sort == ["ETH", "ETH-SEPOLIA"])' "BTC on Bitcoin, ETH on Ethereum and Sepolia"

echo "== deposit addresses from the custodian"
call GET "/v1/wallet/deposit-address?asset=USDT&network=TRON" "" "${AUTH[@]}"
expect 200 - "a TRC20 address"
ADDR=$(jq -r .address <<<"$BODY")
check '(.address | test("^T[1-9A-HJ-NP-Za-km-z]{33}$")) and .min_deposit == "1" and .confirmations == 20' "Base58 with the network's minimum"
call GET "/v1/wallet/deposit-address?asset=USDT&network=TRON" "" "${AUTH[@]}"
check ".address == \"$ADDR\"" "the same address again"
call GET "/v1/wallet/deposit-address?asset=BTC&network=BTC" "" "${AUTH[@]}"
expect 200 - "a Bitcoin address"
check '.address | test("^bc1q[02-9ac-hj-np-z]{38}$")' "bech32 on mainnet"

call GET /v1/account/balances "" "${AUTH[@]}"
BEFORE=$(jq -r '[.balances[] | select(.account_type == "SPOT" and .asset == "USDT")][0].available // "0"' <<<"$BODY")

echo "== deposits"
node "$(dirname "$0")/lib/deposit-watch.mjs" "$BASE" "$TOKEN" deposits,withdrawals >"$WORK/ws.log" 2>&1 &
at_exit "kill $! 2>/dev/null"
disown
TRADE=$(mock deposit --address "$ADDR" --coin "$USDT_TRC20" --amount 30 | jq -r .trade_id)
echo "     the custodian reports 30 USDT (trade $TRADE)"
eventually_call 90 /v1/wallet/deposits '[.items[] | select(.status == "CREDITED" and .amount == "30" and .network == "TRON")] | length == 1' \
  "credited once"
call GET /v1/account/balances "" "${AUTH[@]}"
check "[.balances[] | select(.account_type == \"SPOT\" and .asset == \"USDT\")][0].available | tonumber == ($BEFORE + 30)" "30 USDT more"

[[ $(mock replay --trade "$TRADE" | jq -r .accepted) == true ]] || { echo "FAIL a replay was not answered success" >&2; exit 1; }
echo "ok   the custodian's replay is answered success"
[[ $(mock replay --trade "$TRADE" --age 600 | jq -r .accepted) == false ]] || { echo "FAIL a stale callback was accepted" >&2; exit 1; }
[[ $(mock replay --trade "$TRADE" --forge | jq -r .accepted) == false ]] || { echo "FAIL a forged callback was accepted" >&2; exit 1; }
echo "ok   stale and forged callbacks are refused"
FORM="timestamp=$(date +%s)&nonce=123456&sign=0123456789abcdef0123456789abcdef&body=%7B%22tradeId%22%3A%22e2e-$RUN%22%7D"
STATUS=$(curl -s -o "$WORK/body" -w '%{http_code}' -X POST "$BASE/v1/wallet/callbacks/udun" -H 'Content-Type: application/x-www-form-urlencoded' --data "$FORM")
BODY=$(cat "$WORK/body")
expect 401 WALLET_CALLBACK_SIGNATURE "a forged callback over the internet is refused"
LOGGED=$(pg "SELECT count(*) FROM wallet.custody_callbacks WHERE result = 'REJECTED' AND received_at > now() - interval '5 minutes'")
((LOGGED >= 3)) || { echo "FAIL $LOGGED refused callbacks logged, want 3" >&2; exit 1; }
echo "ok   the refused callbacks are logged ($LOGGED)"
call GET /v1/wallet/deposits "" "${AUTH[@]}"
check '[.items[] | select(.amount == "30")] | length == 1' "still one deposit of 30"

mock deposit --address "$ADDR" --coin "$USDT_TRC20" --amount 0.5 >/dev/null
eventually_call 90 /v1/wallet/deposits '[.items[] | select(.amount == "0.5" and .unclaimed and .reason == "BELOW_MINIMUM" and .status == "REJECTED")] | length == 1' \
  "0.5 USDT below the minimum goes to UNCLAIMED_DEPOSIT"

echo "== an authenticator app for the step-ups"
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

step_up() {
  while (($(date +%s) / 30 + 1 <= LAST)); do sleep 1; done
  LAST=$(($(date +%s) / 30 + 1))
  call POST /v1/auth/step-up "{\"totp_code\":\"$(node "$(dirname "$0")/lib/totp.mjs" "$SECRET" 1)\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
  expect 200 - "step-up by the app"
  STEP=$(jq -r .step_up_token <<<"$BODY")
}

echo "== withdrawals through the custodian"
mock outcome --address "$FAILING" --status 4 >/dev/null
for to in "$PAYEE" "$FAILING"; do
  step_up
  call POST /v1/wallet/withdraw-addresses "{\"network\":\"TRON\",\"address\":\"$to\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP"
  expect 201 - "TRON address $to added"
done
step_up
call POST /v1/wallet/withdraw-addresses "{\"network\":\"TRON\",\"address\":\"0x000000000000000000000000000000000000dEaD\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP"
expect 400 WALLET_INVALID_ADDRESS "an EVM address on TRON is refused"
sleep 62

withdraw() { # withdraw ADDRESS AMOUNT
  step_up
  call POST /v1/wallet/withdrawals "{\"asset\":\"USDT\",\"network\":\"TRON\",\"address\":\"$1\",\"amount\":\"$2\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP"
  expect 201 - "$2 USDT to $1 requested"
}
withdraw "$PAYEE" 12
SENT_ID=$(jq -r .id <<<"$BODY")
check '.status == "PENDING_REVIEW" and .custody == true and .fee == "1"' "in review, with the custodian's network"
withdraw "$FAILING" 10
FAIL_ID=$(jq -r .id <<<"$BODY")
exchangectl wallet approve "$SENT_ID" --reviewer e2e-ops --reason "end-to-end test"
exchangectl wallet approve "$FAIL_ID" --reviewer e2e-ops --reason "end-to-end test"
eventually_call 90 "/v1/wallet/withdrawals/$SENT_ID" '.status == "CONFIRMED" and .tx_hash != null and .submitted_at != null' \
  "handed to the custodian and sent"
eventually_call 90 "/v1/wallet/withdrawals/$FAIL_ID" '.status == "FAILED" and (.reject_reason | startswith("CUSTODY_FAILED"))' \
  "the failed transfer ends FAILED"
eventually_call 60 /v1/account/balances "[.balances[] | select(.account_type == \"SPOT\" and .asset == \"USDT\")][0] | (.available | tonumber) == ($BEFORE + 30 - 13) and (.frozen | tonumber) == 0" \
  "settled: 12 + 1 fee out, the failed 10 back"
call GET "/v1/account/ledger?asset=USDT" "" "${AUTH[@]}"
check '[.items[].entry_type] | (index("DEPOSIT_CREDIT") != null and index("WITHDRAW_SETTLE") != null and index("WITHDRAW_UNFREEZE") != null)' \
  "credit, settlement and release in the fund flow"
grep -q '"status":"SUBMITTED"' "$WORK/ws.log" || { echo "FAIL no SUBMITTED push on the withdrawals channel" >&2; cat "$WORK/ws.log" >&2; exit 1; }
echo "ok   the withdrawals channel pushed SUBMITTED"

echo "== reconciliation"
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
exchangectl wallet reconcile --network UDUN | head -1
for _ in $(seq 20); do
  SHORT=$(pg "SELECT shortfall FROM wallet.chain_checks WHERE network = 'UDUN' AND asset = 'USDT' AND checked_at > '$SINCE' ORDER BY checked_at DESC LIMIT 1")
  [[ -n $SHORT ]] && break
  sleep 3
done
[[ -n $SHORT ]] || { echo "FAIL no custody check after $SINCE" >&2; exit 1; }
[[ $(jq -n "$SHORT <= 0") == true ]] || { echo "FAIL the custodian is short of $SHORT USDT" >&2; exit 1; }
echo "ok   the custodian and the ledger agree on USDT (shortfall $SHORT)"
echo "all custody checks passed"
