#!/usr/bin/env bash
# Fault injection: coin-margined positions liquidated by the market
# (coin-margined design 2026-10-06 §2.2, §2.3; review EX: a condition
# before launch). On ASTRA-USD-PERP, settled in ASTRA and made by the
# simulated market's bots (HOUSE does not quote it), four new users buy
# ASTRA and open positions: isolated, A long at 25x, C long at 15x, B short
# at 20x; cross, D long at 25x on an account that holds only what puts its
# liquidation about 3.85% under the mark (review C68).
# 1. The fund's path: an operator's price event drops ASTRA 4%. As the
#    mark follows, the monitor takes A's position over and its IOC
#    liquidation orders sell into the bots' bids: A is flat, its closing
#    fills are liquidation fills, and ASTRA's insurance fund moved by
#    exactly what those fills say it paid (nothing when they filled above
#    the bankruptcy price).
#    D's cross account goes the same way, late in the drop (or in the
#    second one, deleveraged at the mark): checked after step 2.
# 2. ADL: the bots stop making the perpetual (sim.perp without it) and a
#    second 4% drop reaches C's liquidation price: three IOC orders find
#    no bid and C's position is closed against the best-ranked short (B,
#    unless a bot ranks higher) at C's bankruptcy price on the tick grid,
#    without fees.
# 3. The clearance fee (review C68): D is flat, its cross liquidation DONE
#    with the fee min(what it left, its equity at the take-over), that fee
#    in D's ledger as INSURANCE_CONTRIBUTION (none when nothing was left),
#    D's ASTRA FUTURES account at zero and D told (CONTRACT_LIQUIDATED).
# The price goes back up by the exact inverse of both drops, the bots make
# the perpetual again and B's position is closed; the reconciliation
# (invariant 6 per settlement asset, ASTRA's included) passes.
#
# The drops are the platform's: every position on the platform coin's
# perpetuals and margin account holding ASTRA with a debt feels them. The
# drill skips while a user other than the bots holds a position on either
# perpetual, and says how many margin accounts it may warn. It takes about
# 17% of one operator's 50% within the hour (4% and 4% down, 8.5% back)
# and, like margin-liquidation.sh, skips for an hour after any price event
# or settings change: one of the two runs in a `task fault`, the other
# skips.
#
# Needs ASTRA-USD-PERP TRADING with the bots on it (scripts/ops/astra.sh
# perp-open ASTRA-USD-PERP, perp-on ASTRA-USDT-PERP ASTRA-USD-PERP) and the
# simulated market's price events (astra.sh events-on); about ten minutes.
# The switch and the price go back as they were, also after a failure; one
# that cannot go back fails the run and says how to put it back by hand.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"

SYMBOL=ASTRA-USD-PERP
SPOT=ASTRA-USDT
fail() { echo "FAIL $1" >&2; exit 1; }
# simpost PATH JSON posts to market-sim's management API with the operators'
# key (exchangectl in its container): SIM_STATUS, SIM_BODY.
simpost() {
  local out
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST $1 $(printf %q "$2") 2>&1" || true)
  SIM_STATUS=$(grep -oE '^HTTP [0-9]+' <<<"$out" | tail -1 | cut -d' ' -f2 || true)
  SIM_BODY=$(sed -n '/^{/,/^}/p' <<<"$out" || true)
}
simget() { remote "sudo docker compose $COMPOSE_FILES exec -T market-sim wget -qO- 'http://127.0.0.1:8098$1'"; }

call GET "/v1/market/contracts/$SYMBOL" ""
SIM=$(simget /internal/sim)
if [[ $STATUS != 200 || $(jq -r .status <<<"$BODY") != TRADING || $(jq -r .running <<<"$SIM") != true ||
  $(jq -r --arg s "$SYMBOL" '[.perps[]? | select(.symbol == $s and .running)] | length' <<<"$SIM") != 1 ||
  $(pg "SELECT enabled FROM config.flags WHERE key = 'sim.events'") != t ]]; then
  echo "skip: $SYMBOL is not trading with the bots on it, or the simulated market's price events are off (scripts/ops/astra.sh perp-open $SYMBOL, perp-on, events-on)"
  exit 0
