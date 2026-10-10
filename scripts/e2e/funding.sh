#!/usr/bin/env bash
# Funding end to end (implementation plan §7.3 task 10, requirements
# §11.7; coin-margined design 2026-10-06 G2). Two standing hedges, each of
# two e2e users, one long and one short (each opened against HOUSE, which
# takes every order), live across runs: 0.01 ETH-USDT-PERP (a hedge opened
# before B4 stays on BTC-USDT-PERP at 0.001), and 1 contract of
# BTC-USD-PERP, 100 USD margined and paid in BTC (left alone while that
# contract is not TRADING). Their addresses and password stay in local
# state files (E2E_STATE_DIR, default ~/.cache/exchange-e2e), never in the
# repository. The first run opens a hedge; every later run signs both in
# (answering the 7-day login challenge from the dev inbox) and checks each
# funding time since it opened: both positions were settled at the rate
# market-data-service settled, the payer paid and the receiver received
# what was due (the payer rounded up, the receiver down): the quantity ×
# the settlement mark × |rate| in USDT, the contracts × 100 USD × |rate| ÷
# the settlement mark in BTC. Every run checks that derivatives-service's
# funding rounds are neither stuck nor short. Needs derivatives.trading
# (and derivatives.coin_m), a mark price (market.reference_feed) and ssh to
# the server.
#
#   scripts/e2e/funding.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

NEW_SYMBOL=ETH-USDT-PERP
NEW_QTY=0.01
COINM_SYMBOL=BTC-USD-PERP
COINM_QTY=1
# BTC each user of the coin-margined hedge moves to FUTURES: a long of one
# contract (about 0.0012 BTC) is liquidated only some 60% lower.
COINM_IN=0.002
STATE_DIR=${E2E_STATE_DIR:-$HOME/.cache/exchange-e2e}
STATE_NAME=$(sed -E 's#^https?://##; s#[^A-Za-z0-9]+#_#g' <<<"$BASE")

echo "== funding rounds on the server"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives funding --limit 12" | sed 's/^/     /'
echo "ok   no round waits for its rate too long, none paid out more than it collected"

# login EMAIL DEVICE signs in with PASSWORD and sets TOKEN; after 7 days
# without a login the password alone gets a challenge answered by a code.
# It fails (status 1) only for an account that is closed (B184/B186: a
# purge cleared the hedge out) and stops the script on anything else -
# also where set -e does not, as the condition of an if.
login() {
  local email=$1 device=$2 challenge before code
  call POST /v1/auth/login/password "{\"identifier\":\"$email\",\"password\":\"$PASSWORD\",\"device_id\":\"$device\"}" "${APP[@]}" || exit 1
  if [[ $STATUS == 403 && $(jq -r .code <<<"$BODY") == USER_CLOSED ]]; then
    return 1
  fi
  if [[ $STATUS == 403 && $(jq -r .code <<<"$BODY") == AUTH_LOGIN_CHALLENGE_REQUIRED ]]; then
    challenge=$(jq -r .details.login_challenge_id <<<"$BODY")
    before=$(inbox_count "$email")
    call POST /v1/auth/otp/request "{\"scene\":\"LOGIN_CHALLENGE\",\"channel\":\"EMAIL\",\"login_challenge_id\":\"$challenge\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$device\"}" || exit 1
    expect 200 - "a login challenge code for $email"
    code=$(await_code "$email" "$before")
    call POST /v1/auth/otp/verify "{\"challenge_id\":\"$(jq -r .challenge_id <<<"$BODY")\",\"code\":\"$code\",\"device_id\":\"$device\"}" || exit 1
    expect 200 - "otp/verify LOGIN_CHALLENGE"
    call POST /v1/auth/login/challenge "{\"otp_ticket\":\"$(jq -r .otp_ticket <<<"$BODY")\",\"login_challenge_id\":\"$challenge\",\"device_id\":\"$device\"}" "${APP[@]}" || exit 1
  fi
  expect 200 - "$email signs in"
  TOKEN=$(jq -r .access_token <<<"$BODY")
}

