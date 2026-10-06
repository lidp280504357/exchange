#!/usr/bin/env bash
# Fault injection: a margin account liquidated by the market (margin design
# 2026-10-06 §4.4, §4.5, batch E3). A new user puts 100 USDT on the cross
# account and buys about 210 USDT of ASTRA with AUTO_BORROW (margin level
# about 1.33, ASTRA counting at 0.7); an operator's price event drops
# ASTRA 20%: the monitor warns the account (MARGIN_WARNED in its inbox),
# finds it at its liquidation level (1.10) twice in a row and, with
# margin.liquidation on for this user (unless on for everyone),
# liquidates it: as much ASTRA sold against the simulated market as the
# loan and the 2% fee need (review DD C19), the fee to the insurance fund
# and nothing out of it, the loan owed before the drop repaid, the
# account NORMAL again. Then the price goes back up by the exact inverse
# of the drop.
#
# The drop is the platform's, not only this user's (review DH C20 3):
# every margin account holding ASTRA with a debt may be warned (and
# mailed), and a position on ASTRA-USDT-PERP may be liquidated or
# deleveraged, which the jump back does not undo. The drill skips while a
# user other than the simulated market's bots holds a position there, and
# says how many other margin accounts it may warn. It takes about 45% of
# one operator's 50% within the hour (20% down, about 25% back): the
# price moves of scripts/e2e/astra.sh skip for an hour after it, as this
# drill skips for an hour after any price event or settings change.
#
# Needs the simulated market running with its price events
# (scripts/ops/astra.sh on, events-on); about five minutes. The switches
# and the price go back as they were, also after a failure; one that
# cannot go back fails the run and says how to put it back by hand.
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
  SIM_STATUS=$(grep -oE '^HTTP [0-9]+' <<<"$out" | tail -1 | cut -d' ' -f2 || true)
  SIM_BODY=$(sed -n '/^{/,/^}/p' <<<"$out" || true)
}
simget() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- 'http://127.0.0.1:8098$1'"; }
# back_by PRICE: the jump that takes the target to PRICE.
back_by() { jq -rn --argjson to "$1" --argjson from "$(simget /internal/sim | jq -r .target_price)" '($to / $from - 1) * 10000 | round / 10000'; }

status() { call GET "/v1/market/pairs/$SYMBOL" "" && jq -r .status <<<"$BODY"; }
SIM=$(simget /internal/sim)
if [[ $(status) != TRADING || $(jq -r .running <<<"$SIM") != true || $(pg "SELECT enabled FROM config.flags WHERE key = 'sim.events'") != t ]]; then
  echo "skip: $SYMBOL is not trading, the simulated market is not running, or its price events are off (scripts/ops/astra.sh on, events-on)"
  exit 0
fi
RECENT=$(pg "SELECT (SELECT count(*) FROM marketsim.events WHERE status <> 'CANCELED' AND type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY') AND starts_at BETWEEN now() - interval '1 hour' AND now() + interval '1 hour') + (SELECT count(*) FROM marketsim.param_changes WHERE at > now() - interval '1 hour' AND (move <> 0 OR volume <> 0))")
if ((RECENT > 0)); then
  echo "skip: $RECENT price events or settings changes within the hour take one operator's room; run it an hour after them"
  exit 0