fi
SIZE=$(jq -r .contract_size <<<"$BODY")
MMR=$(jq -r '.risk_tiers[0].mmr' <<<"$BODY")
RECENT=$(pg "SELECT (SELECT count(*) FROM marketsim.events WHERE status <> 'CANCELED' AND type IN ('JUMP', 'TARGET', 'SPIKE', 'TREND', 'VOLATILITY') AND starts_at BETWEEN now() - interval '1 hour' AND now() + interval '1 hour') + (SELECT count(*) FROM marketsim.param_changes WHERE at > now() - interval '1 hour' AND (move <> 0 OR volume <> 0))")
if ((RECENT > 0)); then
  echo "skip: $RECENT price events or settings changes within the hour take one operator's room; run it an hour after them"
  exit 0
fi
# others_on_perps: positions on the platform coin's perpetuals of users
# other than the bots and this drill's, which the drops could liquidate.
MINE="'00000000-0000-0000-0000-000000000000'"
others_on_perps() {
  pg "SELECT count(*) FROM derivatives.positions WHERE symbol IN ('ASTRA-USDT-PERP', 'ASTRA-USD-PERP') AND quantity <> 0 AND user_id NOT IN (SELECT user_id FROM marketsim.bots) AND user_id NOT IN ($MINE)"
}
if (($(others_on_perps) > 0)); then
  echo "skip: $(others_on_perps) positions on the platform coin's perpetuals of users other than the bots; the drops could liquidate them"
  exit 0
fi

# undo SIZES...: the jump that undoes jumps of SIZES, exactly (1 / Π(1 +
# size) − 1, review DK C22 3).
undo() { jq -rn --arg s "$*" '(1 / ([$s | split(" ")[] | tonumber | 1 + .] | reduce .[] as $x (1; . * $x)) - 1) * 10000 | round / 10000'; }
DROPS=""
# back_up: the drops undone as the drill ends; a jump refused or lost
# leaves ASTRA lower for everyone: it fails the run and says how to put it
# back.
back_up() {
  [[ -n $DROPS ]] || return 0
  local body
  body="{\"type\":\"JUMP\",\"size\":$(undo $DROPS),\"actor\":\"fault-ops\",\"reason\":\"fault: back after the coin-margined liquidations\"}"
  simpost /internal/sim/events "$body"
  if [[ $SIM_STATUS != 201 ]]; then
    echo "WARN ASTRA's target is not back up (HTTP ${SIM_STATUS:-none} $SIM_BODY); by hand, on the server in /opt/exchange/infra:" >&2
    echo "     sudo docker compose $COMPOSE_FILES exec -T market-sim /app/exchangectl sim call POST /internal/sim/events '$body'" >&2
    EXIT_FAILED=1
    return
  fi
  echo "ok   ASTRA's target back up by $(undo $DROPS) (the drops $DROPS undone)"
  DROPS=""
}
# drop: an operator's jump of −4% on ASTRA's target.
drop() {
  local from to size
  from=$(simget /internal/sim | jq -r .target_price)
  to=$(jq -rn --argjson p "$from" '$p * 0.96 * 10000 | floor / 10000')
  size=$(jq -rn --argjson to "$to" --argjson from "$from" '($to / $from - 1) * 10000 | round / 10000')
  simpost /internal/sim/events "{\"type\":\"JUMP\",\"size\":$size,\"actor\":\"fault-ops\",\"reason\":\"fault: liquidate coin-margined positions\"}"
  [[ $SIM_STATUS == 201 ]] || fail "the jump: HTTP $SIM_STATUS $SIM_BODY"
  DROPS="${DROPS:+$DROPS }$size"
  echo "ok   the target goes from $from to $to (a jump of $size)"
}

