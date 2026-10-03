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
#     and logged; a forged one over the internet is refused too (by
#     wallet-service's allow list while it has one, by its signature once
#     the real gateway's callbacks come in from any address);
#   - USDT's withdrawals suspended by an operator refuse a request
#     (WALLET_WITHDRAW_SUSPENDED) until resumed (review B4);
#   - with an authenticator app bound, 12 USDT go to a TRON address: in
#     review, approved (exchangectl), handed to the custodian (SUBMITTED),
#     sent (CONFIRMED with its transaction) and settled; the custodian
#     charges 1.2 USDT for it, which is booked from GAS_SUPPLY (its unit on
#     TRC20 confirmed SELF; GAS_SUPPLY topped up from fee revenue when
#     low); 10 USDT to an address the custodian fails end FAILED with the
#     funds back;
#   - the gateway misbehaves (review B1, B2, ④): it takes a withdrawal but
#     its answer is lost, refuses the repeat for the balance the first
#     took, reviews it (status 0) and reports its 1.5 USDT fee as 1500: the
#     withdrawal waits UNCERTAIN with its funds frozen, is then sent and
#     settled; the fee is held for a person, who books the 1.5 charged
#     (exchangectl wallet custody-fee);
#   - a reconciliation of the custodian finds nothing missing.
# Needs wallet.withdraw on and ssh to the server; about five minutes. Runs
# only while wallet-service's UDUN custodian is the stand-in: against the
# real gateway these checks would move real money (it says SKIP).
#
#   scripts/e2e/custody.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

USDT_TRC20="195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
# Valid TRON addresses outside the platform: the custodian sends to the
# first, fails transfers to the second and misbehaves on the third.
PAYEE=TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7
FAILING=TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj
QUIRKY=TWeptS7njqhCtHdDCSFGKA1ttrs6WXxLzj

# mock ARGS... drives the mock gateway on the server.
mock() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T udun-mock /app/udun-mock $args"
}

# eventually_pg SECONDS SQL WANT WHAT polls a query until it prints WANT.
eventually_pg() {
  local secs=$1 sql=$2 want=$3 what=$4 start=$SECONDS got=""
  while ((SECONDS - start < secs)); do
    got=$(pg "$sql")
    [[ $got == "$want" ]] && { printf 'ok   %s (%ss)\n' "$what" "$((SECONDS - start))"; return 0; }
    sleep 3
  done
  printf 'FAIL %s: %s after %ss, want %s\n' "$what" "$got" "$secs" "$want" >&2
  exit 1
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

# The checks drive the stand-in and move what it holds: with the real
# gateway behind UDUN they would move real money (docs/runbook/custody.md,
# 真网关联调). Until the end to end has a custodian of its own, skip.
if [[ $(remote "sudo grep -c '^UDUN_GATEWAY_URL=http://udun-mock' apps.env || true") != 1 ]]; then
  echo "SKIP custody: wallet-service's UDUN gateway is not the stand-in udun-mock, these checks would move real money"
  exit 0
fi

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
for path in udun UDUN Udun; do
  STATUS=$(curl -s -o "$WORK/body" -w '%{http_code}' -X POST "$BASE/v1/wallet/callbacks/$path" -H 'Content-Type: application/x-www-form-urlencoded' --data "$FORM")
  # 403 from nginx's or wallet-service's allow list, 404 for a provider
  # not in lower case, 401 for the signature once any address may call
  case $STATUS in
  401 | 403 | 404) ;;
  *) echo "FAIL a forged callback over the internet to /v1/wallet/callbacks/$path: HTTP $STATUS, want it refused" >&2; exit 1 ;;
  esac
done
echo "ok   a forged callback over the internet is refused, in any case"
LOGGED=$(pg "SELECT count(*) FROM wallet.custody_callbacks WHERE result = 'REJECTED' AND received_at > now() - interval '5 minutes'")
((LOGGED >= 2)) || { echo "FAIL $LOGGED refused callbacks logged, want 2" >&2; exit 1; }
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
# The mock counts its fees in the coin itself, as the documentation reads:
# confirmed for TRC20 (review ④), its fees are booked from GAS_SUPPLY,
# which fee revenue keeps supplied.
exchangectl wallet custody-fee-unit --asset USDT --network TRON --unit SELF --reason "end-to-end: the mock gateway charges in the coin" >/dev/null
GAS=$(pg "SELECT COALESCE(sum(available), 0) FROM ledger.accounts WHERE account_type = 'GAS_SUPPLY' AND asset = 'USDT'")
if [[ $(jq -n "$GAS < 10") == true ]]; then
  exchangectl ledger gas-supply --asset USDT --amount 20 --reason "end-to-end: the custodian's fees" >/dev/null
  echo "     GAS_SUPPLY had $GAS USDT: 20 more from fee revenue"