position() { # position AUTH...: BODY holds the user's only position on the contract
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "$@" && jq -e '.positions | length == 1' <<<"$BODY"
}

# exempt [--off] EMAIL... keeps the hedge's accounts out of the test-account
# purge (L4), or lets them in again once the hedge is gone: the exit hook
# clears out the accounts a run registers, and the hedge stands from run
# to run. Idempotent. Exempting that fails fails the run (the next purge
# would take the hedge); lifting it only warns.
exempt() {
  local off="" e out
  if [[ $1 == --off ]]; then
    off=" --off"
    shift
  fi
  for e in "$@"; do
    if ! out=$(ssh -o ConnectTimeout=20 exchange "cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T -e EXCHANGECTL_ACTOR=e2e-funding user-service /app/exchangectl users exempt --email-like '$e'$off --reason 'funding.sh: the standing hedge'" 2>&1 </dev/null); then
      if [[ -n $off ]]; then
        echo "warn: $e is still exempt from the purge: $(tail -1 <<<"$out")" >&2
        continue
      fi
      echo "FAIL the hedge's account $e is not exempt from the purge: $(tail -1 <<<"$out")" >&2
      exit 1
    fi
  done
  [[ -n $off ]] || echo "ok   the hedge's accounts are exempt from the purge"
}

# open_hedge KIND STATE registers two users and funds their FUTURES
# accounts (usdt: 100 USDT each; coinm: COINM_IN BTC each, bought with the
# welcome USDT), opens a long for one and a short for the other with
# market orders against HOUSE and records them in STATE.
open_hedge() {
  local kind=$1 state=$2 who prefix=e2e-funding
  SYMBOL=$NEW_SYMBOL QTY=$NEW_QTY
  if [[ $kind == coinm ]]; then
    SYMBOL=$COINM_SYMBOL QTY=$COINM_QTY prefix=e2e-funding-coinm
  fi
  PASSWORD="e2e-$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 20 || true)"
  for who in long short; do
    register "$prefix-$who-$RUN@example.com" "$prefix-$who-$RUN" "$PASSWORD"
    eval "TOKEN_$who=$(jq -r .access_token <<<"$BODY")"
  done
  exempt "$prefix-long-$RUN@example.com" "$prefix-short-$RUN@example.com"
  # shellcheck disable=SC2154 # set above
  LONG=(-H "Authorization: Bearer $TOKEN_long")
  # shellcheck disable=SC2154
  SHORT=(-H "Authorization: Bearer $TOKEN_short")
  for who in LONG SHORT; do
    eval "auth=(\"\${${who}[@]}\")"
    spot() { # spot ASSET: the SPOT account's available amount
      call GET /v1/account/balances "" "${auth[@]}"
      jq -r --arg a "$1" '[.balances[] | select(.account_type == "SPOT" and .asset == $a)][0].available // "0"' <<<"$BODY"
    }
    funded() { [[ $(spot USDT) != 0 ]]; }
    eventually 40 "welcome funds arrived ($who)" funded
    if [[ $kind == coinm ]]; then
      call POST /v1/orders '{"symbol":"BTC-USDT","side":"BUY","type":"MARKET","quote_amount":"300"}' "${auth[@]}" -H "Idempotency-Key: funding-btc-$RUN-$who"
      expect 202 - "a market buy of 300 USDT of BTC ($who)"
      enough() { awk -v b="$(spot BTC)" -v n="$COINM_IN" 'BEGIN { exit !(b + 0 >= n + 0) }'; }
      eventually 40 "the BTC arrived ($who)" enough
      call POST /v1/account/transfers "{\"asset\":\"BTC\",\"amount\":\"$COINM_IN\",\"from_account_type\":\"SPOT\",\"to_account_type\":\"FUTURES\"}" \
        "${auth[@]}" -H "Idempotency-Key: funding-coinm-in-$RUN-$who"
      expect 201 - "$COINM_IN BTC to FUTURES ($who)"
    else
      call POST /v1/account/transfers '{"asset":"USDT","amount":"100","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
        "${auth[@]}" -H "Idempotency-Key: funding-in-$RUN-$who"
      expect 201 - "100 USDT to FUTURES ($who)"
    fi
  done
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"$QTY\"}" "${LONG[@]}"
  expect 202 - "the long buys $QTY $SYMBOL at the market"
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$QTY\"}" "${SHORT[@]}"
  expect 202 - "the short sells $QTY $SYMBOL at the market"
  eventually 40 "the long holds $QTY" position "${LONG[@]}"
  eventually 40 "the short holds -$QTY" position "${SHORT[@]}"
  mkdir -p "$STATE_DIR"
  (
    umask 077
    {
      printf 'EMAIL_LONG=%q\nEMAIL_SHORT=%q\n' "$prefix-long-$RUN@example.com" "$prefix-short-$RUN@example.com"
      printf 'DEVICE_LONG=%q\nDEVICE_SHORT=%q\n' "$prefix-long-$RUN" "$prefix-short-$RUN"
      printf 'PASSWORD=%q\nOPENED_AT=%q\n' "$PASSWORD" "$(date +%s)"
      printf 'SYMBOL=%q\nQTY=%q\n' "$SYMBOL" "$QTY"
    } >"$state"
  )
  echo "ok   the hedge is open; the next run after a funding time (00:00, 08:00, 16:00 UTC) checks its payments"
}