# A trader: registered, ASTRA bought with the welcome USDT and IN of it
# moved to FUTURES, isolated (or MODE) at LEVERAGE on the perpetual; sets
# USER_<who> and AUTH_<who>.
trader() {
  local who=$1 leverage=$2 in=$3 mode=${4:-ISOLATED} token
  register "fault-coinm-$who-$RUN@example.com" "fault-coinm-$who-$RUN" "fault coinm $who $RUN"
  token=$(jq -r .access_token <<<"$BODY")
  eval "USER_$who=$(jq -r .user_id <<<"$BODY")"
  eval "AUTH_$who=(-H \"Authorization: Bearer $token\")"
  local auth=(-H "Authorization: Bearer $token")
  astra() {
    call GET /v1/account/balances "" "${auth[@]}"
    jq -r --arg a "$1" '[.balances[] | select(.account_type == "SPOT" and .asset == $a)][0].available // "0"' <<<"$BODY"
  }
  funded() { [[ $(astra USDT) != 0 ]]; }
  eventually 40 "welcome funds arrived ($who)" funded
  call POST /v1/orders "{\"symbol\":\"$SPOT\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quote_amount\":\"100\"}" "${auth[@]}" -H "Idempotency-Key: fault-coinm-astra-$RUN-$who"
  expect 202 - "a market buy of 100 USDT of ASTRA ($who)"
  enough() { awk -v b="$(astra ASTRA)" -v n="$in" 'BEGIN { exit !(b + 0 >= n + 0) }'; }
  eventually 60 "the ASTRA arrived ($who)" enough
  call POST /v1/account/transfers "{\"asset\":\"ASTRA\",\"amount\":\"$in\",\"from_account_type\":\"SPOT\",\"to_account_type\":\"FUTURES\"}" \
    "${auth[@]}" -H "Idempotency-Key: fault-coinm-in-$RUN-$who"
  expect 201 - "$in ASTRA to FUTURES ($who)"
  call PUT "/v1/derivatives/settings/$SYMBOL" "{\"margin_mode\":\"$mode\",\"leverage\":$leverage}" "${auth[@]}"
  expect 200 - "$mode, ${leverage}x ($who)"
}
# open WHO SIDE CONTRACTS: a market order of the user's.
open() {
  local auth_var="AUTH_$1[@]"
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"$2\",\"type\":\"MARKET\",\"quantity\":\"$3\"}" "${!auth_var}"
  expect 202 - "$1: a market $2 of $3 contracts"
}
# held WHO: BODY holds the user's position (exit 1 when flat).
held() {
  local auth_var="AUTH_$1[@]"
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "${!auth_var}" && jq -e '.positions | length == 1' <<<"$BODY" >/dev/null
}
flat() { ! held "$1"; }
# notice WHO TYPE: the user's inbox has a notice of TYPE (the contract
# notices of review FG, B133).
notice() {
  local auth_var="AUTH_$1[@]"
  call GET /v1/notifications "" "${!auth_var}" && [[ $STATUS == 200 ]] &&
    jq -e --arg t "$2" '[.items[] | select(.type == $t)] | length >= 1' <<<"$BODY" >/dev/null
}

echo "== four traders on $SYMBOL"
trader A 25 50
trader C 15 50
trader B 20 60
# D's account: what puts the take-over of a long of 30 contracts (V, their
# value in ASTRA at the mark) 3.85% under it, V x (mmr + 0.0385) / 0.9615,
# and the taker fee, V x 0.0005.
call GET "/v1/market/$SYMBOL/mark-price" ""
expect 200 - "$SYMBOL's mark price"
D_IN=$(jq -rn --argjson m "$(jq -r .mark_price <<<"$BODY")" --argjson size "$SIZE" --argjson mmr "$MMR" \
  '(30 * $size / $m) as $v | ($v * ($mmr + 0.0385) / 0.9615 + $v * 0.0005) * 10000 | ceil / 10000')
