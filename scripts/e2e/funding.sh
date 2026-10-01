#!/usr/bin/env bash
# Funding end to end (implementation plan §7.3 task 10, requirements
# §11.7). A standing hedge of two e2e users, one long and one short 0.01
# ETH-USDT-PERP (each opened against HOUSE, which takes every order; a hedge
# opened before B4 stays on BTC-USDT-PERP at 0.001), lives across runs;
# their addresses and password stay in a local state file (E2E_STATE_DIR,
# default ~/.cache/exchange-e2e), never in the repository. The first run
# opens it; every later run signs both in (answering the 7-day login
# challenge from the dev inbox) and checks each funding time since it
# opened: both positions were settled at the rate market-data-service
# settled, the payer paid and the receiver received the quantity × the
# settlement mark × |rate| (the payer rounded up, the receiver down).
# Every run checks that derivatives-service's funding rounds are neither
# stuck nor short. Needs derivatives.trading, a mark price
# (market.reference_feed) and ssh to the server.
#
#   scripts/e2e/funding.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

NEW_SYMBOL=ETH-USDT-PERP
NEW_QTY=0.01
STATE_DIR=${E2E_STATE_DIR:-$HOME/.cache/exchange-e2e}
STATE="$STATE_DIR/funding-$(sed -E 's#^https?://##; s#[^A-Za-z0-9]+#_#g' <<<"$BASE")"

echo "== funding rounds on the server"
remote "sudo docker compose $COMPOSE_FILES exec -T derivatives-service /app/exchangectl derivatives funding --limit 12" | sed 's/^/     /'
echo "ok   no round waits for its rate too long, none paid out more than it collected"

# login EMAIL DEVICE signs in with PASSWORD and sets TOKEN; after 7 days
# without a login the password alone gets a challenge answered by a code.
login() {
  local email=$1 device=$2 challenge before code
  call POST /v1/auth/login/password "{\"identifier\":\"$email\",\"password\":\"$PASSWORD\",\"device_id\":\"$device\"}" "${APP[@]}"
  if [[ $STATUS == 403 && $(jq -r .code <<<"$BODY") == AUTH_LOGIN_CHALLENGE_REQUIRED ]]; then
    challenge=$(jq -r .details.login_challenge_id <<<"$BODY")
    before=$(inbox_count "$email")
    call POST /v1/auth/otp/request "{\"scene\":\"LOGIN_CHALLENGE\",\"channel\":\"EMAIL\",\"login_challenge_id\":\"$challenge\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$device\"}"
    expect 200 - "a login challenge code for $email"
    code=$(await_code "$email" "$before")
    call POST /v1/auth/otp/verify "{\"challenge_id\":\"$(jq -r .challenge_id <<<"$BODY")\",\"code\":\"$code\",\"device_id\":\"$device\"}"
    expect 200 - "otp/verify LOGIN_CHALLENGE"
    call POST /v1/auth/login/challenge "{\"otp_ticket\":\"$(jq -r .otp_ticket <<<"$BODY")\",\"login_challenge_id\":\"$challenge\",\"device_id\":\"$device\"}" "${APP[@]}"
  fi
  expect 200 - "$email signs in"
  TOKEN=$(jq -r .access_token <<<"$BODY")
}

position() { # position AUTH...: BODY holds the user's only position on the contract
  call GET "/v1/derivatives/positions?symbol=$SYMBOL" "" "$@" && jq -e '.positions | length == 1' <<<"$BODY"
}

