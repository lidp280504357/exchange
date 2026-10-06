#!/usr/bin/env bash
# Fault injection: a margin account liquidated by the market (margin design
# 2026-10-06 §4.4, §4.5, batch E3). A new user puts 100 USDT on the cross
# account and buys about 210 USDT of ASTRA with AUTO_BORROW (margin level
# about 1.34, ASTRA counting at 0.7); an operator's price event drops
# ASTRA 20%: the monitor warns the account (WARNED, MarginLevelWarned),
# finds it at its liquidation level (1.10) twice in a row and, with
# margin.liquidation on for this user only, liquidates it: its ASTRA sold
# against the simulated market, the 2% fee to the insurance fund, the loan
# repaid, the account NORMAL again. Then the price goes back. Needs the
# simulated market's bots and price events (scripts/ops/astra.sh on,
# events-on) and one operator's room within the hour: 20% down and 25%
# back of the 50% (it skips otherwise, as scripts/e2e/astra.sh does);
# about five minutes; the switch goes back as it was, also after a
# failure.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"

SYMBOL=ASTRA-USDT
# simpost PATH JSON posts to market-sim's management API with the operators'
# key (exchangectl in its container): SIM_STATUS, SIM_BODY.
simpost() {
  local out
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST $1 $(printf %q "$2") 2>&1" || true)
  SIM_STATUS=$(grep -oE '^HTTP [0-9]+' <<<"$out" | tail -1 | cut -d' ' -f2)
  SIM_BODY=$(sed -n '/^{/,/^}/p' <<<"$out")
}
simget() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- 'http://127.0.0.1:8098$1'"; }
# back_by PRICE: the jump that takes the target to PRICE.
back_by() { jq -rn --argjson to "$1" --argjson from "$(simget /internal/sim | jq -r .target_price)" '($to / $from - 1) * 10000 | round / 10000'; }

status() { call GET "/v1/market/pairs/$SYMBOL" "" && jq -r .status <<<"$BODY"; }
if [[ $(status) != TRADING || $(pg "SELECT enabled FROM config.flags WHERE key = 'sim.events'") != t ||
  $(pg "SELECT enabled FROM config.flags WHERE key = 'margin.enabled'") != t ]]; then
  echo "skip: $SYMBOL is not trading, the price events are off, or margin trading is off"
  exit 0
fi
RECENT=$(pg "SELECT (SELECT count(*) FROM marketsim.events WHERE status <> 'CANCELED' AND type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY') AND starts_at BETWEEN now() - interval '1 hour' AND now() + interval '1 hour') + (SELECT count(*) FROM marketsim.param_changes WHERE at > now() - interval '1 hour' AND (move <> 0 OR volume <> 0))")
if ((RECENT > 0)); then
  echo "skip: $RECENT price events or settings changes within the hour take one operator's room; run it an hour after them"
  exit 0
fi

# on_for_user KEY USER_ID: KEY on for the user only (unless on for
# everyone), put back as it was when the drill ends. Only "not set" reads
# as off: any other failure stops before anything changes.
on_for_user() {
  local key=$1 user=$2 state enabled users
  if ! state=$(exchangectl flags show "$key" 2>&1); then
    if [[ $state == *"flag $key is not set"* ]]; then
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
  exchangectl flags set "$key" --on --allow-users "${users:+$users,}$user" --reason "fault margin-liquidation.sh: on for its user $user only" >/dev/null
  echo "note: $key on for $user only until the drill ends"
}
put_back() { # put_back KEY ENABLED USERS
  local state=--off
  [[ $2 == true ]] && state=--on
  exchangectl flags set "$1" "$state" --allow-users "$3" --reason "fault margin-liquidation.sh: back as it was" >/dev/null ||
    echo "WARN could not put $1 back (enabled $2, allowed users '$3'): exchangectl flags set $1 $state --allow-users '$3' --reason ..." >&2
}

EMAIL="fault-margin-$RUN@example.com"
echo "== register $EMAIL"
register "$EMAIL" "fault-margin-$RUN" "fault margin $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
USER_ID=$(jq -r .user_id <<<"$BODY")
funded() {
  call GET /v1/account/balances "" "${AUTH[@]}" &&
    [[ $(jq -r '[.balances[] | select(.asset == "USDT" and .account_type == "SPOT")][0].available // "0"' <<<"$BODY") != 0 ]]
}
eventually 40 "welcome funds arrived" funded
on_for_user margin.auto_borrow "$USER_ID"
on_for_user margin.liquidation "$USER_ID"
sleep 6