# hedge KIND STATE checks the payments of the standing hedge recorded in
# STATE, or opens one when there is none yet or its positions are gone.
hedge() {
  local kind=$1 state=$2 due at rate long short size=0
  if [[ ! -f $state ]]; then
    echo "== no standing $kind hedge yet: opening one"
    open_hedge "$kind" "$state"
    return
  fi
  SYMBOL=BTC-USDT-PERP QTY=0.001 # a state file from before B4 names neither
  # shellcheck source=/dev/null
  source "$state"
  echo "== the standing $kind hedge on $SYMBOL (opened $(date -u -r "$OPENED_AT" +%FT%TZ 2>/dev/null || date -u -d "@$OPENED_AT" +%FT%TZ))"
  if ! login "$EMAIL_LONG" "$DEVICE_LONG" || { LONG=(-H "Authorization: Bearer $TOKEN") && ! login "$EMAIL_SHORT" "$DEVICE_SHORT"; }; then
    # A purge cleared the hedge out before it was exempt (B184 ⑤); the
    # other account, if it is still there, goes with the next one.
    echo "     the hedge's accounts were closed (a purge cleared them out); opening a new one"
    exempt --off "$EMAIL_LONG" "$EMAIL_SHORT"
    rm -f "$state"
    open_hedge "$kind" "$state"
    return
  fi
  SHORT=(-H "Authorization: Bearer $TOKEN")
  if ! position "${LONG[@]}" >/dev/null || ! position "${SHORT[@]}" >/dev/null; then
    echo "     the hedge is gone (closed or deleveraged); opening a new one"
    exempt --off "$EMAIL_LONG" "$EMAIL_SHORT"
    rm -f "$state"
    open_hedge "$kind" "$state"
    return
  fi
  echo "ok   both positions are open"
  exempt "$EMAIL_LONG" "$EMAIL_SHORT"

  if [[ $kind == coinm ]]; then
    call GET "/v1/market/contracts/$SYMBOL" ""
    expect 200 - "the contract's face value"
    size=$(jq -r .contract_size <<<"$BODY")
  fi
  call GET "/v1/market/$SYMBOL/funding-rates?limit=50" ""
  expect 200 - "settled funding rates"
  RATES=$BODY
  # Settlement follows the funding time within seconds (Binance's settled
  # rate within two minutes); give it 15 minutes.
  due=$(jq -r --argjson from "$OPENED_AT" --argjson to "$(($(date +%s) - 900))" \
    '[.funding_rates[] | select((.funding_time | fromdateiso8601) > $from and (.funding_time | fromdateiso8601) < $to) | .funding_time] | .[]' <<<"$RATES")
  if [[ -z $due ]]; then
    echo "ok   no funding time has passed since the hedge opened; the next run checks it"
    return
  fi
  call GET "/v1/derivatives/funding?symbol=$SYMBOL&limit=100" "" "${LONG[@]}"
  expect 200 - "the long's funding"
  LONG_ITEMS=$BODY
  call GET "/v1/derivatives/funding?symbol=$SYMBOL&limit=100" "" "${SHORT[@]}"
  expect 200 - "the short's funding"
  SHORT_ITEMS=$BODY
  for at in $due; do
    rate=$(jq -r --arg t "$at" '.funding_rates[] | select(.funding_time == $t) | .funding_rate' <<<"$RATES")
    long=$(jq -c --arg t "$at" '[.items[] | select(.funding_time == $t)][0]' <<<"$LONG_ITEMS")
    short=$(jq -c --arg t "$at" '[.items[] | select(.funding_time == $t)][0]' <<<"$SHORT_ITEMS")
    [[ $long != null && $short != null ]] || { echo "FAIL $at: no funding for the long ($long) or the short ($short)" >&2; exit 1; }
    # jq numbers are doubles: fine for amounts of a few cents at 6
    # decimals, or of a few satoshis at 8.
    jq -ne --argjson l "$long" --argjson s "$short" --arg r "$rate" --arg q "$QTY" --arg size "$size" '
      ($r | tonumber) as $rate | ($l.mark_price | tonumber) as $mark | (if $rate < 0 then -$rate else $rate end) as $abs |
      (if ($size | tonumber) > 0 then ($q | tonumber) * ($size | tonumber) * $abs / $mark else ($q | tonumber) * $mark * $abs end) as $due |
      (if ($size | tonumber) > 0 then 1e-8 else 1e-6 end) as $unit |
      ($l.amount | tonumber) as $la | ($s.amount | tonumber) as $sa |
      ($l.funding_rate == $r) and ($s.funding_rate == $r) and ($l.mark_price == $s.mark_price) and
      (if $rate > 0 then ($la <= 0 and $sa >= 0 and (-$la - $due) > -$unit / 1000 and (-$la - $due) < $unit * 1.000001 and ($due - $sa) > -$unit / 1000 and ($due - $sa) < $unit * 1.000001)
       elif $rate < 0 then ($sa <= 0 and $la >= 0 and (-$sa - $due) > -$unit / 1000 and (-$sa - $due) < $unit * 1.000001 and ($due - $la) > -$unit / 1000 and ($due - $la) < $unit * 1.000001)
       else ($la == 0 and $sa == 0) end)' >/dev/null ||
      { echo "FAIL $at at rate $rate: long $long, short $short" >&2; exit 1; }
    echo "ok   $at: rate $rate ($(jq -r --arg t "$at" '.funding_rates[] | select(.funding_time == $t) | .source // "PLATFORM"' <<<"$RATES")), long $(jq -r .amount <<<"$long"), short $(jq -r .amount <<<"$short") $(jq -r '.settle_asset // "USDT"' <<<"$long")"
  done
}

hedge usdt "$STATE_DIR/funding-$STATE_NAME"
call GET "/v1/market/contracts/$COINM_SYMBOL" ""
if [[ $STATUS == 200 && $(jq -r .status <<<"$BODY") == TRADING ]]; then
  hedge coinm "$STATE_DIR/funding-coinm-$STATE_NAME"
else
  echo "SKIP the coin-margined hedge: $COINM_SYMBOL is not TRADING"
fi
echo "all funding checks passed"
