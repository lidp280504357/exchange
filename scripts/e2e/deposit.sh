#!/usr/bin/env bash
# Deposits end to end on Sepolia (implementation plan §6.3 task 9): a new
# user gets a deposit address (the same one when asking again); the funded
# end-to-end sender (E2E_SEPOLIA_SENDER_KEY in .env, see
# docs/runbook/wallet.md) sends 0.0012 ETH and 0.0002 ETH to it. The first
# goes DETECTED -> CONFIRMING -> CREDITED after 12 confirmations and shows
# in the balance, the fund flow and a notice; the second is below the
# 0.001 minimum and ends REJECTED (booked to UNCLAIMED_DEPOSIT), leaving
# the balance alone. The "deposits" and "balances" WebSocket channels push
# the changes. Takes about five minutes (12-second blocks, a scan every
# 30 seconds) and 0.0014 ETH plus gas; with less than 0.003 ETH left the
# sender is not used and only the address checks run.
#
#   scripts/e2e/deposit.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

EMAIL="e2e-deposit-$RUN@example.com"
DEVICE="e2e-deposit-$RUN"
echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "e2e deposit $RUN"
TOKEN=$(jq -r .access_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $TOKEN")

echo "== deposit address"
call GET "/v1/wallet/deposit-address?asset=ETH&network=ETH-SEPOLIA" "" "${AUTH[@]}"
expect 200 - "the user's ETH-SEPOLIA deposit address"
ADDR=$(jq -r .address <<<"$BODY")
check '(.address | test("^0x[0-9a-fA-F]{40}$")) and .min_deposit == "0.001" and .confirmations == 12 and .contract == null' \
  "an address with the network's minimum and confirmations"
call GET "/v1/wallet/deposit-address?asset=eth&network=eth-sepolia" "" "${AUTH[@]}"
expect 200 - "asked again"
check ".address == \"$ADDR\"" "the same address again"
call GET "/v1/wallet/deposit-address?asset=BTC&network=ETH-SEPOLIA" "" "${AUTH[@]}"
expect 404 WALLET_NETWORK_UNKNOWN "an asset without that network"
call GET "/v1/wallet/deposit-address?asset=ETH" "" "${AUTH[@]}"
expect 400 COMMON_INVALID_ARGUMENT "the network is required"
call GET /v1/wallet/deposits "" "${AUTH[@]}"
expect 200 - "the deposit history"
check '.items == [] and .next_cursor == null' "no deposits yet"

go build -o "$WORK/sendeth" ./scripts/e2e/sendeth
read -r SENDER FUNDS < <("$WORK/sendeth" -balance)
if [[ $(jq -n "$FUNDS < 0.003") == true ]]; then
  echo "SKIP the Sepolia transfers: the sender $SENDER has $FUNDS ETH; fund it (docs/runbook/wallet.md)"
  exit 0
fi

echo "== send 0.0012 and 0.0002 ETH on Sepolia (sender $SENDER has $FUNDS ETH)"
node "$(dirname "$0")/lib/deposit-watch.mjs" "$BASE" "$TOKEN" >"$WORK/ws.log" 2>&1 &
at_exit "kill $! 2>/dev/null"
disown # no job-control notice when it is killed at the end
TX1=$("$WORK/sendeth" -to "$ADDR" -amount 0.0012 -wait)
TX2=$("$WORK/sendeth" -to "$ADDR" -amount 0.0002 -wait)
printf 'ok   sent %s and %s\n' "$TX1" "$TX2"
grep -q '"subscribed":true' "$WORK/ws.log" || { echo "FAIL the WebSocket watcher did not subscribe" >&2; cat "$WORK/ws.log" >&2; exit 1; }

# wait_deposits SECONDS WHAT CONDITION polls the deposit list every 5 s
# until the jq CONDITION holds.
wait_deposits() {
  local secs=$1 what=$2 cond=$3 start=$SECONDS
  while ((SECONDS - start < secs)); do
    if call GET /v1/wallet/deposits "" "${AUTH[@]}" && [[ $STATUS == 200 && $(jq -r "$cond" <<<"$BODY") == true ]]; then
      printf 'ok   %s (%ss)\n' "$what" "$((SECONDS - start))"
      return 0
    fi
    sleep 5
  done
  printf 'FAIL %s: %s does not hold after %ss for\n%s\n' "$what" "$cond" "$secs" "$BODY" >&2
  exit 1
}
one() { printf '(.items[] | select(.tx_hash == "%s"))' "$1"; }

wait_deposits 150 "both transfers detected" "[.items[] | select(.tx_hash == \"$TX1\" or .tx_hash == \"$TX2\")] | length == 2"
check "$(one "$TX1") | .asset == \"ETH\" and .amount == \"0.0012\" and .raw_amount == \"1200000000000000\" and .log_index == -1
  and .unclaimed == false and .reason == null and .address == \"$ADDR\" and .required_confirmations == 12" \
  "0.0012 ETH detected for the account"
check "$(one "$TX2") | .unclaimed == true and .reason == \"BELOW_MINIMUM\" and .status != \"CREDITED\"" \
  "0.0002 ETH detected as below the minimum"

echo "== wait for 12 confirmations"
wait_deposits 480 "credited after 12 confirmations" \
  "($(one "$TX1") | .status == \"CREDITED\" and .confirmations == 12 and .credited_at != null) and ($(one "$TX2") | .status == \"REJECTED\")"

call GET /v1/account/balances "" "${AUTH[@]}"
expect 200 - "balances"
check '[.balances[] | select(.account_type == "SPOT" and .asset == "ETH")][0].available | tonumber == 2.0012' \
  "spot ETH is the 2 ETH welcome funds plus 0.0012 (the small deposit is not credited)"
call GET "/v1/account/ledger?asset=ETH&type=DEPOSIT_CREDIT" "" "${AUTH[@]}"
expect 200 - "the fund flow"
check '[.items[] | select(.entry_type == "DEPOSIT_CREDIT")] | length == 1 and (.[0].amount | tonumber) == 0.0012' \
  "one DEPOSIT_CREDIT of 0.0012 ETH"

sleep 3
call GET /v1/notifications "" "${AUTH[@]}"
expect 200 - "notices"
check '([.items[].type] | index("DEPOSIT_CREDITED") != null) and ([.items[].type] | index("DEPOSIT_UNCLAIMED") != null)' \
  "a notice for the credited deposit and one for the unclaimed deposit"

pushes() { jq -rs "$1" "$WORK/ws.log"; }
[[ $(pushes "[.[] | select(.channel == \"deposits\" and .data.tx_hash == \"$TX1\") | .data.status] | index(\"CREDITED\") != null") == true ]] ||
  { echo "FAIL no CREDITED push on the deposits channel" >&2; cat "$WORK/ws.log" >&2; exit 1; }
[[ $(pushes '[.[] | select(.channel == "balances" and .data.entry_type == "DEPOSIT_CREDIT" and .data.asset == "ETH")] | length == 1') == true ]] ||
  { echo "FAIL no DEPOSIT_CREDIT push on the balances channel" >&2; cat "$WORK/ws.log" >&2; exit 1; }
echo "ok   the deposits and balances channels pushed the deposit"