# open_hedge registers two users, gives each 100 USDT in FUTURES, opens a
# long for one and a short for the other with market orders against HOUSE
# and records them in the state file.
open_hedge() {
  local who
  SYMBOL=$NEW_SYMBOL QTY=$NEW_QTY
  PASSWORD="e2e-$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 20 || true)"
  for who in long short; do
    register "e2e-funding-$who-$RUN@example.com" "e2e-funding-$who-$RUN" "$PASSWORD"
    eval "TOKEN_$who=$(jq -r .access_token <<<"$BODY")"
  done
  # shellcheck disable=SC2154 # set above
  LONG=(-H "Authorization: Bearer $TOKEN_long")
  # shellcheck disable=SC2154
  SHORT=(-H "Authorization: Bearer $TOKEN_short")
  for who in LONG SHORT; do
    eval "auth=(\"\${${who}[@]}\")"
    funded() {
      call GET /v1/account/balances "" "${auth[@]}"
      [[ $(jq -r '[.balances[] | select(.account_type == "SPOT" and .asset == "USDT")][0].available // "0"' <<<"$BODY") != "0" ]]
    }
    eventually 40 "welcome funds arrived ($who)" funded
    call POST /v1/account/transfers '{"asset":"USDT","amount":"100","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
      "${auth[@]}" -H "Idempotency-Key: funding-in-$RUN-$who"
    expect 201 - "100 USDT to FUTURES ($who)"
  done
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"BUY\",\"type\":\"MARKET\",\"quantity\":\"$QTY\"}" "${LONG[@]}"
  expect 202 - "the long buys $QTY at the market"
  call POST /v1/derivatives/orders "{\"symbol\":\"$SYMBOL\",\"side\":\"SELL\",\"type\":\"MARKET\",\"quantity\":\"$QTY\"}" "${SHORT[@]}"
  expect 202 - "the short sells $QTY at the market"
  eventually 40 "the long holds $QTY" position "${LONG[@]}"
  eventually 40 "the short holds -$QTY" position "${SHORT[@]}"
  mkdir -p "$STATE_DIR"
  umask 077
  {
    printf 'EMAIL_LONG=%q\nEMAIL_SHORT=%q\n' "e2e-funding-long-$RUN@example.com" "e2e-funding-short-$RUN@example.com"
    printf 'DEVICE_LONG=%q\nDEVICE_SHORT=%q\n' "e2e-funding-long-$RUN" "e2e-funding-short-$RUN"
    printf 'PASSWORD=%q\nOPENED_AT=%q\n' "$PASSWORD" "$(date +%s)"
    printf 'SYMBOL=%q\nQTY=%q\n' "$SYMBOL" "$QTY"
  } >"$STATE"
  echo "ok   the hedge is open; the next run after a funding time (00:00, 08:00, 16:00 UTC) checks its payments"
}

if [[ ! -f $STATE ]]; then
  echo "== no standing hedge yet: opening one"
  open_hedge
  echo "all funding checks passed"
  exit 0
fi

SYMBOL=BTC-USDT-PERP QTY=0.001 # a state file from before B4 names neither
# shellcheck source=/dev/null
source "$STATE"
echo "== the standing hedge (opened $(date -u -r "$OPENED_AT" +%FT%TZ 2>/dev/null || date -u -d "@$OPENED_AT" +%FT%TZ))"
login "$EMAIL_LONG" "$DEVICE_LONG"
LONG=(-H "Authorization: Bearer $TOKEN")
login "$EMAIL_SHORT" "$DEVICE_SHORT"
SHORT=(-H "Authorization: Bearer $TOKEN")
if ! position "${LONG[@]}" >/dev/null || ! position "${SHORT[@]}" >/dev/null; then
  echo "     the hedge is gone (closed or deleveraged); opening a new one"
  rm -f "$STATE"
  open_hedge
  echo "all funding checks passed"
  exit 0
fi
echo "ok   both positions are open"

call GET "/v1/market/$SYMBOL/funding-rates?limit=50" ""
expect 200 - "settled funding rates"
RATES=$BODY
# Settlement follows the funding time within seconds; give it 15 minutes.
due=$(jq -r --argjson from "$OPENED_AT" --argjson to "$(($(date +%s) - 900))" \
  '[.funding_rates[] | select((.funding_time | fromdateiso8601) > $from and (.funding_time | fromdateiso8601) < $to) | .funding_time] | .[]' <<<"$RATES")
if [[ -z $due ]]; then
  echo "ok   no funding time has passed since the hedge opened; the next run checks it"
  echo "all funding checks passed"
  exit 0
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
  # jq numbers are doubles: fine for amounts of a few cents at 6 decimals.
  jq -ne --argjson l "$long" --argjson s "$short" --arg r "$rate" --arg q "$QTY" '
    ($r | tonumber) as $rate | ($l.mark_price | tonumber) as $mark | (($q | tonumber) * $mark * (if $rate < 0 then -$rate else $rate end)) as $due |
    ($l.amount | tonumber) as $la | ($s.amount | tonumber) as $sa |
    ($l.funding_rate == $r) and ($s.funding_rate == $r) and ($l.mark_price == $s.mark_price) and
    (if $rate > 0 then ($la <= 0 and $sa >= 0 and (-$la - $due) > -1e-9 and (-$la - $due) < 1.000001e-6 and ($due - $sa) > -1e-9 and ($due - $sa) < 1.000001e-6)
     elif $rate < 0 then ($sa <= 0 and $la >= 0 and (-$sa - $due) > -1e-9 and (-$sa - $due) < 1.000001e-6 and ($due - $la) > -1e-9 and ($due - $la) < 1.000001e-6)
     else ($la == 0 and $sa == 0) end)' >/dev/null ||
    { echo "FAIL $at at rate $rate: long $long, short $short" >&2; exit 1; }
  echo "ok   $at: rate $rate, long $(jq -r .amount <<<"$long"), short $(jq -r .amount <<<"$short")"
done
echo "all funding checks passed"