trader D 25 "$D_IN" CROSS
MINE="'$USER_A', '$USER_B', '$USER_C', '$USER_D'"
# shellcheck disable=SC2016 # expanded when the drill ends
at_exit 'unwind'
# unwind: the users' orders off the book and what is left of their
# positions closed against the bots; a position left fails the run (later
# drills would skip for it).
unwind() {
  local who qty side auth_var
  for who in A B C D; do
    auth_var="AUTH_$who[@]"
    call DELETE "/v1/derivatives/orders?symbol=$SYMBOL" "" "${!auth_var}" || true
    for _ in $(seq 12); do
      held "$who" || break
      qty=$(jq -r '.positions[0].quantity | tonumber | if . < 0 then -. else . end' <<<"$BODY")
      side=$(jq -r 'if (.positions[0].quantity | tonumber) < 0 then "BUY" else "SELL" end' <<<"$BODY")
      call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"$side\",\"type\":\"MARKET\",\"quantity\":\"$qty\",\"reduce_only\":true}" "${!auth_var}"
      sleep 5
    done
    if held "$who"; then
      echo "WARN $who still holds $SYMBOL: close it by hand (a reduce-only market order of that user)" >&2
      EXIT_FAILED=1
    fi
  done
  return 0
}
open A BUY 30
open C BUY 30
open D BUY 30
open B SELL 60
eventually 60 "A holds 30" held A
eventually 60 "C holds 30" held C
eventually 60 "D holds 30" held D
echo "     D's cross long: $D_IN ASTRA in FUTURES, estimated liquidation price $(jq -r '.positions[0].liquidation_price' <<<"$BODY") (entry $(jq -r '.positions[0].entry_price' <<<"$BODY"))"
eventually 60 "B holds -60" held B
if (($(others_on_perps) > 0)); then
  echo "skip: a position on the platform coin's perpetuals of a user other than the bots opened meanwhile; the drops could liquidate it"
  exit 0