fi
fee_row() { # fee_row WITHDRAWAL_ID prints status|amount|asset|booked
  pg "SELECT status, trim_scale(amount), asset, booked_at IS NOT NULL FROM wallet.chain_fees WHERE reference = '$1'"
}
eventually_booked() { # eventually_booked WITHDRAWAL_ID AMOUNT WHAT
  eventually_pg 60 "SELECT status, trim_scale(amount), asset, booked_at IS NOT NULL FROM wallet.chain_fees WHERE reference = '$1'" \
    "BOOKABLE|$2|USDT|t" "$3"
}
mock outcome --address "$PAYEE" --status 3 --fee 1200000 --charge 1200000 >/dev/null
mock outcome --address "$FAILING" --status 4 >/dev/null
for to in "$PAYEE" "$FAILING" "$QUIRKY"; do
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
# An asset's withdrawals suspended (review B4; the custody check does it
# on funds missing twice, here an operator): refused, then resumed.
exchangectl wallet withdrawals-suspend --asset USDT --reason "end-to-end: refused while suspended" >/dev/null
at_exit "exchangectl wallet withdrawals-resume --asset USDT --reason 'end-to-end: cleanup' >/dev/null 2>&1"
step_up
call POST /v1/wallet/withdrawals "{\"asset\":\"USDT\",\"network\":\"TRON\",\"address\":\"$PAYEE\",\"amount\":\"12\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP"
expect 422 WALLET_WITHDRAW_SUSPENDED "refused while USDT's withdrawals are suspended"
call GET "/v1/wallet/networks?asset=USDT" "" "${AUTH[@]}"
expect 200 - "networks while suspended"
check '[.networks[] | select(.network == "TRON")][0] | .withdraw_suspended == true and .withdraw_enabled == false' "the sites see USDT's withdrawals suspended"
exchangectl wallet withdrawals-resume --asset USDT --reason "end-to-end: resumed" >/dev/null
echo "ok   resumed by an operator"
call GET "/v1/wallet/networks?asset=USDT" "" "${AUTH[@]}"
check '[.networks[] | select(.network == "TRON")][0] | .withdraw_suspended == false and .withdraw_enabled == true' "and open again"
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
eventually_booked "$SENT_ID" 1.2 "the custodian's 1.2 USDT for sending it booked from GAS_SUPPLY"

echo "== a gateway that loses an answer and reports its fee in another unit"
mock outcome --address "$QUIRKY" --status 3 --lose-answer --repeat-code 4001 --review --fee 1500000000 --charge 1500000 >/dev/null
withdraw "$QUIRKY" 11
QUIRK_ID=$(jq -r .id <<<"$BODY")
exchangectl wallet approve "$QUIRK_ID" --reviewer e2e-ops --reason "end-to-end test"
eventually_call 60 "/v1/wallet/withdrawals/$QUIRK_ID" '.status == "SUBMITTED"' "handed over, its answer lost"
# Hold back the callbacks the repeat (a minute later) releases.
mock delay --seconds 45 >/dev/null
eventually_pg 120 "SELECT status, provider_status, reject_reason LIKE 'UNCERTAIN%' FROM wallet.withdrawals WHERE id = '$QUIRK_ID'" \
  "SUBMITTED|UNCERTAIN|t" "the repeat refused for the balance: UNCERTAIN, nothing released"
call GET "/v1/wallet/withdrawals/$QUIRK_ID" "" "${AUTH[@]}"
check '.status == "SUBMITTED" and .reject_reason == null' "its user sees it with the custodian, not the custodian's words"
mock delay --seconds 0 >/dev/null
call GET /v1/account/balances "" "${AUTH[@]}"
check '[.balances[] | select(.account_type == "SPOT" and .asset == "USDT")][0] | (.frozen | tonumber) == 12' "the 11 and the fee stay frozen"
eventually_call 90 "/v1/wallet/withdrawals/$QUIRK_ID" '.status == "CONFIRMED" and .tx_hash != null' "the gateway's callbacks send it after all"
DETAIL=$(pg "SELECT detail FROM wallet.custody_callbacks WHERE business_id = '$QUIRK_ID' AND status = 3 ORDER BY received_at DESC LIMIT 1")
[[ $DETAIL == *"held for a person"* && $(fee_row "$QUIRK_ID") == "HELD|1500|USDT|f" ]] ||
  { echo "FAIL the fee reported as 1500: $DETAIL, $(fee_row "$QUIRK_ID")" >&2; exit 1; }
[[ $(exchangectl wallet custody-fees) == *"$QUIRK_ID"*"above 5 USDT"* ]] || { echo "FAIL the held fee is not listed" >&2; exit 1; }
echo "ok   the fee reported as 1500 USDT is held for a person, not booked"
exchangectl wallet custody-fee "$QUIRK_ID" --book --amount 1.5 --reason "end-to-end: the mock took 1.5" >/dev/null
eventually_booked "$QUIRK_ID" 1.5 "booked as the person found it charged, 1.5 USDT"
REVIEWED=$(pg "SELECT count(*) FROM wallet.custody_callbacks WHERE business_id = '$QUIRK_ID' AND status = 0")
((REVIEWED == 1)) || { echo "FAIL $REVIEWED review callbacks, want 1" >&2; exit 1; }
echo "ok   the gateway's review (status 0) is taken"
eventually_call 60 /v1/account/balances "[.balances[] | select(.account_type == \"SPOT\" and .asset == \"USDT\")][0] | (.available | tonumber) == ($BEFORE + 30 - 25) and (.frozen | tonumber) == 0" \
  "settled: 11 and the fee out"
mock outcome --address "$QUIRKY" --status 3 >/dev/null
mock outcome --address "$PAYEE" --status 3 >/dev/null

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