fi
# others_on_perp: positions on ASTRA-USDT-PERP of users other than the
# simulated market's bots, which a 20% drop could liquidate.
others_on_perp() {
  pg "SELECT count(*) FROM derivatives.positions WHERE symbol = 'ASTRA-USDT-PERP' AND quantity <> 0 AND user_id NOT IN (SELECT user_id FROM marketsim.bots)"
}
if (($(others_on_perp) > 0)); then
  echo "skip: $(others_on_perp) positions on ASTRA-USDT-PERP of users other than the bots; the drop could liquidate them"
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
# put_back KEY ENABLED USERS: a switch that cannot go back fails the run.
put_back() {
  local state=--off
  [[ $2 == true ]] && state=--on
  if ! exchangectl flags set "$1" "$state" --allow-users "$3" --reason "fault margin-liquidation.sh: back as it was" >/dev/null; then
    echo "WARN could not put $1 back (enabled $2, allowed users '$3'); by hand: exchangectl flags set $1 $state --allow-users '$3' --reason ..." >&2
    EXIT_FAILED=1
  fi
}
# undo SIZE: the jump that undoes a jump of SIZE, exactly (1 / (1 + SIZE)
# - 1, review DK C22 3): the drift of the model since does not count
# against one operator's room.
undo() { jq -rn --argjson s "$1" '(1 / (1 + $s) - 1) * 10000 | round / 10000'; }
# back_up SIZE: the drop of SIZE undone as the drill ends (review DH C20
# 1); a jump refused or lost (the budget, someone's target, market-sim
# down) leaves ASTRA lower for everyone: it fails the run and says how to
# put it back.
back_up() {
  local body
  body="{\"type\":\"JUMP\",\"size\":$(undo "$1"),\"actor\":\"fault-ops\",\"reason\":\"fault: back after the liquidation\"}"
  simpost /internal/sim/events "$body"
  if [[ $SIM_STATUS != 201 ]]; then
    echo "WARN ASTRA's target is not back up (HTTP ${SIM_STATUS:-none} $SIM_BODY); by hand, on the server in /opt/exchange/infra:" >&2
    echo "     sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST /internal/sim/events '$body'" >&2
    EXIT_FAILED=1
    return
  fi
  echo "ok   ASTRA's target back up by $(undo "$1") (the drop of $1 undone)"
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
on_for_user margin.enabled "$USER_ID"
on_for_user margin.auto_borrow "$USER_ID"
on_for_user margin.liquidation "$USER_ID"
sleep 6

# unwind: the ASTRA sold back with AUTO_REPAY and what is still owed
# repaid: a drill that skips after its buy leaves no loan (review DK C22
# 2).
unwind() {
  local qty bid
  call GET /v1/margin/accounts "" "${AUTH[@]}"
  qty=$(jq -r '[.cross.balances[] | select(.asset == "ASTRA")][0].free // "0"' <<<"$BODY")
  if [[ $qty != 0 ]] && book; then
    bid=$(jq -r '.bids[0][0] | tonumber * 0.99 * 10000 | floor / 10000' <<<"$BODY")
    call POST /v1/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$bid\",\"quantity\":\"$(jq -rn --argjson q "$qty" '$q | floor')\",\"account\":\"MARGIN_CROSS\",\"side_effect\":\"AUTO_REPAY\"}" "${AUTH[@]}" -H "Idempotency-Key: fault-margin-$RUN-unwind"
    for _ in $(seq 30); do # the sale repays as it settles
      call GET /v1/margin/loans "" "${AUTH[@]}"
      [[ $(jq '.items | length' <<<"$BODY") == 0 ]] && break
      sleep 1
    done
  fi
  call POST /v1/margin/repay '{"account":"MARGIN_CROSS","asset":"USDT","amount":"ALL"}' "${AUTH[@]}" -H "Idempotency-Key: fault-margin-$RUN-unwind-repay"
  call GET /v1/margin/loans "" "${AUTH[@]}"
  if [[ $(jq '.items | length' <<<"$BODY") != 0 ]]; then
    echo "WARN the drill's user $USER_ID still owes: $BODY" >&2
    EXIT_FAILED=1
  fi
}

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
if (($(others_on_perp) > 0)); then
  echo "skip: a position on ASTRA-USDT-PERP of a user other than the bots opened meanwhile; the drop could liquidate it"
  exit 0
fi
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
call GET /v1/margin/loans "" "${AUTH[@]}"
OWED=$(jq -r '[.items[] | select(.asset == "USDT")][0] | (.principal | tonumber) + (.interest | tonumber)' <<<"$BODY")
echo "     owed before the drop: $OWED USDT"

echo "== an operator's event drops ASTRA 20%"
if (($(others_on_perp) > 0)); then
  echo "skip: a position on ASTRA-USDT-PERP of a user other than the bots opened meanwhile; the drop could liquidate it"
  unwind
  exit 0
fi
WARNABLE=$(pg "SELECT count(*) FROM ledger.accounts a WHERE a.owner_type = 'USER' AND a.account_type IN ('MARGIN_CROSS', 'MARGIN_ISOLATED') AND a.asset = 'ASTRA' AND a.available + a.frozen > 0 AND a.owner_id::text <> '$USER_ID' AND EXISTS (SELECT 1 FROM ledger.accounts d WHERE d.owner_type = 'USER' AND d.owner_id = a.owner_id AND d.scope = a.scope AND d.account_type IN (a.account_type || '_DEBT', a.account_type || '_INTEREST') AND d.available <> 0)")
echo "note: $WARNABLE other margin accounts hold ASTRA with a debt; the drop may warn (and mail) them"
FROM=$(simget /internal/sim | jq -r .target_price)
DOWN=$(jq -rn --argjson p "$FROM" '$p * 0.8 * 10000 | floor / 10000 | tostring')
DROP=$(back_by "$DOWN")
simpost /internal/sim/events "{\"type\":\"JUMP\",\"size\":$DROP,\"actor\":\"fault-ops\",\"reason\":\"fault: liquidate a margin account\"}"
[[ $SIM_STATUS == 201 ]] || { echo "FAIL the jump: HTTP $SIM_STATUS $SIM_BODY" >&2; exit 1; }
# shellcheck disable=SC2016 # expanded when the drill ends
at_exit 'back_up "$DROP"'
echo "ok   the target goes from $FROM to $DOWN (a jump of $DROP)"
has_notice() {
  call GET /v1/notifications "" "${AUTH[@]}" && [[ $STATUS == 200 && $(jq --arg t "$1" '[.items[] | select(.type == $t)] | length' <<<"$BODY") -ge 1 ]]
}
eventually 240 "the account warned: MARGIN_WARNED in its inbox (review DH C20 4)" has_notice MARGIN_WARNED
# settled: the liquidation COMPLETED, or stopped where the drill fails at
# once (STUCK): SHORTFALL, or its latest repayment refused and the step
# not moved on for a minute (a refused repayment is made again under a new
# key, review DD C19 3: one alone is no failure, review DK C22 1).
STUCK=""
settled() {
  local st refused
  st=$(pg "SELECT status FROM margin.liquidations WHERE user_id = '$USER_ID' ORDER BY started_at DESC LIMIT 1")
  if [[ $st == SHORTFALL ]]; then
    STUCK="the liquidation waits for the insurance fund (SHORTFALL)"
  elif [[ -n $st && $st != COMPLETED ]]; then
    refused=$(pg "SELECT r.failure FROM margin.liquidations l CROSS JOIN LATERAL (SELECT status, failure FROM margin.repays WHERE liquidation_id = l.liquidation_id ORDER BY created_at DESC LIMIT 1) r WHERE l.user_id = '$USER_ID' AND l.status <> 'COMPLETED' AND r.status = 'FAILED' AND l.step_at < now() - interval '60 seconds'")
    [[ -z $refused ]] || STUCK="the ledger keeps refusing the liquidation's repayment ($refused)"
  fi
  [[ -n $STUCK || $st == COMPLETED ]]
}
eventually 300 "the liquidation over" settled
[[ -z $STUCK ]] || { echo "FAIL $STUCK" >&2; exit 1; }
call GET /v1/margin/liquidations "" "${AUTH[@]}"
check '.items[0].status == "COMPLETED" and .items[0].account == "MARGIN_CROSS" and (.items[0].margin_level | tonumber) <= 1.1 and .items[0].repaid[0].asset == "USDT" and (.items[0].fee | tonumber) > 0 and .items[0].insurance_covered == "0"' "at or under 1.10, the ASTRA sold, the loan repaid, the fee charged, nothing from the insurance fund"
check "(.items[0].repaid[0].amount | tonumber) >= $OWED and (.items[0].repaid[0].amount | tonumber) - $OWED < 0.01" "the $OWED USDT owed before the drop repaid (an hour's interest at most more; review DH C20 5)"
LIQ=$(jq -r .items[0].liquidation_id <<<"$BODY")
FEE=$(jq -r .items[0].fee <<<"$BODY")
[[ $(pg "SELECT trigger FROM margin.liquidations WHERE liquidation_id = '$LIQ'") == AUTO ]] ||
  { echo "FAIL the liquidation was not the monitor's" >&2; exit 1; }
echo "ok   the monitor's (AUTO)"
# What the account's postings moved into and out of the insurance fund:
# the fee, exactly (review DH C20 2).
TO_FUND=$(pg "SELECT coalesce(sum(jl.amount), 0) FROM ledger.margin_postings mp CROSS JOIN LATERAL jsonb_array_elements_text(mp.journals) j(id) JOIN ledger.journal_lines jl ON jl.journal_id = NULLIF(j.id, '')::uuid JOIN ledger.accounts a ON a.id = jl.account_id WHERE mp.user_id = '$USER_ID' AND a.account_type = 'INSURANCE_FUND' AND a.asset = 'USDT'")
jq -en --argjson got "$TO_FUND" --argjson fee "$FEE" '$got == $fee' >/dev/null ||
  { echo "FAIL the insurance fund got $TO_FUND USDT from the account, not the fee $FEE" >&2; exit 1; }
echo "ok   the insurance fund got the fee ($FEE USDT) and paid nothing"
call GET /v1/margin/loans "" "${AUTH[@]}"
check '.items == []' "nothing owed"
call GET /v1/margin/accounts "" "${AUTH[@]}"
check '.cross.status == "NORMAL"' "the account NORMAL again"
echo "     left to it: $(jq -r '[.cross.balances[] | select((.free | tonumber) > 0) | "\(.free) \(.asset)"] | join(", ")' <<<"$BODY") (only what the debt and the fee needed was sold)"
eventually 60 "MARGIN_LIQUIDATED in its inbox" has_notice MARGIN_LIQUIDATED

echo "== the invariants"
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl ledger reconcile" | grep -E "MARGIN_" | sed 's/^/     /'
remote "sudo docker compose $COMPOSE_FILES exec -T margin-service /app/exchangectl margin reconcile" | tail -1 | sed 's/^/     /'
echo "margin liquidation drill passed (the price goes back as the drill ends)"