fi
WARNABLE=$(pg "SELECT count(*) FROM ledger.accounts a WHERE a.owner_type = 'USER' AND a.account_type IN ('MARGIN_CROSS', 'MARGIN_ISOLATED') AND a.asset = 'ASTRA' AND a.available + a.frozen > 0 AND EXISTS (SELECT 1 FROM ledger.accounts d WHERE d.owner_type = 'USER' AND d.owner_id = a.owner_id AND d.scope = a.scope AND d.account_type IN (a.account_type || '_DEBT', a.account_type || '_INTEREST') AND d.available <> 0)")
echo "note: $WARNABLE margin accounts hold ASTRA with a debt; the drops may warn (and mail) them"
echo "== 1. ASTRA drops 4%: A is liquidated against the bots' bids"
# ASTRA's insurance fund before: its row, its version (the lines after it
# are what moved it) and the time A's funding is counted from, read before
# A's margin (review FP, C48: a funding payment between the two would go
# unseen).
read -r FUND_ID FUND_BEFORE FUND_V T0 <<<"$(pg "SELECT concat_ws(' ', id, trim_scale(available), version, extract(epoch FROM now())) FROM ledger.accounts WHERE owner_type = 'SYSTEM' AND account_type = 'INSURANCE_FUND' AND asset = 'ASTRA'")"
[[ -n $FUND_ID ]] || fail "ASTRA has no insurance fund account (scripts/ops/astra.sh seed)"
held A || fail "A's position went before the drop"
M_A=$(jq -r '.positions[0].margin' <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the drill ends
at_exit 'back_up'
drop
eventually 600 "A's position liquidated (flat)" flat A
# The fund's side of A's liquidation, from the ledger's lines (review FI,
# C46): an isolated position's liquidation fills pay the fund what is left
# of their share of the margin after the loss and the fee (filled above
# the bankruptcy price), or the fund pays the loss the margin does not
# cover (below it). Either way the fund's net is A's margin less the
# losses and the fees of the fills, its pay-outs are what the fills say
# it paid, and its row moved by its lines. Only the settlements of A's
# liquidation fills count (keyed fill:<trade>:<side>; a funding payment
# the fund covered is not one), and only the fund's available balance.
read -r LIQ_FILLS PAID IN OUT NET EXPECT OTHER FUND_AFTER FUNDED OK_PAID OK_ROW OK_NET <<<"$(pg "
WITH mine AS (
  SELECT l.amount FROM ledger.futures_settlements s
  CROSS JOIN LATERAL jsonb_array_elements(s.outcomes) o
  JOIN ledger.journal_lines l ON l.journal_id = NULLIF(o->>'journal_id', '')::uuid
  WHERE s.user_id = '$USER_A' AND split_part(s.idem_key, ':', 1) = 'fill'
    AND split_part(s.idem_key, ':', 2) IN (SELECT trade_id::text FROM derivatives.fills WHERE user_id = '$USER_A' AND liquidation)
    AND l.account_id = '$FUND_ID' AND l.balance_kind = 'AVAILABLE'),
m AS (SELECT coalesce(sum(amount) FILTER (WHERE amount > 0), 0) AS pay_in,
  coalesce(-sum(amount) FILTER (WHERE amount < 0), 0) AS pay_out FROM mine),
fund AS (SELECT available, version FROM ledger.accounts WHERE id = '$FUND_ID'),
lines AS (SELECT coalesce(sum(l.amount), 0) AS moved FROM ledger.journal_lines l, fund
  WHERE l.account_id = '$FUND_ID' AND l.balance_kind = 'AVAILABLE' AND l.account_version > $FUND_V AND l.account_version <= fund.version),
fills AS (SELECT count(*) FILTER (WHERE liquidation) AS n, coalesce(sum(insurance), 0) AS paid,
  coalesce(sum(greatest(-realized_pnl, 0)) FILTER (WHERE liquidation), 0) AS loss,
  coalesce(sum(fee) FILTER (WHERE liquidation), 0) AS fee
  FROM derivatives.fills WHERE user_id = '$USER_A' AND symbol = '$SYMBOL'),
v AS (SELECT fills.n, fills.paid, m.pay_in, m.pay_out, m.pay_in - m.pay_out AS net,
  '$M_A'::numeric - fills.loss - fills.fee AS expect, lines.moved - (m.pay_in - m.pay_out) AS other, fund.available,
  (SELECT count(*) FROM derivatives.funding_payments WHERE user_id = '$USER_A' AND settled_at >= to_timestamp($T0)) AS funded,
  '$FUND_BEFORE'::numeric + lines.moved = fund.available AS row_ok
  FROM fills, m, lines, fund)
SELECT concat_ws(' ', n, trim_scale(paid), trim_scale(pay_in), trim_scale(pay_out), trim_scale(net), trim_scale(expect),
  trim_scale(other), trim_scale(available), funded, pay_out = paid, row_ok, net = expect) FROM v")"
((LIQ_FILLS > 0)) || fail "A's position was closed by no liquidation fill"
[[ $OK_PAID == t ]] || fail "ASTRA's insurance fund paid out $OUT on A's liquidation, while its fills say $PAID"
[[ $OK_ROW == t ]] || fail "ASTRA's insurance fund row went from $FUND_BEFORE to $FUND_AFTER, not by its lines since version $FUND_V"
if ((FUNDED > 0)); then
  echo "note: a funding payment of A's came in between, so its margin is not checked against the fund's net"
else
  [[ $OK_NET == t ]] || fail "ASTRA's insurance fund's net on A's liquidation is $NET, while A's margin $M_A less the fills' losses and fees is $EXPECT"
fi
echo "ok   $LIQ_FILLS liquidation fills; ASTRA's insurance fund took $IN and paid $OUT (the fills say $PAID): net $NET, A's margin $M_A less the losses and fees"
[[ $OTHER == 0 ]] || echo "note: other settlements moved the fund meanwhile by $OTHER ASTRA ($FUND_BEFORE -> $FUND_AFTER)"
eventually 60 "A was told: CONTRACT_LIQUIDATING" notice A CONTRACT_LIQUIDATING
# The monitor looks once a second: a warning goes out when it sees the
# margin between 1.0 and 1.2 times the maintenance margin on the way down,
# which a mark moving with the 60-second index nearly always is. When the
# outbox has one, the inbox must have it too (review FP, C48).
WARNED=$(pg "SELECT count(*) FROM derivatives.outbox WHERE topic = 'derivatives.liquidation.events' AND event_type = 'derivatives.LiquidationWarning' AND partition_key = '$USER_A' AND created_at >= to_timestamp($T0)")
if ((WARNED > 0)); then
  eventually 60 "A was warned before: CONTRACT_LIQUIDATION_WARNED" notice A CONTRACT_LIQUIDATION_WARNED
else
  echo "note: no warning went out for A: the mark crossed the warning band between two checks"
fi
held C || fail "C's position went with A's"

echo "== 2. the bots leave $SYMBOL, ASTRA drops 4% more: C is deleveraged"
PERPS=$(remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl flags show sim.perp" | jq -r '(.rules.symbols.allow // []) | join(",")')
[[ ",$PERPS," == *",$SYMBOL,"* ]] || fail "sim.perp does not name $SYMBOL ($PERPS)"
PERP_OFF=""
# perp_back: the bots back on the perpetual; one that cannot go back
# fails the run.
perp_back() {
  [[ -n $PERP_OFF ]] || return 0
  if remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl flags set sim.perp --on --allow-symbols $PERPS --reason $(printf %q "fault coinm-liquidation.sh: the bots back on $SYMBOL")" >/dev/null; then
    PERP_OFF=""
    return 0
  fi
  echo "WARN sim.perp not put back; by hand: exchangectl flags set sim.perp --on --allow-symbols '$PERPS' --reason ..." >&2
  EXIT_FAILED=1
}
# shellcheck disable=SC2016 # expanded when the drill ends
at_exit 'perp_back'
PERP_OFF=1
remote "sudo docker compose $COMPOSE_FILES exec -T ledger-service /app/exchangectl flags set sim.perp --on --allow-symbols $(jq -rn --arg p "$PERPS" --arg s "$SYMBOL" '[$p | split(",")[] | select(. != $s)] | join(",")') --reason $(printf %q "fault coinm-liquidation.sh: the bots leave $SYMBOL for a moment")" >/dev/null
bare() {
  call GET "/v1/market/$SYMBOL/depth?limit=5" "" && [[ $STATUS == 200 ]] && jq -e '(.bids | length) == 0' <<<"$BODY" >/dev/null
}
eventually 120 "no bid left on $SYMBOL" bare
held C || fail "C's position went"
# C's bankruptcy price, read just before the drop (review FI, C46):
# contracts x face value / (cost + margin), the cost contracts x face /
# the entry price (an inverse contract's harmonic mean).
C_BANKRUPT=$(jq -r --argjson size "$SIZE" '.positions[0] | ((.quantity | tonumber) * $size) as $qs | ($qs / (.entry_price | tonumber) + (.margin | tonumber)) as $left | $qs / $left' <<<"$BODY")
drop
eventually 600 "C's position closed (flat)" flat C
ADL=$(pg "SELECT f.price || ' ' || f.fee || ' ' || f.realized_pnl FROM derivatives.fills f JOIN derivatives.orders o ON o.order_id = f.order_id WHERE f.user_id = '$USER_C' AND f.symbol = '$SYMBOL' AND o.kind = 'ADL' ORDER BY f.executed_at DESC LIMIT 1")
[[ -n $ADL ]] || fail "C's position was not deleveraged: $(pg "SELECT o.kind || ' ' || f.price FROM derivatives.fills f JOIN derivatives.orders o ON o.order_id = f.order_id WHERE f.user_id = '$USER_C' ORDER BY f.executed_at")"
read -r ADL_PRICE ADL_FEE ADL_PNL <<<"$ADL"
jq -en --argjson p "$ADL_PRICE" --argjson b "$C_BANKRUPT" --argjson f "$ADL_FEE" '(($p - $b) | fabs) <= 0.0001 and $f == 0' >/dev/null ||
  fail "C's ADL fill at $ADL_PRICE (its bankruptcy price is about $C_BANKRUPT), fee $ADL_FEE"
ATTEMPTS=$(pg "SELECT count(*) FROM derivatives.orders WHERE user_id = '$USER_C' AND symbol = '$SYMBOL' AND kind = 'LIQUIDATION'")
echo "ok   C deleveraged at $ADL_PRICE (bankruptcy about $C_BANKRUPT), no fee, PnL $ADL_PNL ASTRA, after $ATTEMPTS liquidation orders"
WHO=$(pg "SELECT string_agg(DISTINCT CASE WHEN o.user_id = '$USER_B' THEN 'B' WHEN o.user_id IN (SELECT user_id FROM marketsim.bots) THEN 'a bot' ELSE o.user_id::text END, ', ') FROM derivatives.orders o WHERE o.symbol = '$SYMBOL' AND o.kind = 'ADL' AND o.user_id NOT IN ('$USER_C', '$USER_D') AND o.created_at > now() - interval '15 minutes'")
echo "ok   against: $WHO"
eventually 60 "C was told: CONTRACT_LIQUIDATING" notice C CONTRACT_LIQUIDATING
if [[ $WHO == *B* ]]; then
  eventually 60 "B was told of its deleveraging: CONTRACT_ADL" notice B CONTRACT_ADL
fi

echo "== 3. D's cross account: what its liquidation left to ASTRA's insurance fund (review C68)"
eventually 600 "D's cross long liquidated (flat)" flat D
cleared() { [[ $(pg "SELECT status FROM derivatives.cross_liquidations WHERE user_id = '$USER_D'") == DONE ]]; }
eventually 60 "D's cross liquidation over (DONE)" cleared
read -r D_FEE D_LEFT D_EQUITY <<<"$(pg "SELECT concat_ws(' ', trim_scale(fee), trim_scale(balance + flows), trim_scale(equity)) FROM derivatives.cross_liquidations WHERE user_id = '$USER_D'")"
# The fee is what the liquidation left, at most the equity at the take-over,
# none when nothing was left (down to ASTRA's decimals).
jq -en --argjson fee "$D_FEE" --argjson left "$D_LEFT" --argjson eq "$D_EQUITY" '([$left, $eq, 0] | sort | .[1]) as $want | $fee <= $want and $want - $fee < 0.0001' >/dev/null ||
  fail "D's clearance fee $D_FEE, while the liquidation left $D_LEFT (equity at the take-over $D_EQUITY)"
D_AUTH="AUTH_D[@]"
call GET "/v1/account/ledger?asset=ASTRA&type=INSURANCE_CONTRIBUTION" "" "${!D_AUTH}"
expect 200 - "D's insurance fund entries"
check "[.items[] | select(.account_type == \"FUTURES\") | .amount | tonumber | fabs] | (add // 0) - $D_FEE | fabs < 0.000001" \
  "the fee $D_FEE in D's ledger as INSURANCE_CONTRIBUTION (the liquidation left $D_LEFT, its equity at the take-over $D_EQUITY)"
call GET "/v1/derivatives/account?asset=ASTRA" "" "${!D_AUTH}"
expect 200 - "D's ASTRA FUTURES account"
check '(.wallet_balance | tonumber) == 0 and (.frozen | tonumber) == 0' "D's account at zero"
eventually 60 "D was told: CONTRACT_LIQUIDATED" notice D CONTRACT_LIQUIDATED

echo "== the price, the bots and B's position back"
back_up
perp_back
eventually 120 "the bots bid on $SYMBOL again" eval '! bare'
unwind
[[ -z $EXIT_FAILED ]] || fail "a position is still open"
echo "ok   B's position closed"

echo "== the derivatives reconciliation, every settlement asset"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives reconcile" | grep -E "^ASTRA|settlements" | sed 's/^/     /'
echo "coin-margined liquidation drill passed"