echo "== 100 USDT on the cross account, about 210 USDT of ASTRA bought with AUTO_BORROW"
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"100"}' "${AUTH[@]}" -H "Idempotency-Key: fault-margin-$RUN-in"
expect 200 - "100 USDT into the cross account"
book() {
  call GET "/v1/market/$SYMBOL/depth?limit=5" "" && [[ $STATUS == 200 ]] &&
    jq -e '(.bids | length) > 0 and (.asks | length) > 0' <<<"$BODY" >/dev/null
}
eventually 60 "$SYMBOL shows a two-sided book" book
ASK=$(jq -r '.asks[0][0]' <<<"$BODY")
QTY=$(jq -rn --argjson ask "$ASK" '210 / $ask | floor')
PRICE=$(jq -rn --argjson ask "$ASK" '$ask * 1.01 * 10000 | floor / 10000')
call POST /v1/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"$QTY\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_BORROW\"}" "${AUTH[@]}" -H "Idempotency-Key: fault-margin-$RUN-buy"
expect 202 - "a buy of $QTY ASTRA at $PRICE on the cross account, AUTO_BORROW"
ORDER=$(jq -r .order_id <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the drill ends
at_exit 'call DELETE "/v1/orders?symbol=$SYMBOL" "" "${AUTH[@]}"'
filled() { call GET "/v1/orders/$ORDER" "" "${AUTH[@]}" && [[ $(jq -r .status <<<"$BODY") == FILLED ]]; }
eventually 60 "the buy is FILLED" filled
call GET /v1/margin/accounts "" "${AUTH[@]}"
check '(.cross.margin_level | tonumber) > 1.3 and (.cross.total_liability | tonumber) > 0' "ASTRA on the cross account, a USDT loan, the level above 1.3"
echo "     margin level $(jq -r .cross.margin_level <<<"$BODY")"

echo "== an operator's event drops ASTRA 20%"
FROM=$(simget /internal/sim | jq -r .target_price)
DOWN=$(jq -rn --argjson p "$FROM" '$p * 0.8 * 10000 | floor / 10000 | tostring')
simpost /internal/sim/events "{\"type\":\"JUMP\",\"size\":$(back_by "$DOWN"),\"actor\":\"fault-ops\",\"reason\":\"fault: liquidate a margin account\"}"
[[ $SIM_STATUS == 201 ]] || { echo "FAIL the jump: HTTP $SIM_STATUS $SIM_BODY" >&2; exit 1; }
# shellcheck disable=SC2016 # expanded when the drill ends
at_exit 'simpost /internal/sim/events "{\"type\":\"JUMP\",\"size\":$(back_by "$FROM"),\"actor\":\"fault-ops\",\"reason\":\"fault: back after the liquidation\"}"'
echo "ok   the target goes from $FROM to $DOWN"
liquidated() {
  call GET /v1/margin/liquidations "" "${AUTH[@]}" && [[ $STATUS == 200 ]] && jq -e '.items[0].status == "COMPLETED"' <<<"$BODY" >/dev/null
}
eventually 300 "the monitor liquidated the account" liquidated
check '.items[0].account == "MARGIN_CROSS" and (.items[0].margin_level | tonumber) <= 1.1 and .items[0].repaid[0].asset == "USDT" and (.items[0].fee | tonumber) > 0' "at or under 1.10, the ASTRA sold, the loan repaid, the fee charged"
LIQ=$(jq -r .items[0].liquidation_id <<<"$BODY")
[[ $(pg "SELECT trigger FROM margin.liquidations WHERE liquidation_id = '$LIQ'") == AUTO ]] ||
  { echo "FAIL the liquidation was not the monitor's" >&2; exit 1; }
echo "ok   the monitor's (AUTO)"
call GET /v1/margin/loans "" "${AUTH[@]}"
check '.items == []' "nothing owed"
call GET /v1/margin/accounts "" "${AUTH[@]}"
check '.cross.status == "NORMAL"' "the account NORMAL again"

echo "== the invariants"
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | grep -E "MARGIN_" | sed 's/^/     /'
remote "sudo docker compose $COMPOSE_FILES exec -T margin-service /app/exchangectl margin reconcile" | tail -1 | sed 's/^/     /'
echo "margin liquidation drill passed (the price goes back as the drill ends)"
