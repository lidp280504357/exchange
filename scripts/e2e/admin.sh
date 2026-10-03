#!/usr/bin/env bash
# The admin console end to end (implementation plan §6.3 task 11): nginx
# serves the console on its own domain (admin.astras.vip, phase 4 B5; the
# old /admin/ of the user site redirects there) and its API at /admin/v1
# (admin-service, never the gateway). Four administrators are created for the run with
# exchangectl in the admin-service container (random passwords and
# authenticator secrets passed on stdin, never printed) and disabled at
# the end. It checks sign-in (password + TOTP, one use per code, or the
# password alone while the flag admin.login_without_totp is on; the
# cookie's attributes, the CSRF header), roles, freezing and unfreezing
# an account, cancelling its orders, the guard of trading parameters
# (statuses, a reference symbol, a risk ladder's impact: an ADMIN confirms
# the preview, the change waits its minute — the run sets the least delay
# and puts it back — or is canceled; a halt at once), a flag
# round trip, a two-person ledger adjustment, the settings and
# single-person mode (adjustments booked alone, one above the limit
# waiting and withdrawn), the counts and their event stream, the
# withdrawal list, the perpetual contracts (states, reduce-only, a status
# round trip, the liquidation monitor, a two-person insurance fund
# contribution), the reports, every user's positions and the liquidation
# log, a pair listed from the console (LINK-BTC: a reference symbol checked
# with Binance, previewed, applied, opened, an order resting on it), the
# deposits that need a person (with the
# custodian's stand-in: a deposit whose callback comes late, backfilled
# and then confirmed by it; one below the minimum credited to the user,
# another rejected; a deposit to a probe address of nobody credited to
# the user, C5.5 ㉑), a withdrawal's review details and holds, the user's
# authenticator app bound and reset by the console (the reset recorded:
# withdrawals wait for review for a day after it), an
# administrator created from the console (C5.5 ⑪: a one-time setup link
# shown once, which no check prints, whose holder sets the password and
# binds the authenticator without a session; an OPERATOR signing in with
# them; role, password and authenticator resets each with its link, its
# own password changed, sessions, disable and enable), the system health with
# details, an announcement on both sites within a minute (scheduled,
# published, edited while shown, taken off) and an in-app message
# delivered to the user and read, the reports over a period by week or
# month with the users' activity and HOUSE's result, an asset's profile
# edited and read by the sites, the simulated market (its state, a price
# event within one operator's share and one beyond it approved by a second
# administrator, both starting tomorrow and canceled; settings changed and
# put back, one beyond the share rejected; who holds the coin; a cent
# minted for every bot; the bots' orders and trades), the audit trail with
# its CSV export, and sign-out. Every request that moves money carries an
# Idempotency-Key (C5.5 ⑥): an adjustment, a hold, its release and the
# in-app message are sent twice under theirs and made once, another
# request under a key is refused, and one without is too.
#
#   scripts/e2e/admin.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

CSRF=(-H 'X-Admin-CSRF: 1')
ADMIN_BASE="${ADMIN_BASE:-https://admin.astras.vip}"
# acall is call on the console's domain.
acall() {
  local user_base=$BASE rc=0
  BASE=$ADMIN_BASE
  call "$@" || rc=$?
  BASE=$user_base
  return $rc
}
totp() { node "$(dirname "$0")/lib/totp.mjs" "$1" "${2:-0}"; }
# A base32 secret of 160 bits and a password, both random (tr ends on
# SIGPIPE when head has enough, which pipefail would count as a failure).
secret() { LC_ALL=C tr -dc 'A-Z2-7' </dev/urandom | head -c 32 || true; }
password() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true; }

echo "== the console is served at $ADMIN_BASE"
acall GET / "" -D "$WORK/headers"
[[ $STATUS == 200 ]] && grep -q '管理后台' <<<"$BODY" || { echo "FAIL GET $ADMIN_BASE/: $STATUS" >&2; exit 1; }
grep -qi '^x-frame-options: DENY' "$WORK/headers" && grep -qi "^content-security-policy: default-src 'self'" "$WORK/headers" &&
  grep -qi '^x-robots-tag: noindex' "$WORK/headers" || { echo "FAIL the console's security headers:" >&2; cat "$WORK/headers" >&2; exit 1; }
echo "ok   the console page, with X-Frame-Options, CSP and noindex"
ASSET=$(grep -oE '/assets/index-[A-Za-z0-9_-]+\.js' <<<"$BODY" | head -1)
acall GET "$ASSET" "" -D "$WORK/headers"
[[ $STATUS == 200 ]] && grep -qi '^cache-control: public, max-age=31536000, immutable' "$WORK/headers" ||
  { echo "FAIL $ASSET: $STATUS" >&2; exit 1; }
echo "ok   its hashed assets are cached for good"
acall GET /withdrawals ""
[[ $STATUS == 200 ]] && grep -q '管理后台' <<<"$BODY" || { echo "FAIL the SPA fallback: $STATUS" >&2; exit 1; }
echo "ok   deep links fall back to the console page"
call GET /admin/withdrawals "" -D "$WORK/headers"
[[ $STATUS == 301 ]] && grep -qi "^location: $ADMIN_BASE/withdrawals" "$WORK/headers" ||
  { echo "FAIL the old console's address does not lead to $ADMIN_BASE: $STATUS" >&2; exit 1; }
echo "ok   the old /admin/ of the user site redirects to the console's domain"

echo "== the API wants a session and the CSRF header"
acall GET /admin/v1/me ""
expect 401 ADMIN_UNAUTHORIZED "no session"
acall POST /admin/v1/login '{"email":"nobody@example.com","password":"x","totp_code":"000000"}'
expect 403 ADMIN_CSRF "a write without X-Admin-CSRF"
acall POST /admin/v1/login '{"email":"nobody@example.com","password":"a wrong password","totp_code":"000000"}' "${CSRF[@]}"
expect 401 ADMIN_LOGIN_FAILED "an unknown administrator"
call GET /v1/admin/v1/me ""
[[ $STATUS == 404 ]] || { echo "FAIL the gateway answers for the console: $STATUS" >&2; exit 1; }
echo "ok   the user gateway does not route the console"

echo "== four administrators for this run"
ROLES=(ADMIN OPERATOR FINANCE AUDITOR)
for role in "${ROLES[@]}"; do
  lower=$(tr '[:upper:]' '[:lower:]' <<<"$role")
  email="e2e-$lower-$RUN@example.com"
  pw=$(password)
  sec=$(secret)
  # A connection dropped after the account was made is retried by remote:
  # the second try finds it there, with this run's secrets (the email is
  # this run's).
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin create --email $email --name 'e2e $lower' --role $role --secrets-stdin" \
    "$(printf '%s\n%s\n' "$pw" "$sec")" 2>&1 || true)
  grep -qE "^created .* $email \($role\)|ADMIN_EXISTS" <<<"$out" || { echo "FAIL admin create $role: $out" >&2; exit 1; }
  eval "EMAIL_$role=\$email PW_$role=\$pw SECRET_$role=\$sec"
  # shellcheck disable=SC2016 # expanded when the script ends
  at_exit "remote \"sudo docker compose \$COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $email --reason 'e2e run over'\" >/dev/null"
  echo "ok   $role $email"
done

# login ROLE signs in, keeping the session in the role's cookie jar, and
# sets CODE_ROLE; login ROLE CODE tries a given code with a throwaway jar
# (curl -c rewrites the jar with what it saw, which would drop the session).
login() {
  local role=$1 email pw sec code jar
  email=$(eval "echo \$EMAIL_$role") pw=$(eval "echo \$PW_$role") sec=$(eval "echo \$SECRET_$role")
  if [[ -n ${2:-} ]]; then
    code=$2 jar="$WORK/other.jar"
  else
    code=$(totp "$sec") jar="$WORK/$role.jar"
    eval "CODE_$role=\$code"
  fi
  acall POST /admin/v1/login "$(jq -nc --arg e "$email" --arg p "$pw" --arg c "$code" '{email: $e, password: $p, totp_code: $c}')" \
    "${CSRF[@]}" -c "$jar" -D "$WORK/$role.headers"
}
# KEY is the next write's Idempotency-Key, which every request that moves
# money carries (C5.5 ⑥): a new one unless set; none with KEY=none.
KEY=
as() { # as ROLE METHOD PATH JSON: a call with the role's session (a write with an Idempotency-Key)
  local role=$1 key=${KEY:-e2e-$(password)}
  shift
  KEY=
  if [[ $1 == GET || $key == none ]]; then
    acall "$1" "$2" "$3" -b "$WORK/$role.jar" "${CSRF[@]}"
  else
    acall "$1" "$2" "$3" -b "$WORK/$role.jar" "${CSRF[@]}" -H "Idempotency-Key: $key"
  fi
}

echo "== sign-in"
acall GET /admin/v1/login-options ""
expect 200 - "the sign-in options need no session"
TOTP_REQUIRED=$(jq -r .totp_required <<<"$BODY")
login ADMIN
expect 200 - "ADMIN signs in with password and code"
check ".admin.role == \"ADMIN\" and (.admin.permissions | length) == 28" "with every permission"
cookie=$(grep -i '^set-cookie: admin_session=' "$WORK/ADMIN.headers")
for attr in 'Path=/admin/' 'HttpOnly' 'Secure' 'SameSite=Strict'; do
  grep -qi "$attr" <<<"$cookie" || { echo "FAIL the session cookie lacks $attr: $cookie" >&2; exit 1; }
done
echo "ok   the cookie is HttpOnly, Secure, SameSite=Strict, Path=/admin/"
if [[ $TOTP_REQUIRED == true ]]; then
  login ADMIN "$CODE_ADMIN"
  expect 401 ADMIN_LOGIN_FAILED "the same code does not sign in twice"
else
  login ADMIN 000000
  expect 200 - "the password alone signs in while admin.login_without_totp is on"
fi
for role in OPERATOR FINANCE AUDITOR; do
  login "$role"
  expect 200 - "$role signs in"
done
as AUDITOR GET /admin/v1/me ""
expect 200 - "me"
check '.role == "AUDITOR" and (.permissions | index("flags.write")) == null and (.permissions | index("audit.read")) != null' "AUDITOR only reads"

echo "== roles"
as AUDITOR PUT /admin/v1/flags/market.reference_kline '{"enabled":true,"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "AUDITOR cannot switch a flag"
as OPERATOR POST /admin/v1/ledger/adjustments '{"user_id":"01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b","asset":"USDT","amount":"1","reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "OPERATOR cannot request an adjustment"
as FINANCE POST /admin/v1/users/01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b/cancel-orders '{"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "FINANCE cannot cancel orders"
as FINANCE GET "/admin/v1/withdrawals?status=CONFIRMED" ""
expect 200 - "FINANCE lists withdrawals"
check '.items | type == "array"' "the list is an array"
as OPERATOR POST /admin/v1/withdrawals/review-batch '{"ids":["01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b"],"approve":true,"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "OPERATOR reviews no batch"
as FINANCE POST /admin/v1/withdrawals/review-batch '{"ids":["01929c3e-7f3a-7d7e-8a1b-2c3d4e5f6a7b"],"approve":true,"reason":"e2e batch"}'
expect 200 - "FINANCE reviews a batch"
check '.results == [.results[0]] and .results[0].ok == false and .results[0].code == "COMMON_NOT_FOUND"' "an unknown withdrawal fails alone"
acall GET /admin/v1/me "" -b "$WORK/AUDITOR.jar"
expect 200 - "reads need no CSRF header"
acall PUT /admin/v1/flags/market.reference_kline '{"enabled":true,"reason":"e2e"}' -b "$WORK/OPERATOR.jar"
expect 403 ADMIN_CSRF "writes do, even with a session"

EMAIL="e2e-admin-user-$RUN@example.com"
DEVICE="e2e-admin-user-$RUN"
echo "== an account to act on: $EMAIL"
register "$EMAIL" "$DEVICE" "e2e admin user $RUN"
USER_ID=$(jq -r .user_id <<<"$BODY")
REFRESH=$(jq -r .refresh_token <<<"$BODY")
UAUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE /v1/orders "" "${UAUTH[@]}"'
as OPERATOR GET "/admin/v1/users/lookup?q=$(jq -rn --arg e "$EMAIL" '$e|@uri')" ""
expect 200 - "OPERATOR finds the account by email"
check ".user.id == \"$USER_ID\" and .user.status == \"ACTIVE\"" "the account"
as OPERATOR GET "/admin/v1/users/lookup?q=$USER_ID" ""
expect 200 - "and by ID"

echo "== the account's page: notes and tags"
as AUDITOR GET "/admin/v1/users/$USER_ID" ""
expect 200 - "the account"
check ".id == \"$USER_ID\" and .tags == []" "without tags"
as AUDITOR POST "/admin/v1/users/$USER_ID/notes" '{"body":"e2e"}'
expect 403 ADMIN_FORBIDDEN "AUDITOR writes no notes"
as FINANCE POST "/admin/v1/users/$USER_ID/notes" '{"body":"e2e: called about a deposit"}'
expect 201 - "FINANCE writes a note"
check ".admin_email == \"$EMAIL_FINANCE\"" "signed with the administrator's email"
as OPERATOR PUT "/admin/v1/users/$USER_ID/tags" '{"tags":["test","vip","TEST"]}'
expect 200 - "OPERATOR tags the account"
check '.tags == ["TEST","VIP"]' "upper case, once each, sorted"
as AUDITOR GET "/admin/v1/users/$USER_ID/notes" ""
expect 200 - "the notes"
check '(.items | length) == 1 and .items[0].body == "e2e: called about a deposit"' "the note"
as OPERATOR PUT "/admin/v1/users/$USER_ID/tags" '{"tags":["not a tag"]}'
expect 400 COMMON_INVALID_ARGUMENT "a tag is a code"
as OPERATOR PUT "/admin/v1/users/$USER_ID/tags" '{"tags":[]}'
expect 200 - "and removes the tags"

echo "== freeze and unfreeze"
as OPERATOR POST "/admin/v1/users/$USER_ID/status" '{"to":"FROZEN","reason":"SUSPICIOUS_LOGIN","note":"scripts/e2e/admin.sh"}'
expect 200 - "OPERATOR freezes the account"
check '.from == "ACTIVE" and .to == "FROZEN"' "ACTIVE → FROZEN"
# A status change sends the user's tokens to refresh (docs/runbook/accounts.md).
stale_token() {
  call GET /v1/user/profile "" "${UAUTH[@]}"
  [[ $STATUS == 401 && $(jq -r .code <<<"$BODY") == AUTH_TOKEN_EXPIRED ]]
}
refresh_user() { # refresh_user SCOPE
  eventually 30 "the user's token is sent to refresh" stale_token
  sleep 1 # tokens issued within a second of the change are stale too
  call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
  expect 200 - "refresh"
  check ".scope == \"$1\"" "a $1 token"
  REFRESH=$(jq -r .refresh_token <<<"$BODY")
  UAUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
}
refresh_user read
call GET /v1/user/profile "" "${UAUTH[@]}"
check '.status == "FROZEN"' "the user sees FROZEN"
as OPERATOR POST "/admin/v1/users/$USER_ID/status" '{"to":"CLOSED","reason":"USER_REQUEST"}'
expect 409 USER_STATUS_TRANSITION_INVALID "FROZEN cannot close directly"
as OPERATOR POST "/admin/v1/users/$USER_ID/status" '{"to":"ACTIVE","reason":"REVIEW_CLEARED"}'
expect 200 - "and unfreezes it"
refresh_user full
call GET /v1/user/profile "" "${UAUTH[@]}"
check '.status == "ACTIVE"' "ACTIVE again"

echo "== a pair's status (ETH-BTC): previewed, only by an ADMIN"
# Statuses are trading parameters (design 2026-10-02 §2 item 6): an ADMIN
# previews a move and confirms it with the preview's token; a halt takes
# effect at once, anything else waits settings.change_delay_seconds. The
# run halts and opens its own pair (LINK-BTC, below) and halts the
# perpetual ETH-USDT-PERP; ETH-BTC, which other checks trade on, is only
# previewed. The changes wait a minute during the run: the least the test
# server's admin-service allows (ADMIN_CHANGE_DELAY_FLOOR, 10 minutes
# elsewhere, C5.5 ⑩).
as AUDITOR GET /admin/v1/settings ""
check '.change_delay_floor_seconds == 60' "the test server lets the changes wait a minute"
DELAY_BEFORE=$(jq -r .change_delay_seconds <<<"$BODY")
TWO_PERSON=$(jq -r .two_person_approval <<<"$BODY")
if [[ $DELAY_BEFORE != 60 ]]; then
  as ADMIN PUT /admin/v1/settings '{"change_delay_seconds":60,"reason":"e2e changes wait a minute"}'
  expect 200 - "the run's changes of trading parameters wait a minute"
  at_exit "as ADMIN PUT /admin/v1/settings '{\"change_delay_seconds\":$DELAY_BEFORE,\"reason\":\"e2e cleanup\"}' >/dev/null"
fi
# confirm_status PATH TO REASON: an ADMIN previews a status move of the pair
# or contract at PATH and confirms it with the preview's token.
confirm_status() {
  local token
  as ADMIN POST "$1/status/preview" "{\"to\":\"$2\"}"
  [[ $STATUS == 200 ]] || return 0
  token=$(jq -r '.confirmation.token // ""' <<<"$BODY")
  as ADMIN POST "$1/status" "$(jq -nc --arg to "$2" --arg r "$3" --arg c "$token" '{to: $to, reason: $r, confirmation: $c}')"
}
status_is() { # status_is SYMBOL STATUS: the reference data say so
  as AUDITOR GET /admin/v1/instruments/config ""
  [[ $STATUS == 200 ]] && jq -e --arg s "$1" --arg st "$2" '[.pairs[], .contracts[]] | map(select(.symbol == $s)) | .[0].status == $st' <<<"$BODY" >/dev/null
}
as OPERATOR POST /admin/v1/instruments/pairs/ETH-BTC/status/preview '{"to":"HALT"}'
expect 403 ADMIN_FORBIDDEN "an OPERATOR moves no pair"
as ADMIN POST /admin/v1/instruments/pairs/ETH-BTC/status/preview '{"to":"HALT"}'
expect 200 - "ADMIN previews a halt"
check '.from == "TRADING" and .to == "HALT" and .immediate == true and .confirmation == null and (.open_orders | type) == "number"' \
  "at once, nothing to confirm; the orders resting on it counted"
as ADMIN POST /admin/v1/instruments/pairs/ETH-BTC/status/preview '{"to":"CANCEL_ONLY"}'
expect 200 - "and the way out"
check ".immediate == false and .delay_seconds == 60 and (.confirmation.token | length) > 40 and .two_person == $TWO_PERSON" "waits a minute once confirmed"
as ADMIN POST /admin/v1/instruments/pairs/ETH-BTC/status '{"to":"CANCEL_ONLY","reason":"e2e without the preview"}'
expect 409 ADMIN_CONFIRMATION_REQUIRED "not without the preview's confirmation"
as ADMIN POST /admin/v1/instruments/pairs/ETH-BTC/status/preview '{"to":"PREPARE"}'
expect 409 INSTRUMENT_STATUS_TRANSITION_INVALID "TRADING cannot go back to PREPARE"
as AUDITOR GET /admin/v1/instruments ""
expect 200 - "instruments"
check '(.pairs[] | select(.symbol == "ETH-BTC") | .status) == "TRADING" and (.assets | map(.asset_code) | index("ETH")) != null' "ETH-BTC still trades; assets are listed"

echo "== cancel every order of the account"
balance() {
  call GET /v1/account/balances "" "${UAUTH[@]}"
  jq -r --arg a "$1" '.balances[] | select(.asset == $a and .account_type == "SPOT") | "\(.available) \(.frozen)"' <<<"$BODY"
}
funded() { [[ $(balance BTC) == "0.1 0" ]]; }
eventually 40 "welcome funds arrived" funded
# A buy well under HOUSE's bid rests (every order trades against HOUSE).
call GET "/v1/market/ETH-BTC/depth?limit=5" ""
expect 200 - "ETH-BTC's book"
LOW=$(jq -r '.bids[0][0] | tonumber * 0.9 * 100000 | floor / 100000 | tostring' <<<"$BODY")
call POST /v1/orders "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\"}" "${UAUTH[@]}" -H "Idempotency-Key: e2e-admin-$RUN"
expect 202 - "the user rests a buy at $LOW"
ORDER=$(jq -r .order_id <<<"$BODY")
as OPERATOR POST "/admin/v1/users/$USER_ID/cancel-orders" '{"reason":"e2e cancel all"}'
expect 202 - "OPERATOR cancels all its orders"
canceled() {
  call GET "/v1/orders/$ORDER" "" "${UAUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == CANCELED ]]
}
eventually 40 "the order is CANCELED" canceled
eventually 20 "its funds are released" funded

echo "== a flag round trip (market.reference_kline)"
as OPERATOR GET /admin/v1/flags ""
expect 200 - "flags"
check '(.items | map(.key) | index("wallet.withdraw")) != null and (.items | map(.key) | index("derivatives.trading")) != null' "every known flag is listed"
BEFORE=$(jq -r '.items[] | select(.key == "market.reference_kline") | .enabled' <<<"$BODY")
FLIP=$([[ $BEFORE == true ]] && echo false || echo true)
as OPERATOR PUT /admin/v1/flags/market.reference_kline "{\"enabled\":$FLIP,\"reason\":\"e2e flip\"}"
expect 200 - "OPERATOR switches it to $FLIP"
check ".enabled == $FLIP and .updated_by == \"$EMAIL_OPERATOR\"" "the change names the administrator"
as OPERATOR PUT /admin/v1/flags/market.reference_kline "{\"enabled\":$BEFORE,\"reason\":\"e2e restore\"}"
expect 200 - "and back to $BEFORE"
as OPERATOR PUT /admin/v1/flags/no.such.flag '{"enabled":true,"reason":"e2e"}'
expect 404 COMMON_NOT_FOUND "an unknown flag"

echo "== a two-person ledger adjustment"
call GET /v1/account/balances "" "${UAUTH[@]}"
USDT_BEFORE=$(jq -r '.balances[] | select(.asset == "USDT" and .account_type == "SPOT") | .available' <<<"$BODY")
USDT_BEFORE=${USDT_BEFORE:-0}
as FINANCE POST /admin/v1/ledger/adjustments "{\"user_id\":\"$USER_ID\",\"asset\":\"usdt\",\"amount\":\"1.5\",\"reason\":\"e2e goodwill\"}"
expect 201 - "FINANCE requests +1.5 USDT"
check '.status == "PENDING" and .payload.asset == "USDT" and .payload.amount == "1.5"' "PENDING"
APPROVAL=$(jq -r .id <<<"$BODY")
# If a check fails before a decision, the request does not stay pending.
at_exit "as ADMIN POST /admin/v1/approvals/$APPROVAL/decide '{\"approve\":false,\"reason\":\"e2e cleanup\"}'"
as FINANCE POST "/admin/v1/approvals/$APPROVAL/decide" '{"approve":true,"reason":"my own"}'
expect 403 ADMIN_SELF_APPROVAL "not by the requester"
as OPERATOR POST "/admin/v1/approvals/$APPROVAL/decide" '{"approve":true,"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "not by an OPERATOR"
as ADMIN GET "/admin/v1/approvals?status=PENDING" ""
check ".items | map(.id) | index(\"$APPROVAL\") != null" "ADMIN sees it pending"
as ADMIN POST "/admin/v1/approvals/$APPROVAL/decide" '{"approve":true,"reason":"checked by e2e"}'
expect 200 - "ADMIN approves"
check '.status == "EXECUTED" and (.result | startswith("journal ")) and .decided_by != null' "EXECUTED with the journal"
as ADMIN POST "/admin/v1/approvals/$APPROVAL/decide" '{"approve":false,"reason":"again"}'
expect 409 ADMIN_APPROVAL_DECIDED "decided once"
credited() {
  call GET /v1/account/balances "" "${UAUTH[@]}"
  jq -e --arg b "$USDT_BEFORE" '.balances[] | select(.asset == "USDT" and .account_type == "SPOT") | (.available | tonumber) == (($b | tonumber) + 1.5)' <<<"$BODY" >/dev/null
}
eventually 20 "the user has 1.5 USDT more" credited
as FINANCE POST /admin/v1/ledger/adjustments "{\"user_id\":\"$USER_ID\",\"asset\":\"USDT\",\"amount\":\"-1.5\",\"reason\":\"e2e reversal\"}"
expect 201 - "FINANCE requests the reversal"
REVERSAL=$(jq -r .id <<<"$BODY")
as ADMIN POST "/admin/v1/approvals/$REVERSAL/decide" '{"approve":false,"reason":"e2e keeps it"}'
expect 200 - "ADMIN rejects it"
check '.status == "REJECTED"' "REJECTED, nothing booked"

echo "== single-person mode and the settings"
as AUDITOR GET /admin/v1/settings ""
expect 200 - "every administrator reads the settings"
check '(.single_max_usdt | test("^[0-9.]+$")) and (.daily_max_usdt | test("^[0-9.]+$")) and (.daily_used_usdt | test("^[0-9.]+$"))' "the limits and the caller's use"
TWO_PERSON=$(jq -r .two_person_approval <<<"$BODY")
as FINANCE PUT /admin/v1/settings '{"single_max_usdt":"1","reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "only an ADMIN changes them"
as OPERATOR PUT /admin/v1/flags/admin.two_person_approval '{"enabled":true,"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "nor does an OPERATOR switch two-person approval as a flag"
as ADMIN PUT /admin/v1/settings '{"single_max_usdt":"600000000","reason":"e2e"}'
expect 400 COMMON_INVALID_ARGUMENT "a single limit above the day's"
if [[ $TWO_PERSON == false ]]; then
  call GET /v1/account/balances "" "${UAUTH[@]}"
  USDT_BEFORE=$(jq -r '.balances[] | select(.asset == "USDT" and .account_type == "SPOT") | .available' <<<"$BODY")
  USDT_BEFORE=${USDT_BEFORE:-0}
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" "{\"asset\":\"usdt\",\"amount\":\"2.5\",\"reason\":\"e2e single credit\",\"reference\":\"e2e-$RUN\"}"
  expect 201 - "ADMIN credits 2.5 USDT alone"
  check '.status == "EXECUTED" and .mode == "SINGLE" and (.journal_id | type) == "string" and .value_usdt == "2.5" and .escalation == ""' "booked at once, with its journal"
  plus() {
    call GET /v1/account/balances "" "${UAUTH[@]}"
    jq -e --arg b "$USDT_BEFORE" --arg d "$1" '.balances[] | select(.asset == "USDT" and .account_type == "SPOT") | (.available | tonumber) == (($b | tonumber) + ($d | tonumber))' <<<"$BODY" >/dev/null
  }
  eventually 20 "the user has 2.5 USDT more" plus 2.5
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"-2.5","reason":"e2e single reversal"}'
  expect 201 - "and takes it back"
  check '.status == "EXECUTED" and .mode == "SINGLE"' "booked"
  eventually 20 "the balance is back" plus 0
  KEY=none
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"1","reason":"e2e without a key"}'
  expect 400 COMMON_INVALID_ARGUMENT "a request that moves money wants an Idempotency-Key"
  KEY="e2e-once-$RUN"
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"1.25","reason":"e2e keyed credit"}'
  expect 201 - "ADMIN credits 1.25 USDT under a key"
  ONCE=$(jq -r .id <<<"$BODY")
  KEY="e2e-once-$RUN"
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"1.25","reason":"e2e keyed credit"}'
  expect 201 - "the same request again (a retry)"
  check ".id == \"$ONCE\" and .status == \"EXECUTED\"" "answers with the same operation"
  KEY="e2e-once-$RUN"
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"2","reason":"e2e keyed credit"}'
  expect 409 COMMON_IDEMPOTENCY_CONFLICT "the key with another amount is refused"
  eventually 20 "the user has 1.25 USDT more, once" plus 1.25
  as ADMIN POST "/admin/v1/approvals/$ONCE/decide" '{"approve":true,"reason":"e2e a decision repeated"}'
  expect 200 - "its decider's approval again (a retry)"
  check ".id == \"$ONCE\" and .status == \"EXECUTED\"" "answers with it as it stands, booking nothing more"
  as ADMIN POST "/admin/v1/approvals/$ONCE/decide" '{"approve":false,"reason":"e2e the other decision"}'
  expect 409 ADMIN_APPROVAL_DECIDED "the other decision is refused"
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"-1.25","reason":"e2e keyed reversal"}'
  expect 201 - "and takes it back"
  eventually 20 "the balance is back again" plus 0
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"asset":"USDT","amount":"100000000","reason":"e2e above the limit"}'
  expect 201 - "an adjustment above the single-operation limit"
  check '.status == "PENDING" and .mode == "TWO_PERSON" and .escalation == "SINGLE_LIMIT"' "waits for a second administrator"
  BIG=$(jq -r .id <<<"$BODY")
  at_exit "as ADMIN POST /admin/v1/approvals/$BIG/decide '{\"approve\":false,\"reason\":\"e2e cleanup\"}'"
  as ADMIN POST "/admin/v1/approvals/$BIG/decide" '{"approve":true,"reason":"my own"}'
  expect 403 ADMIN_SELF_APPROVAL "not approved by its requester"
  as ADMIN POST "/admin/v1/approvals/$BIG/decide" '{"approve":false,"reason":"e2e withdraws it"}'
  expect 200 - "who may withdraw it"
  check '.status == "REJECTED" and .decided_by_email != null' "REJECTED, nothing booked"
else
  echo "skip the single-person adjustments: two-person approval is on"
fi
as ADMIN GET /admin/v1/todo ""
expect 200 - "the counts waiting"
check '(.withdrawals | type) == "number" and (.approvals | type) == "number" and (.deposits | type) == "number" and (.partial | length) == 0' \
  "withdrawals, fund operations and deposits"
events=$(curl -sN --max-time 4 -b "$WORK/ADMIN.jar" "$ADMIN_BASE/admin/v1/events" || true)
grep -q '^event: todo' <<<"$events" || { echo "FAIL the event stream: $events" >&2; exit 1; }
echo "ok   the event stream pushes the counts"

echo "== perpetual contracts"
as AUDITOR GET /admin/v1/derivatives/contracts ""
expect 200 - "contracts"
check '([.contracts[].symbol] | contains(["BTC-USDT-PERP","ETH-USDT-PERP"])) and all(.contracts[]; (.open_interest | test("^[0-9.]+$")) and (.reduce_only | type) == "boolean")' "each with its reduce-only state and open interest"
as FINANCE POST /admin/v1/derivatives/contracts/BTC-USDT-PERP/lift-reduce-only '{"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "FINANCE cannot lift reduce-only"
as OPERATOR POST /admin/v1/derivatives/contracts/BTC-USDT-PERP/lift-reduce-only '{"reason":"e2e check"}'
expect 200 - "OPERATOR may lift it"
check '.symbol == "BTC-USDT-PERP" and (.lifted | type) == "boolean"' "lifted says whether it was on"
as OPERATOR POST /admin/v1/derivatives/contracts/ETH-USDT-PERP/status '{"to":"HALT","reason":"e2e halt"}'
expect 403 ADMIN_FORBIDDEN "an OPERATOR halts no contract"
PERP_RESUMED=""
if [[ $TWO_PERSON == false ]]; then
  as ADMIN POST /admin/v1/derivatives/contracts/ETH-USDT-PERP/status '{"to":"HALT","reason":"e2e halt"}'
  expect 200 - "ADMIN halts ETH-USDT-PERP at once"
  check '.from == "TRADING" and .to == "HALT" and .change == null' "TRADING → HALT"
  # shellcheck disable=SC2016 # a safety net: the contract trades again a minute after whatever happens
  at_exit 'status_is ETH-USDT-PERP HALT && confirm_status /admin/v1/derivatives/contracts/ETH-USDT-PERP TRADING "e2e cleanup" >/dev/null'
  confirm_status /admin/v1/derivatives/contracts/ETH-USDT-PERP TRADING "e2e resume"
  expect 202 - "and confirms its resume"
  check '.from == "HALT" and .to == "TRADING" and .change.status == "SCHEDULED" and .change.kind == "CONTRACT_STATUS" and .change.target == "contract:ETH-USDT-PERP"' \
    "which waits its minute"
  PERP_RESUMED=$(jq -r .change.id <<<"$BODY")
else
  echo "skip a contract's halt and resume: two-person approval is on and this run has one ADMIN"
fi
as AUDITOR GET /admin/v1/instruments ""
check '[.contracts[].symbol] | index("ETH-USDT-PERP") != null' "the instruments list the contracts"
as AUDITOR GET /admin/v1/derivatives/risk ""
expect 200 - "positions near liquidation"
check '.positions | type == "array"' "a list"
as AUDITOR GET "/admin/v1/derivatives/liquidations?days=30" ""
expect 200 - "liquidation steps"
check '.items | type == "array"' "a list"
as AUDITOR GET "/admin/v1/derivatives/liquidations?kind=SIDEWAYS" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown kind"
as AUDITOR GET "/admin/v1/derivatives/liquidations?days=7&kind=FILLED&symbol=ETH-USDT-PERP" ""
expect 200 - "liquidation steps of a kind and a contract"
if [[ $(jq '.items | length' <<<"$BODY") -gt 0 ]]; then
  check 'all(.items[]; .kind == "FILLED" and .symbol == "ETH-USDT-PERP")' "only those"
else
  echo "skip only those: no ETH-USDT-PERP liquidation filled in the last 7 days to tell the filter by"
fi
as AUDITOR GET "/admin/v1/derivatives/liquidations?user_id=bob" ""
expect 400 COMMON_INVALID_ARGUMENT "a user that is no UUID"
as AUDITOR GET "/admin/v1/positions?watch=true" ""
expect 200 - "every user's positions at risk"
check '(.positions | type) == "array" and (.truncated | type) == "boolean" and all(.positions[]; .margin_ratio == null or (.margin_ratio | tonumber) >= 0)' \
  "a list, riskiest first"

echo "== a two-person insurance fund contribution"
as AUDITOR GET /admin/v1/derivatives/insurance-fund ""
expect 200 - "the insurance fund"
check '.asset == "USDT" and (.balance | test("^[0-9.]+$"))' "its USDT balance"
FUND_BEFORE=$(jq -r .balance <<<"$BODY")
as OPERATOR POST /admin/v1/derivatives/insurance-fund/contributions '{"amount":"1","reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "OPERATOR cannot request one"
as FINANCE POST /admin/v1/derivatives/insurance-fund/contributions '{"amount":"-1","reason":"e2e"}'
expect 400 COMMON_INVALID_ARGUMENT "a contribution is positive"
as FINANCE POST /admin/v1/derivatives/insurance-fund/contributions '{"amount":"1","reason":"e2e contribution"}'
expect 201 - "FINANCE requests 1 USDT"
check '.kind == "INSURANCE_FUND" and .payload.asset == "USDT" and .payload.amount == "1" and .status == "PENDING"' "PENDING"
CONTRIBUTION=$(jq -r .id <<<"$BODY")
at_exit "as ADMIN POST /admin/v1/approvals/$CONTRIBUTION/decide '{\"approve\":false,\"reason\":\"e2e cleanup\"}'"
as FINANCE POST "/admin/v1/approvals/$CONTRIBUTION/decide" '{"approve":true,"reason":"my own"}'
expect 403 ADMIN_SELF_APPROVAL "not by the requester"
as ADMIN POST "/admin/v1/approvals/$CONTRIBUTION/decide" '{"approve":true,"reason":"checked by e2e"}'
expect 200 - "ADMIN approves"
check '.status == "EXECUTED" and (.result | startswith("journal "))' "EXECUTED with the journal"
fund_grew() {
  as AUDITOR GET /admin/v1/derivatives/insurance-fund ""
  jq -e --arg b "$FUND_BEFORE" '(.balance | tonumber) == (($b | tonumber) + 1)' <<<"$BODY" >/dev/null
}
eventually 20 "the fund holds 1 USDT more" fund_grew

echo "== reports from the ClickHouse read models"
as AUDITOR GET "/admin/v1/reports/trading?days=30" ""
expect 200 - "trading report"
check '.items | type == "array" and all(.[]; (.volume | test("^[0-9.]+$")) and (.trades >= 0))' "per symbol and day, amounts as decimal strings"
as AUDITOR GET "/admin/v1/reports/wallet?days=30" ""
expect 200 - "wallet report"
check '.items | type == "array"' "per asset and day"
as AUDITOR GET "/admin/v1/reports/candles?symbol=ETH-BTC&interval=1d&limit=5" ""
expect 200 - "daily ETH-BTC candles"
check '.items | type == "array" and length <= 5' "at most the limit"
as AUDITOR GET "/admin/v1/reports/candles?symbol=ETH-BTC&interval=2h" ""
expect 400 COMMON_INVALID_ARGUMENT "an interval the report does not offer"
as AUDITOR GET "/admin/v1/reports/derivatives?days=30" ""
expect 200 - "contract report"
check '.items | type == "array" and all(.[]; (.notional | test("^[0-9.]+$")) and (.liquidations >= 0))' "per contract and day"
as AUDITOR GET /admin/v1/reports/open-interest ""
expect 200 - "open interest"
check '.items | type == "array" and all(.[]; .long == .short)' "long equals short per contract"
TODAY=$(date -u +%Y-%m-%d)
as AUDITOR GET "/admin/v1/reports/trading?from=$(jq -nr 'now - 40 * 86400 | strftime("%Y-%m-%d")')&to=$TODAY&bucket=month" ""
expect 200 - "trading report between two dates, by month"
check 'all(.items[]; .day | endswith("-01"))' "each month on its first day"
as AUDITOR GET "/admin/v1/reports/wallet?days=7&bucket=year" ""
expect 400 COMMON_INVALID_ARGUMENT "a bucket the reports do not offer"
as AUDITOR GET "/admin/v1/reports/trading?from=$TODAY&to=2026-01-01" ""
expect 400 COMMON_INVALID_ARGUMENT "a period that ends before it starts"
# The run registered and signed its user in today; the bots and HOUSE are
# left out.
as AUDITOR GET "/admin/v1/reports/users?days=7" ""
expect 200 - "the users' activity"
check "(.items | length) == 7 and .partial == [] and .items[-1].day == \"$TODAY\" and .items[-1].registered >= 1 and .items[-1].signed_in >= 1" \
  "a day each, today with this run's user, the bots known"
check '[.items[].total] as $t | all(range(1; $t | length); $t[.] >= $t[. - 1])' "the running total never falls"
as AUDITOR GET "/admin/v1/reports/house-pnl?days=14&bucket=week" ""
expect 200 - "HOUSE's result by week"
check '(.items | length) >= 2 and all(.items[]; (.day | strptime("%Y-%m-%d") | mktime | strftime("%u")) == "1" and (.total | test("^-?[0-9]+[.][0-9]{2}$")))' \
  "each week from its Monday, in USDT"
check '(.unpriced | type) == "array" and ((.items[-1].cumulative | tonumber) - ([.items[].total | tonumber] | add) | fabs) < 0.05' \
  "the running sum adds up the buckets"

echo "== an asset's profile (LINK)"
as AUDITOR GET /admin/v1/assets/LINK/profile ""
expect 200 - "every administrator reads an asset's profile"
PROFILE=$BODY
check '(.description | type) == "object" and (.links | type) == "object" and (.version | type) == "number"' "its text, links and version"
profile_body() { # profile_body ENGLISH REASON: LINK's profile with another English introduction (none when empty)
  jq -c --arg en "$1" --arg r "$2" \
    '{display_name, links, reason: $r, description: (if $en == "" then .description | del(.en) else .description + {en: $en} end)}' <<<"$PROFILE"
}
EN_BEFORE=$(jq -r '.description.en // ""' <<<"$PROFILE")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'as ADMIN PUT /admin/v1/assets/LINK/profile "$(profile_body "$EN_BEFORE" "e2e cleanup")" >/dev/null'
as AUDITOR PUT /admin/v1/assets/LINK/profile "$(profile_body x "e2e reads only")"
expect 403 ADMIN_FORBIDDEN "AUDITOR edits no profile"
as OPERATOR PUT /admin/v1/assets/LINK/profile "$(profile_body "Chainlink connects contracts to the world's data (e2e $RUN)." "e2e edits the introduction")"
expect 200 - "OPERATOR edits LINK's English introduction"
check "(.version > $(jq .version <<<"$PROFILE")) and .logo_mime == $(jq .logo_mime <<<"$PROFILE") and .logo_size == $(jq .logo_size <<<"$PROFILE")" \
  "a new version, the logo kept"
profiled() {
  call GET /v1/market/assets ""
  [[ $STATUS == 200 ]] && jq -e --arg run "$RUN" '.assets[] | select(.asset_code == "LINK") | (.description.en // "") | contains($run)' <<<"$BODY" >/dev/null
}
eventually 50 "the sites read it within a minute" profiled
as OPERATOR PUT /admin/v1/assets/LINK/profile "$(profile_body "$EN_BEFORE" "e2e puts the introduction back")"
expect 200 - "and puts it back"

echo "== the simulated market (ASTRA)"
# Nothing here moves the live price: the events start tomorrow and are
# canceled, the settings go back, the change beyond one operator's share is
# rejected, the mint is a cent per bot.
as AUDITOR GET /admin/v1/sim ""
expect 200 - "every administrator reads the simulated market"
check '(.symbol | test("^[A-Z0-9]+-USDT$")) and (.bots | length) >= 1 and (.params.p0 | type) == "number" and (.version | type) == "number"' \
  "its pair, its bots, its settings and their version"
SIM=$BODY
SIM_PAIR=$(jq -r .symbol <<<"$SIM")
BOTS=$(jq '.bots | length' <<<"$SIM")
as AUDITOR GET "/admin/v1/sim/history?minutes=10" ""
expect 200 - "the target and the last price"
check '(.items | length) >= 1 and all(.items[]; .target_price | test("^[0-9.]+$"))' "over the last minutes"
as AUDITOR POST /admin/v1/sim/impact "$(jq -nc --arg t "$(jq -r .target_price <<<"$SIM")" '{price: (($t | tonumber) * 0.9 * 10000 | floor / 10000 | tostring)}')"
expect 200 - "what a 10% fall would do to the perpetual"
check '(.longs | type) == "number" and (.shorts | type) == "number" and (.liquidated | type) == "number" and (.insurance_cost | test("^[0-9.]+$"))' \
  "its longs and shorts, those liquidated, the insurance fund's share"
TOMORROW=$(jq -nr 'now + 20 * 3600 | strftime("%Y-%m-%dT%H:%M:%SZ")')
sim_event() { # sim_event SIZE REASON: a jump starting tomorrow
  jq -nc --arg at "$TOMORROW" --argjson s "$1" --arg r "$2" '{type: "JUMP", size: $s, starts_at: $at, reason: $r}'
}
as FINANCE POST /admin/v1/sim/events "$(sim_event 0.02 "e2e: finance moves prices")"
expect 403 ADMIN_FORBIDDEN "FINANCE moves no price"
as OPERATOR POST /admin/v1/sim/events "$(sim_event 0.02 "e2e: a small jump tomorrow")"
expect 201 - "OPERATOR schedules a 2% jump, within one operator's share"
check ".event.status == \"SCHEDULED\" and .event.created_by == \"$EMAIL_OPERATOR\" and .event.approved_by == \"\"" "in the operator's name"
SMALL_JUMP=$(jq -r .event.id <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'as OPERATOR POST "/admin/v1/sim/events/$SMALL_JUMP/end" "{\"reason\":\"e2e cleanup\"}" >/dev/null'
as OPERATOR POST "/admin/v1/sim/events/$SMALL_JUMP/end" '{"reason":"e2e: not needed after all"}'
expect 200 - "and cancels it"
check '.status == "CANCELED"' "before it starts"
as OPERATOR POST /admin/v1/sim/events "$(sim_event 0.35 "e2e: a 35% jump tomorrow, to be canceled")"
expect 202 - "a 35% jump is beyond one operator's share"
check '.approval.kind == "SIM_EVENT" and .approval.status == "PENDING" and .approval.escalation == "SIM_SHARE" and .approval.payload.move == "0.35"' \
  "it waits for a second administrator, with the move market-sim measured"
BIG_JUMP=$(jq -r .approval.id <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'as ADMIN POST "/admin/v1/approvals/$BIG_JUMP/decide" "{\"approve\":false,\"reason\":\"e2e cleanup\"}" >/dev/null'
as OPERATOR POST "/admin/v1/approvals/$BIG_JUMP/decide" '{"approve":true,"reason":"e2e approves its own"}'
expect 403 ADMIN_SELF_APPROVAL "not approved by the operator who asked"
as FINANCE POST "/admin/v1/approvals/$BIG_JUMP/decide" '{"approve":true,"reason":"e2e: finance approves"}'
expect 403 ADMIN_FORBIDDEN "nor by FINANCE"
as ADMIN POST "/admin/v1/approvals/$BIG_JUMP/decide" '{"approve":true,"reason":"e2e: approved, to be canceled"}'
expect 200 - "ADMIN approves it"
check '.status == "EXECUTED" and (.result | startswith("event "))' "market-sim scheduled it"
BIG_EVENT=$(jq -r '.result | ltrimstr("event ")' <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'as OPERATOR POST "/admin/v1/sim/events/$BIG_EVENT/end" "{\"reason\":\"e2e cleanup\"}" >/dev/null'
as AUDITOR GET "/admin/v1/sim/events?all=true&limit=20" ""
check "[.items[] | select(.id == \"$BIG_EVENT\")][0] | .status == \"SCHEDULED\" and .created_by == \"$EMAIL_OPERATOR\" and .approved_by == \"$EMAIL_ADMIN\"" \
  "in the operator's name, approved by the ADMIN"
as OPERATOR POST "/admin/v1/sim/events/$BIG_EVENT/end" '{"reason":"e2e: the drill is over"}'
expect 200 - "and canceled before it starts"
SIM_PARAMS=$(jq -c .params <<<"$SIM")
as AUDITOR PUT /admin/v1/sim/params "$(jq -c '{params: ., reason: "e2e reads only"}' <<<"$SIM_PARAMS")"
expect 403 ADMIN_FORBIDDEN "AUDITOR changes no settings"
# The settings go back if the run stops before it puts them back itself.
restore_sim_params() { as OPERATOR PUT /admin/v1/sim/params "$(jq -c '{params: ., reason: "e2e cleanup"}' <<<"$SIM_PARAMS")" >/dev/null; }
at_exit restore_sim_params
as OPERATOR PUT /admin/v1/sim/params "$(jq -c '{params: (. + {sigma: (.sigma + 0.01)}), reason: "e2e: a little more volatility"}' <<<"$SIM_PARAMS")"
expect 200 - "OPERATOR changes the volatility, within its share"
check ".version > $(jq .version <<<"$SIM")" "a new version of the settings"
as OPERATOR PUT /admin/v1/sim/params "$(jq -c '{params: ., reason: "e2e puts the volatility back"}' <<<"$SIM_PARAMS")"
expect 200 - "and puts it back"
as OPERATOR PUT /admin/v1/sim/params "$(jq -c '{params: (. + {p0: (.p0 * 1.5)}), reason: "e2e: an anchor 50% higher"}' <<<"$SIM_PARAMS")"
expect 202 - "an anchor 50% higher is beyond one operator's share"
check '.approval.kind == "SIM_PARAMS" and .approval.status == "PENDING"' "it waits for a second administrator"
as ADMIN POST "/admin/v1/approvals/$(jq -r .approval.id <<<"$BODY")/decide" '{"approve":false,"reason":"e2e: no such move"}'
expect 200 - "ADMIN rejects it"
check '.status == "REJECTED"' "and nothing changes"
as AUDITOR GET /admin/v1/sim/token ""
expect 200 - "who holds the coin"
check ".asset == \"${SIM_PAIR%-USDT}\" and .bots.holders == $BOTS and (.issued | tonumber) > 0 and (.top | length) >= 1" \
  "every bot holds some; what was issued; the largest holders"
check '((.bots.amount | tonumber) + (.users.amount | tonumber) + ([.platform[].amount | tonumber] | add // 0) - (.issued | tonumber) | fabs) < 0.001' \
  "the bots, the users and the platform hold all that was issued"
as OPERATOR POST /admin/v1/sim/mint '{"asset":"USDT","amount":"1","reason":"e2e: operators print no money"}'
expect 403 ADMIN_FORBIDDEN "an OPERATOR mints nothing"
as FINANCE POST /admin/v1/sim/mint "$(jq -nc --argjson n "$BOTS" '{asset: "USDT", amount: ($n / 100 | tostring), reason: "e2e: a cent for every bot"}')"
expect 201 - "FINANCE mints a cent of USDT for every bot"
check "(.payload.bots | fromjson | length) == $BOTS and all(.payload.bots | fromjson | .[]; .amount == \"0.01\")" "one share per bot"
if [[ $TWO_PERSON == false ]]; then
  check '.status == "EXECUTED" and .mode == "SINGLE" and (.journal_id | type) == "string"' "booked at once in single-person mode"
else
  as ADMIN POST "/admin/v1/approvals/$(jq -r .id <<<"$BODY")/decide" '{"approve":true,"reason":"e2e: a cent each"}'
  expect 200 - "ADMIN approves it"
fi
check ".status == \"EXECUTED\" and (.result | startswith(\"$BOTS adjustments\"))" "one adjustment per bot"
as FINANCE POST /admin/v1/sim/mint '{"asset":"USDT","amount":"200000","role":"MAKER","reason":"e2e: beyond the single limit"}'
expect 201 - "200,000 USDT for the makers"
check '.status == "PENDING" and .mode == "TWO_PERSON" and .payload.role == "MAKER"' "waits for a second administrator"
BIG_MINT=$(jq -r .id <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'as ADMIN POST "/admin/v1/approvals/$BIG_MINT/decide" "{\"approve\":false,\"reason\":\"e2e cleanup\"}" >/dev/null'
as ADMIN POST "/admin/v1/approvals/$BIG_MINT/decide" '{"approve":false,"reason":"e2e: no such sum"}'
expect 200 - "ADMIN rejects it"
check '.status == "REJECTED"' "nothing booked"
as AUDITOR GET "/admin/v1/orders?symbol=$SIM_PAIR&accounts=bots&limit=5" ""
expect 200 - "the bots' orders"
check '(.items | length) >= 1 and all(.items[]; .bot)' "only bots', each marked"
as AUDITOR GET "/admin/v1/orders?accounts=users&limit=5" ""
expect 200 - "everyone else's orders"
check 'all(.items[]; .bot | not)' "no bot among them"
as AUDITOR GET "/admin/v1/trades?symbol=$SIM_PAIR&accounts=bots&limit=5" ""
expect 200 - "the trades between bots"
check '(.items | length) >= 1 and all(.items[]; .buyer_bot and .seller_bot)' "both sides marked"
as AUDITOR GET "/admin/v1/trades?accounts=robots" ""
expect 400 COMMON_INVALID_ARGUMENT "bots or users, nothing else"

echo "== paged lists and the overview"
as AUDITOR GET "/admin/v1/users?limit=2" ""
expect 200 - "accounts, newest first"
check '(.items | length) == 2 and (.next_cursor | type) == "string" and (.items[0].created_at >= .items[1].created_at)' "a page of two with a cursor"
CURSOR=$(jq -r .next_cursor <<<"$BODY")
FIRST=$(jq -r '.items | map(.id) | join(",")' <<<"$BODY")
as AUDITOR GET "/admin/v1/users?limit=2&cursor=$CURSOR" ""
expect 200 - "the next page"
check "(.items | length) >= 1 and ([.items[].id] | map(. as \$i | \"$FIRST\" | contains(\$i)) | any | not)" "without the first page's accounts"
as AUDITOR GET "/admin/v1/users?status=GONE" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown status"
as AUDITOR GET "/admin/v1/users?cursor=not-a-cursor" ""
expect 400 COMMON_INVALID_ARGUMENT "a cursor the console did not make"
orders_listed() {
  as AUDITOR GET "/admin/v1/orders?user_id=$USER_ID" ""
  [[ $STATUS == 200 ]] && jq -e --arg o "$ORDER" '.items | map(.order_id) | index($o) != null' <<<"$BODY" >/dev/null
}
eventually 60 "the user's canceled order is in the orders view" orders_listed
check '.items[0].status == "CANCELED" and .items[0].symbol == "ETH-BTC"' "in its latest state"
as AUDITOR GET "/admin/v1/trades?symbol=ETH-BTC&limit=3" ""
expect 200 - "trades"
check '(.items | length) <= 3 and all(.items[]; .symbol == "ETH-BTC" and (.price | test("^[0-9.]+$")))' "of one symbol"
as AUDITOR GET "/admin/v1/deposits?limit=5" ""
expect 200 - "deposits"
check '.items | type == "array" and length <= 5' "a page"
as FINANCE GET "/admin/v1/withdrawals?status=ALL&limit=1" ""
expect 200 - "withdrawals of every status"
check '(.items | length) <= 1 and has("next_cursor")' "a page with its cursor"
as ADMIN GET "/admin/v1/approvals?limit=1" ""
check '(.items | length) == 1 and (.next_cursor | type) == "string"' "approvals page too"
as AUDITOR GET "/admin/v1/dashboard?days=7" ""
expect 200 - "the overview"
check '.users.total >= 1 and (.series | length) == 7 and (.partial | length) == 0 and (.feed.state | test("^(OK|DELAYED|DOWN|OFF)$"))' "accounts, a week of days, the feed; nothing missing"

echo "== the account's money: valued balances, a hold, a single cancel"
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 200 - "the user refreshes"
REFRESH=$(jq -r .refresh_token <<<"$BODY")
UAUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
as AUDITOR GET "/admin/v1/users/$USER_ID/balances" ""
expect 200 - "the balances, valued"
check '(.balances | length) >= 1 and .balances[0].account_type == "SPOT" and (.total_usdt | tonumber) > 0' "SPOT first, a total in USDT"
spot_usdt() { # spot_usdt FIELD: the user's SPOT USDT available or frozen
  as AUDITOR GET "/admin/v1/users/$USER_ID/balances" ""
  jq -r --arg f "$1" '[.balances[] | select(.account_type == "SPOT" and .asset == "USDT")][0][$f] // "0"' <<<"$BODY"
}
FROZEN_BEFORE=$(spot_usdt frozen)
as AUDITOR POST "/admin/v1/users/$USER_ID/holds" '{"asset":"USDT","amount":"1.5","reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "AUDITOR holds nothing"
KEY="e2e-hold-$RUN"
as FINANCE POST "/admin/v1/users/$USER_ID/holds" '{"asset":"USDT","amount":"1.5","reason":"e2e chargeback check"}'
expect 201 - "FINANCE holds 1.5 USDT"
check ".active == true and .amount == \"1.5\" and .actor == \"$EMAIL_FINANCE\"" "an active hold by FINANCE"
HOLD=$(jq -r .id <<<"$BODY")
KEY="e2e-hold-$RUN"
as FINANCE POST "/admin/v1/users/$USER_ID/holds" '{"asset":"USDT","amount":"1.5","reason":"e2e chargeback check"}'
expect 201 - "the same hold again (a retry)"
check ".id == \"$HOLD\"" "is the same hold"
[[ $(jq -n --arg a "$(spot_usdt frozen)" --arg b "$FROZEN_BEFORE" '($a | tonumber) - ($b | tonumber) == 1.5') == true ]] ||
  { echo "FAIL the hold is frozen: $FROZEN_BEFORE -> $(spot_usdt frozen)" >&2; exit 1; }
echo "ok   1.5 USDT more frozen"
call GET "/v1/account/ledger?asset=USDT&type=ADMIN_FREEZE" "" "${UAUTH[@]}"
expect 200 - "the user's fund flow"
check '(.items | length) == 2 and all(.items[]; .entry_type == "ADMIN_FREEZE")' "shows the hold (available to frozen)"
KEY="e2e-release-$RUN"
as FINANCE DELETE "/admin/v1/users/$USER_ID/holds/$HOLD" '{"reason":"e2e cleared"}'
expect 200 - "and releases it"
check '.active == false and .released_by != "" and .release_journal_id != null' "released"
KEY="e2e-release-$RUN"
as FINANCE DELETE "/admin/v1/users/$USER_ID/holds/$HOLD" '{"reason":"e2e cleared"}'
expect 200 - "the same release again (a retry)"
check ".id == \"$HOLD\" and .active == false" "answers with the released hold"
as FINANCE DELETE "/admin/v1/users/$USER_ID/holds/$HOLD" '{"reason":"e2e again"}'
expect 409 LEDGER_HOLD_RELEASED "a hold is released once"
[[ $(spot_usdt frozen) == "$FROZEN_BEFORE" ]] || { echo "FAIL the release: $FROZEN_BEFORE -> $(spot_usdt frozen)" >&2; exit 1; }
echo "ok   the frozen balance is back"
as AUDITOR GET "/admin/v1/users/$USER_ID/holds" ""
check "(.holds | length) == 1 and .holds[0].id == \"$HOLD\"" "the hold stays listed"
# A hold the console cannot release (part of its freeze released
# elsewhere): the operator releases the hold's own part of the frozen
# balance, never another order's or withdrawal's (C5.5 ⑧, ⑯).
KEY="e2e-hold-forced-$RUN"
as FINANCE POST "/admin/v1/users/$USER_ID/holds" '{"asset":"USDT","amount":"0.7","reason":"e2e forced release check"}'
expect 201 - "FINANCE holds 0.7 USDT"
FORCED=$(jq -r .id <<<"$BODY")
if out=$(exchangectl ledger release-hold --id "$FORCED" --amount 0.8 --reason "e2e more than the hold" 2>&1); then
  echo "FAIL release-hold released more than the hold: $out" >&2
  exit 1
fi
echo "ok   release-hold refuses more than the hold's part"
out=$(exchangectl ledger release-hold --id "$FORCED" --reason "e2e operator release" 2>&1) || { echo "FAIL release-hold: $out" >&2; exit 1; }
[[ $out == *"released: 0.7 of 0.7 USDT"* ]] || { echo "FAIL release-hold: $out" >&2; exit 1; }
echo "ok   the operator releases it: all of it is the hold's"
[[ $(spot_usdt frozen) == "$FROZEN_BEFORE" ]] || { echo "FAIL the forced release: $FROZEN_BEFORE -> $(spot_usdt frozen)" >&2; exit 1; }
echo "ok   the frozen balance is back"
as AUDITOR GET "/admin/v1/users/$USER_ID/holds" ""
check "any(.holds[]; .id == \"$FORCED\" and .active == false)" "listed released"
forced_audited() {
  as AUDITOR GET "/admin/v1/audit-logs?target=user:$USER_ID" ""
  [[ $STATUS == 200 ]] && jq -e --arg h "$FORCED" 'any(.items[]; .payload.action == "ledger.hold_released" and
    (.payload.details | contains($h) and contains("\"forced\":true")))' <<<"$BODY" >/dev/null
}
eventually 60 "audited as forced" forced_audited

call GET "/v1/market/ETH-BTC/depth?limit=5" ""
LOW=$(jq -r '.bids[0][0] | tonumber * 0.9 * 100000 | floor / 100000 | tostring' <<<"$BODY")
call POST /v1/orders "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\"}" "${UAUTH[@]}" -H "Idempotency-Key: e2e-admin-one-$RUN"
expect 202 - "the user rests a buy at $LOW"
ONE=$(jq -r .order_id <<<"$BODY")
as FINANCE POST "/admin/v1/users/$USER_ID/orders/$ONE/cancel" '{"reason":"e2e single cancel"}'
expect 403 ADMIN_FORBIDDEN "FINANCE cancels no orders"
as OPERATOR POST "/admin/v1/users/$USER_ID/orders/$ONE/cancel" '{"reason":"e2e single cancel"}'
expect 202 - "OPERATOR cancels that one order"
one_canceled() {
  call GET "/v1/orders/$ONE" "" "${UAUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == CANCELED ]]
}
eventually 40 "the order is canceled" one_canceled

if [[ $TWO_PERSON == false ]]; then
  echo "== a futures adjustment (single-person mode)"
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"account_type":"FUTURES","asset":"USDT","amount":"2","reason":"e2e futures credit"}'
  expect 201 - "ADMIN credits 2 USDT to FUTURES"
  check '.status == "EXECUTED" and .payload.account_type == "FUTURES"' "booked on FUTURES"
  futures_usdt() {
    call GET /v1/account/balances "" "${UAUTH[@]}"
    jq -r '[.balances[] | select(.account_type == "FUTURES" and .asset == "USDT")][0].available // "0"' <<<"$BODY"
  }
  [[ $(futures_usdt) == 2 ]] || { echo "FAIL futures balance $(futures_usdt), want 2" >&2; exit 1; }
  echo "ok   the user has 2 USDT in FUTURES"
  as ADMIN POST "/admin/v1/users/$USER_ID/adjustments" '{"account_type":"FUTURES","asset":"USDT","amount":"-2","reason":"e2e futures reversal"}'
  expect 201 - "and takes it back"
  check '.status == "EXECUTED"' "booked"
fi

echo "== a force close"
if [[ -n $PERP_RESUMED ]]; then
  # The resume confirmed with the contracts above has had its minute; the
  # console applies a change within 5 seconds of its time.
  eventually 180 "ETH-USDT-PERP trades again once its resume took effect" status_is ETH-USDT-PERP TRADING
  as AUDITOR GET "/admin/v1/instruments/changes?status=APPLIED&limit=20" ""
  check "any(.items[]; .id == \"$PERP_RESUMED\" and .applied_at != null and .requested_by_email == \"$EMAIL_ADMIN\")" "the change applied, in its requester's name"
fi
call POST /v1/account/transfers '{"asset":"USDT","amount":"100","from_account_type":"SPOT","to_account_type":"FUTURES"}' \
  "${UAUTH[@]}" -H "Idempotency-Key: e2e-admin-perp-$RUN"
expect 201 - "the user moves 100 USDT to FUTURES"
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'call DELETE "/v1/derivatives/orders?symbol=ETH-USDT-PERP" "" "${UAUTH[@]}"'
call POST /v1/derivatives/orders '{"symbol":"ETH-USDT-PERP","side":"BUY","type":"MARKET","quantity":"0.10"}' "${UAUTH[@]}"
if [[ $STATUS == 403 ]]; then
  echo "skip force close: contract trading is off ($(jq -r .code <<<"$BODY"))"
else
  expect 202 - "the user buys 0.1 ETH-USDT-PERP at the market"
  # A contract just resumed has no HOUSE book until the market maker sees
  # it trading again: an IOC buy then cancels unfilled, and is tried again.
  filled() { # filled ORDER_ID: it ended FILLED (polled until it ends)
    local i
    for i in $(seq 20); do
      call GET "/v1/derivatives/orders/$1" "" "${UAUTH[@]}"
      case $(jq -r .status <<<"$BODY") in
        FILLED) return 0 ;;
        CANCELED | REJECTED | EXPIRED) return 1 ;;
      esac
      sleep 0.5
    done
    return 1
  }
  perp_buy() {
    call POST /v1/derivatives/orders '{"symbol":"ETH-USDT-PERP","side":"BUY","type":"MARKET","quantity":"0.10"}' "${UAUTH[@]}"
    [[ $STATUS == 202 ]] && filled "$(jq -r .order_id <<<"$BODY")"
  }
  if ! filled "$(jq -r .order_id <<<"$BODY")"; then
    eventually 30 "a market buy fills once HOUSE quotes the resumed contract" perp_buy
  fi
  user_long() {
    as AUDITOR GET "/admin/v1/users/$USER_ID/positions" ""
    [[ $STATUS == 200 ]] && jq -e '.positions | length == 1 and .[0].quantity == "0.1"' <<<"$BODY" >/dev/null
  }
  eventually 40 "the console shows the long" user_long
  as AUDITOR GET "/admin/v1/positions?user_id=$USER_ID&symbol=eth-usdt-perp" ""
  expect 200 - "every user's positions, of this user"
  check '(.positions | length) == 1 and .positions[0].quantity == "0.1" and .positions[0].mark_price != null and (.house_user_id | type) == "string"' \
    "the long valued at the mark price; HOUSE's account named"
  HOUSE_ID=$(jq -r .house_user_id <<<"$BODY")
  as AUDITOR GET "/admin/v1/positions?user_id=$HOUSE_ID&symbol=ETH-USDT-PERP" ""
  expect 200 - "HOUSE's positions"
  check '(.positions | length) >= 1' "HOUSE holds the other side"
  as AUDITOR GET "/admin/v1/positions?symbol=ETH-USDT-PERP&limit=5000" ""
  expect 200 - "a limit beyond the most is clamped"
  check "(.positions | length) <= 500 and (.positions | map(.user_id == \"$HOUSE_ID\") | . == sort)" "HOUSE's positions after every user's"
  # What a debit of the FUTURES balance would leave of the cross margin,
  # shown before a negative adjustment (C5.5 ⑧, ⑯).
  as AUDITOR GET "/admin/v1/users/$USER_ID/futures-margin?debit=1" ""
  expect 200 - "the user's cross margin"
  check '.asset == "USDT" and .positions == 1 and (.unmeasured or (((.equity | tonumber) - (.equity_after | tonumber) - 1 | . * .) < 1e-12 and
    (.maintenance | tonumber) > 0 and .state == "HEALTHY"))' "the long, healthy; a debit of 1 USDT leaves 1 less"
  as AUDITOR GET "/admin/v1/users/$USER_ID/futures-margin?debit=-1" ""
  expect 400 COMMON_INVALID_ARGUMENT "a debit is positive"
  as FINANCE POST "/admin/v1/users/$USER_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e force close"}'
  expect 403 ADMIN_FORBIDDEN "FINANCE closes no positions"
  as OPERATOR POST "/admin/v1/users/$USER_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e force close"}'
  expect 200 - "OPERATOR closes it at the market"
  check '.type == "MARKET" and .reduce_only == true and .side == "SELL" and .quantity == "0.1"' "a reduce-only market sell of 0.1"
  check '.status == "FILLED" and .filled_quantity == "0.1"' "the console waited for it: filled in full against HOUSE"
  user_flat() {
    as AUDITOR GET "/admin/v1/users/$USER_ID/positions" ""
    [[ $STATUS == 200 ]] && jq -e '.positions | length == 0' <<<"$BODY" >/dev/null
  }
  eventually 40 "the position is closed" user_flat
  as OPERATOR POST "/admin/v1/users/$USER_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e again"}'
  expect 422 DERIV_NO_POSITION "nothing left to close"
  as OPERATOR POST "/admin/v1/users/$HOUSE_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e HOUSE"}'
  expect 422 DERIV_HOUSE_NOT_CLOSED "HOUSE's positions are not closed from the console"
  closed_audited() {
    as AUDITOR GET "/admin/v1/audit-logs?target=user:$USER_ID" ""
    [[ $STATUS == 200 ]] && jq -e '[.items[].payload.action] | (index("admin.derivatives.position_close_requested") != null and
      index("admin.derivatives.position_closed") != null)' <<<"$BODY" >/dev/null
  }
  eventually 60 "the close is audited when asked for and when it filled" closed_audited
fi

echo "== listing a pair from the console (LINK-BTC)"
as AUDITOR GET /admin/v1/instruments/config ""
expect 200 - "the reference data as a config document"
check '(.pairs | length) >= 50 and (.fee_schedules | map(.tier) | index("default")) != null and (.assets | map(.asset_code) | index("LINK")) != null' \
  "pairs, fee tiers and assets in the reference file's shape"
# LINK-BTC is the run's own pair, listed once and kept (C5.5 ⑩): it
# follows Binance's LINKBTC and HOUSE quotes it, its document is the same
# from run to run, and each run halts it at once and confirms its opening,
# so it trades again by the end.
# The document leaves out an empty reference symbol: "" is a pair that
# follows none, "none" no pair at all.
LINK_REF=$(jq -r 'first(.pairs[] | select(.symbol == "LINK-BTC") | .reference_symbol // "") // "none"' <<<"$BODY")
LINK_NOW=$(jq -r 'first(.pairs[] | select(.symbol == "LINK-BTC") | .status) // "none"' <<<"$BODY")
# HOUSE quotes every pair (the user's decision of 2026-10-02): LINK-BTC is
# appended once to market.house_liquidity's symbols, the list kept (an
# empty list already means every symbol).
as OPERATOR GET /admin/v1/flags ""
expect 200 - "the flags"
HOUSE_ALLOW=$(jq -r '[.items[] | select(.key == "market.house_liquidity") | .rules.symbols.allow // [] | .[]] | join(",")' <<<"$BODY")
if [[ -n $HOUSE_ALLOW && ,$HOUSE_ALLOW, != *,LINK-BTC,* ]]; then
  exchangectl flags set market.house_liquidity --allow-symbols "$HOUSE_ALLOW,LINK-BTC" --reason "e2e: HOUSE quotes LINK-BTC like every pair (C5.5 ⑩)" >/dev/null
  echo "ok   LINK-BTC joins HOUSE's symbols, the list kept"
fi
link_pair() { # link_pair [REFERENCE] [MULTIPLIER]: LINK-BTC as a config document
  jq -nc --arg r "${1-LINKBTC}" --arg m "${2:-1}" '{pairs: [{symbol: "LINK-BTC", base_asset: "LINK", quote_asset: "BTC", tick_size: "0.0000001",
    lot_size: "0.1", min_quantity: "0.1", max_quantity: "100000", min_notional: "0.0001", price_band: "0.1", fee_tier: "default", status: "PREPARE",
    reference_symbol: $r, reference_multiplier: $m}]}'
}
link_follows() { # LINK-BTC follows Binance's LINKBTC
  as AUDITOR GET /admin/v1/instruments/config ""
  [[ $STATUS == 200 ]] && jq -e '.pairs[] | select(.symbol == "LINK-BTC" and .reference_symbol == "LINKBTC")' <<<"$BODY" >/dev/null
}
as AUDITOR POST /admin/v1/instruments/preview "{\"config\":$(link_pair)}"
expect 403 ADMIN_FORBIDDEN "AUDITOR previews no change"
as OPERATOR POST /admin/v1/instruments/preview "{\"config\":$(link_pair NOPECOINBTC)}"
expect 422 ADMIN_REFERENCE_UNKNOWN "a reference symbol Binance does not list is refused"
# A new pair starts in PREPARE: one created trading would skip the guard of
# its opening (LINK-ETH is never created).
LINK_ETH=$(jq -nc '{pairs: [{symbol: "LINK-ETH", base_asset: "LINK", quote_asset: "ETH", tick_size: "0.000001", lot_size: "0.1",
  min_quantity: "0.1", max_quantity: "100000", min_notional: "0.001", price_band: "0.1", fee_tier: "default", status: "TRADING",
  reference_symbol: "", reference_multiplier: "1"}]}')
as ADMIN POST /admin/v1/instruments/preview "{\"config\":$LINK_ETH}"
expect 422 ADMIN_NEW_ITEM_NOT_PREPARE "a pair created trading is refused, even to an ADMIN"
case $LINK_REF in
  none)
    as OPERATOR POST /admin/v1/instruments/preview "{\"config\":$(link_pair)}"
    expect 200 - "a preview of LINK-BTC with Binance's LINKBTC"
    check '(.changes | length) == 1 and .changes[0].action == "CREATE" and .changes[0].after.reference_symbol == "LINKBTC" and
      ([.warnings[].code] | index("HOUSE_QUOTES") != null and index("HOUSE_NOT_LISTED") == null and index("STREAMS_RECONNECT") != null)' \
      "checked: HOUSE will quote it, the reference streams reconnect"
    as OPERATOR POST /admin/v1/instruments/apply "{\"config\":$(link_pair),\"reason\":\"e2e lists LINK-BTC\"}"
    expect 200 - "OPERATOR lists LINK-BTC at once (a new pair touches nobody)"
    check '(.changes | length) == 1 and .changes[0].action == "CREATE" and .changes[0].after.status == "PREPARE"' "created in PREPARE, versioned"
    LINK_REF=LINKBTC LINK_NOW=PREPARE
    ;;
  LINKBTC) ;;
  *)
    # Listed before it followed Binance: an ADMIN confirms its reference
    # once, and the run waits for it before its status moves (a move
    # changes the version the confirmation was bound to).
    if [[ $TWO_PERSON == false ]]; then
      as ADMIN POST /admin/v1/instruments/preview "{\"config\":$(link_pair)}"
      expect 200 - "ADMIN previews LINK-BTC following Binance's LINKBTC"
      check '([.warnings[].code] | index("HOUSE_QUOTES") != null and index("HOUSE_NOT_LISTED") == null and index("STREAMS_RECONNECT") != null)' \
        "checked: HOUSE will quote it, the reference streams reconnect"
      as ADMIN POST /admin/v1/instruments/apply "$(jq -nc --argjson c "$(link_pair)" --arg t "$(jq -r .guard.confirmation.token <<<"$BODY")" \
        '{config: $c, reason: "e2e LINK-BTC follows Binance", confirmation: $t}')"
      expect 202 - "and confirms it"
      eventually 180 "LINK-BTC follows Binance's LINKBTC once its change took effect" link_follows
      LINK_REF=LINKBTC
    else
      echo "skip LINK-BTC's reference: two-person approval is on and this run has one ADMIN"
    fi
    ;;
esac
if [[ $LINK_REF == LINKBTC ]]; then
  as OPERATOR POST /admin/v1/instruments/apply "{\"config\":$(link_pair),\"reason\":\"e2e again\"}"
  expect 200 - "the same document again"
  check '(.changes | length) == 0 and .unchanged == 1' "changes nothing"
  if [[ $LINK_NOW != PREPARE ]]; then
    check '[.warnings[] | select(.code == "STATUS_IGNORED" and .symbol == "LINK-BTC")] | length == 1' "its PREPARE noted: a document moves no status"
  fi
fi
pair_listed() {
  call GET /v1/market/pairs ""
  [[ $STATUS == 200 ]] && jq -e '.pairs[] | select(.symbol == "LINK-BTC")' <<<"$BODY" >/dev/null
}
eventually 60 "the sites list LINK-BTC" pair_listed
LINK_STATUS=$(jq -r '.pairs[] | select(.symbol == "LINK-BTC") | .status' <<<"$BODY")
# shellcheck disable=SC2016 # a safety net: LINK-BTC trades again a minute after whatever happens
at_exit 'status_is LINK-BTC HALT && confirm_status /admin/v1/instruments/pairs/LINK-BTC TRADING "e2e cleanup" >/dev/null'
LINK_OPENING=""
if [[ $TWO_PERSON == false ]]; then
  if [[ $LINK_STATUS == TRADING ]]; then
    as ADMIN POST /admin/v1/instruments/pairs/LINK-BTC/status '{"to":"HALT","reason":"e2e halts its pair"}'
    expect 200 - "ADMIN halts LINK-BTC"
    check '.from == "TRADING" and .to == "HALT" and .change == null' "at once: the brake does not wait"
    LINK_STATUS=HALT
  fi
  as OPERATOR POST /admin/v1/instruments/pairs/LINK-BTC/status '{"to":"TRADING","reason":"e2e opens LINK-BTC"}'
  expect 403 ADMIN_FORBIDDEN "an OPERATOR opens no pair"
  confirm_status /admin/v1/instruments/pairs/LINK-BTC TRADING "e2e opens LINK-BTC"
  expect 202 - "ADMIN confirms LINK-BTC's opening (from $LINK_STATUS)"
  check '.change.status == "SCHEDULED" and .change.kind == "PAIR_STATUS" and .change.effective_at != null' "it opens a minute later"
  LINK_OPENING=$(jq -r .change.id <<<"$BODY")
else
  echo "skip LINK-BTC's halt and opening: two-person approval is on and this run has one ADMIN"
fi

echo "== trading parameters wait for an ADMIN's confirmation and their time"
as AUDITOR GET /admin/v1/instruments/config ""
CONFIG=$BODY
# LINK-BTC's reference multiplier: a trading parameter, confirmed and then
# canceled.
MULTIPLIED=$(link_pair "$LINK_REF" 10) # a power of ten, as instrument-service wants
as OPERATOR POST /admin/v1/instruments/preview "{\"config\":$MULTIPLIED}"
expect 200 - "an OPERATOR previews LINK-BTC's reference multiplier"
check '([.guard.params[] | select(.entity == "TRADING_PAIR" and .key == "LINK-BTC" and .field == "reference_multiplier")] | length == 1) and .guard.confirmation == null' \
  "a trading parameter, not the OPERATOR's to confirm"
as OPERATOR POST /admin/v1/instruments/apply "{\"config\":$MULTIPLIED,\"reason\":\"e2e multiplier\"}"
expect 403 ADMIN_FORBIDDEN "nor to apply"
as ADMIN POST /admin/v1/instruments/apply "{\"config\":$MULTIPLIED,\"reason\":\"e2e multiplier\"}"
expect 409 ADMIN_CONFIRMATION_REQUIRED "an ADMIN brings the preview's confirmation"
as ADMIN POST /admin/v1/instruments/preview "{\"config\":$MULTIPLIED}"
expect 200 - "ADMIN's preview"
check ".guard.delay_seconds == 60 and (.guard.confirmation.token | length) > 40 and .guard.two_person == $TWO_PERSON" "confirmable for ten minutes"
TOKEN=$(jq -r .guard.confirmation.token <<<"$BODY")
as ADMIN POST /admin/v1/instruments/apply "$(jq -nc --argjson c "$MULTIPLIED" --arg t "$TOKEN" '{config: $c, reason: "e2e multiplier", confirmation: $t}')"
expect 202 - "confirmed, the change waits"
check '.change.kind == "CONFIG" and (.change.status == "SCHEDULED" or .change.status == "PENDING_APPROVAL") and
  .change.summary.params[0].field == "reference_multiplier"' "recorded with what it moves"
REF_CHANGE=$(jq -r .change.id <<<"$BODY")
at_exit "as ADMIN POST /admin/v1/instruments/changes/$REF_CHANGE/cancel '{\"reason\":\"e2e cleanup\"}' >/dev/null"
as ADMIN POST /admin/v1/instruments/apply "$(jq -nc --argjson c "$MULTIPLIED" --arg t "$TOKEN" '{config: $c, reason: "e2e multiplier", confirmation: $t}')"
expect 202 - "the same confirmation again (a retry)"
check ".change.id == \"$REF_CHANGE\"" "confirms the one change it confirmed"
as ADMIN GET /admin/v1/todo ""
check '.instrument_changes >= 1' "the console counts what waits"
as OPERATOR POST "/admin/v1/instruments/changes/$REF_CHANGE/cancel" '{"reason":"e2e not mine"}'
expect 403 ADMIN_FORBIDDEN "an OPERATOR cancels nothing"
as ADMIN POST "/admin/v1/instruments/changes/$REF_CHANGE/cancel" '{"reason":"e2e changed its mind"}'
expect 200 - "ADMIN cancels it before its time"
check ".status == \"CANCELED\" and .closed_by_email == \"$EMAIL_ADMIN\"" "canceled, nothing applied"
as ADMIN POST "/admin/v1/instruments/changes/$REF_CHANGE/cancel" '{"reason":"e2e again"}'
expect 409 ADMIN_CHANGE_CLOSED "a change closes once"
as AUDITOR GET "/admin/v1/instruments/changes?status=CANCELED&limit=10" ""
expect 200 - "every administrator reads the changes"
check "any(.items[]; .id == \"$REF_CHANGE\")" "the canceled one among them"
LADDER=$(jq -c '{contracts: [.contracts[] | select(.symbol == "ETH-USDT-PERP") | del(.version, .status) | .risk_tiers = [{max_notional: "1000000", max_leverage: 20, mmr: "0.02"}]]}' <<<"$CONFIG")
as ADMIN POST /admin/v1/instruments/preview "{\"config\":$LADDER}"
expect 200 - "ADMIN previews a stricter ladder for ETH-USDT-PERP"
check '([.guard.params[].field] | index("risk_tiers")) != null and .guard.impacts[0].symbol == "ETH-USDT-PERP" and
  (.guard.impacts[0].liquidated | type) == "number" and (.guard.impacts[0].notional | test("^[0-9.]+$"))' "with the positions it would liquidate"
BTCUSDT=$(jq -c '{pairs: [.pairs[] | select(.symbol == "BTC-USDT") | del(.version, .listed_at) | .reference_symbol = ""]}' <<<"$CONFIG")
as OPERATOR POST /admin/v1/instruments/preview "{\"config\":$BTCUSDT}"
expect 422 ADMIN_REFERENCE_IN_USE "BTC-USDT keeps its reference symbol (HOUSE quotes it, a perpetual's index follows it)"
if [[ -n $LINK_OPENING ]]; then
  eventually 180 "LINK-BTC opens once its change took effect" status_is LINK-BTC TRADING
fi
# A buy 5% under the best bid (HOUSE's, on Binance's book) rests on the
# book, within the price band; without a book, at 0.0001. Each try has its
# own idempotency key: the gateway replays a refused one (the trading
# service sees a new status a moment after it changed).
call GET "/v1/market/LINK-BTC/depth?limit=5" ""
LINK_PRICE=$(jq -r 'if (.bids | length) > 0 then (.bids[0][0] | tonumber * 0.95 * 10000000 | floor / 10000000 | tostring) else "0.0001" end' <<<"$BODY")
LINK_TRY=0
link_order() {
  LINK_TRY=$((LINK_TRY + 1))
  call POST /v1/orders "{\"symbol\":\"LINK-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LINK_PRICE\",\"quantity\":\"3\"}" "${UAUTH[@]}" \
    -H "Idempotency-Key: e2e-admin-link-$RUN-$LINK_TRY"
  [[ $STATUS == 202 ]]
}
eventually 40 "the user rests a buy on LINK-BTC at $LINK_PRICE" link_order
LINK_ORDER=$(jq -r .order_id <<<"$BODY")
link_open() {
  call GET "/v1/orders/$LINK_ORDER" "" "${UAUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == OPEN ]]
}
eventually 40 "it rests on the book" link_open
call DELETE "/v1/orders/$LINK_ORDER" "" "${UAUTH[@]}"
link_canceled() {
  call GET "/v1/orders/$LINK_ORDER" "" "${UAUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == CANCELED ]]
}
eventually 40 "and is canceled; LINK-BTC keeps trading" link_canceled

echo "== deposits that need a person (with the custodian's stand-in)"
# mock ARGS... drives the custodian's stand-in on the server (udun-mock).
mock() {
  local args
  args=$(printf '%q ' "$@")
  remote "sudo docker compose $COMPOSE_FILES exec -T udun-mock /app/udun-mock $args"
}
USDT_TRC20="195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
call GET "/v1/wallet/deposit-address?asset=USDT&network=TRON" "" "${UAUTH[@]}"
if [[ $STATUS != 200 ]]; then
  echo "skip the deposit decisions: no TRC20 address from the custodian ($STATUS $(jq -r .code <<<"$BODY"))"
else
  ADDR=$(jq -r .address <<<"$BODY")
  SPOT_BEFORE=$(spot_usdt available)
  more_usdt() { # more_usdt AMOUNT: the user's SPOT USDT is AMOUNT above SPOT_BEFORE (to a hair: jq counts in floats)
    [[ $(jq -n --arg a "$(spot_usdt available)" --arg b "$SPOT_BEFORE" --arg d "$1" \
      '(($a | tonumber) - ($b | tonumber) - ($d | tonumber)) | fabs < 0.0000001') == true ]]
  }
  # The custodian receives 2 USDT and its callback is held back: lost, for now. A shorter
  # delay applies to the callbacks udun-mock holds already (56e491b), so the hold lasts
  # until the backfill's checks are done and ends then: the late callback comes at once.
  at_exit "mock delay --seconds 0 >/dev/null"
  mock delay --seconds 600 >/dev/null
  read -r TRADE TX < <(mock deposit --address "$ADDR" --coin "$USDT_TRC20" --amount 2 | jq -r '"\(.trade_id) \(.tx_id)"')
  echo "     the custodian received 2 USDT (trade $TRADE); its callback is held back"
  BACKFILL=$(jq -nc --arg a "$ADDR" --arg t "$TRADE" --arg h "$TX" '{network: "TRON", trade_id: $t, address: $a, tx_hash: $h, amount: "2"}')
  as OPERATOR POST /admin/v1/deposits/manual/check "$BACKFILL"
  expect 403 ADMIN_FORBIDDEN "OPERATOR backfills nothing"
  as FINANCE POST /admin/v1/deposits/manual/check "$(jq -c '.network = "ETH-SEPOLIA"' <<<"$BACKFILL")"
  expect 400 COMMON_INVALID_ARGUMENT "only a custodian's network is backfilled"
  as FINANCE POST /admin/v1/deposits/manual/check "$BACKFILL"
  expect 200 - "FINANCE checks the backfill"
  check ".user_id == \"$USER_ID\" and .asset == \"USDT\" and .unclaimed == false and .value_usdt != null" "the user's address, USDT, above the minimum"
  as FINANCE POST /admin/v1/deposits/manual "$(jq -c '.reason = "e2e: in the custodian console, callback lost"' <<<"$BACKFILL")"
  expect 201 - "FINANCE backfills it"
  if [[ $(jq -r .status <<<"$BODY") == PENDING ]]; then
    as ADMIN POST "/admin/v1/approvals/$(jq -r .id <<<"$BODY")/decide" '{"approve":true,"reason":"e2e second administrator"}'
    expect 200 - "a second administrator approves it (two-person mode)"
  fi
  check '.kind == "DEPOSIT_BACKFILL" and .status == "EXECUTED" and (.result | startswith("deposit ")) and .payload.trade_id != null' \
    "a fund operation, booked as a deposit"
  BACKFILLED=$(jq -r '.result | ltrimstr("deposit ")' <<<"$BODY")
  eventually 60 "the user has 2 USDT more" more_usdt 2
  as FINANCE GET "/admin/v1/deposits/review?manual_pending=true&user_id=$USER_ID" ""
  expect 200 - "the backfills waiting for their callback"
  check "[.items[] | select(.id == \"$BACKFILLED\" and .source == \"MANUAL\" and .entered_by == \"$EMAIL_FINANCE\" and .callback_at == null)] | length == 1" \
    "this one, by FINANCE, no callback yet"
  as FINANCE POST /admin/v1/deposits/manual "$(jq -c '.reason = "e2e: the same trade again"' <<<"$BACKFILL")"
  expect 409 WALLET_DEPOSIT_KNOWN "the same trade is not booked twice"
  mock delay --seconds 0 >/dev/null # the late callback goes at the stand-in's next flush

  # Below the minimum (1 USDT): booked to UNCLAIMED_DEPOSIT, waiting for a decision.
  mock deposit --address "$ADDR" --coin "$USDT_TRC20" --amount 0.5 >/dev/null
  mock deposit --address "$ADDR" --coin "$USDT_TRC20" --amount 0.25 >/dev/null
  waiting() { # waiting AMOUNT VAR: the unclaimed deposit of AMOUNT waits; its ID goes to VAR
    local id
    as FINANCE GET "/admin/v1/deposits/review?attention=true&user_id=$USER_ID" ""
    id=$(jq -r --arg a "$1" '[.items[] | select(.amount == $a and .reason == "BELOW_MINIMUM" and .unclaimed and .journal_id != null)][0].id // empty' <<<"$BODY")
    [[ -n $id ]] && eval "$2=\$id"
  }
  eventually 120 "0.5 USDT below the minimum waits for a decision" waiting 0.5 SMALL
  eventually 60 "so do 0.25 USDT" waiting 0.25 SMALLER
  as ADMIN GET /admin/v1/todo ""
  check '.deposits >= 2' "the console's counts include them"
  as OPERATOR POST "/admin/v1/deposits/$SMALL/credit" '{"reason":"e2e"}'
  expect 403 ADMIN_FORBIDDEN "OPERATOR credits nothing"
  as FINANCE POST "/admin/v1/deposits/$SMALL/credit" '{"reason":"e2e minimum waived"}'
  expect 200 - "FINANCE credits 0.5 USDT to the user"
  check ".status == \"CREDITED\" and .resolution == \"CREDITED\" and .resolved_by == \"$EMAIL_FINANCE\" and .release_journal_id != null" \
    "CREDITED, with the release's journal"
  eventually 60 "the user has 2.5 USDT more" more_usdt 2.5
  as FINANCE POST "/admin/v1/deposits/$SMALLER/reject" '{"reason":"e2e below the minimum, stays unclaimed"}'
  expect 200 - "FINANCE rejects 0.25 USDT"
  check '.resolution == "DISMISSED" and .status == "REJECTED" and .attention == false' "handled, no funds moved"
  as FINANCE POST "/admin/v1/deposits/$SMALLER/credit" '{"reason":"e2e changed my mind"}'
  expect 409 WALLET_DEPOSIT_NOT_RELEASABLE "a rejected one is not credited later"
  as FINANCE GET "/admin/v1/deposits/review?attention=true&user_id=$USER_ID" ""
  check "[.items[].id] | (index(\"$SMALL\") == null and index(\"$SMALLER\") == null)" "neither waits any more"

  late_callback() {
    as FINANCE GET "/admin/v1/deposits/$BACKFILLED" ""
    [[ $STATUS == 200 ]] && jq -e '.callback_at != null and .discrepancy == "" and .attention == false' <<<"$BODY" >/dev/null
  }
  eventually 150 "the custodian's late callback confirms the backfill" late_callback
  more_usdt 2.5 || { echo "FAIL the late callback booked the deposit again: $(spot_usdt available) from $SPOT_BEFORE" >&2; exit 1; }
  echo "ok   and books nothing again"
  [[ $(pg "SELECT result FROM wallet.custody_callbacks WHERE trade_id = '$TRADE' ORDER BY received_at DESC LIMIT 1") == APPLIED ]] ||
    { echo "FAIL the late callback of trade $TRADE is not APPLIED" >&2; exit 1; }
  echo "ok   the callback is logged APPLIED"
  as FINANCE GET "/admin/v1/deposits/review?manual_pending=true&user_id=$USER_ID" ""
  check "[.items[].id] | index(\"$BACKFILLED\") == null" "no longer waiting for its callback"
  if exchangectl wallet checks --network UDUN | grep -q "$BACKFILLED"; then
    echo "FAIL exchangectl still lists $BACKFILLED as backfilled without a callback" >&2
    exit 1
  fi
  echo "ok   nor in exchangectl's custodian report"
fi

echo "== a deposit of nobody credited to a user (B7a, C5.5 21)"
# Deposits of nobody come from the stand-in custodian UDUNMOCK (ADR-0017),
# which serves only the hidden test asset: a user eligible for test assets
# (registered in AQ) takes a deposit address on it, the stand-in's addresses
# are retired, and two transfers to that address are booked to
# UNCLAIMED_DEPOSIT as deposits of nobody, the user their address's former
# holder. One is credited to the former holder (the usual limits; the test
# asset has no price, so a second administrator may decide), the other to
# another user: that one waits for a second administrator whatever its worth
# (the coordinator's 10-04 decision). Skipped until the stand-in custodian
# serves a network.
NO_OWNER=00000000-0000-0000-0000-000000000000
as OPERATOR GET /admin/v1/instruments ""
expect 200 - "the instruments"
TEST_NET=$(jq -c 'first(.assets[] | .asset_code as $a | (.networks // [])[] | select(.provider == "UDUNMOCK") |
  {asset: $a, network, coin: .provider_coin}) // empty' <<<"$BODY")
if [[ -z $TEST_NET ]]; then
  echo "skip a deposit of nobody: the stand-in custodian UDUNMOCK serves no network yet (ADR-0017)"
else
  T_ASSET=$(jq -r .asset <<<"$TEST_NET")
  T_NETWORK=$(jq -r .network <<<"$TEST_NET")
  T_COIN=$(jq -r .coin <<<"$TEST_NET")
  register "e2e-admin-holder-$RUN@example.com" "e2e-admin-holder-$RUN" "e2e admin holder $RUN" AQ
  HOLDER=$(jq -r .user_id <<<"$BODY")
  HAUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
  call GET "/v1/wallet/deposit-address?asset=$T_ASSET&network=$T_NETWORK" "" "${HAUTH[@]}"
  expect 200 - "a user eligible for test assets takes a $T_ASSET address on $T_NETWORK"
  HOLD_ADDR=$(jq -r .address <<<"$BODY")
  remote "sudo docker compose $COMPOSE_FILES exec -T wallet-service /app/exchangectl wallet retire-addresses --provider UDUNMOCK --reason 'e2e admin.sh: a retired address receives deposits of nobody' --yes" >/dev/null
  echo "ok   the stand-in's deposit addresses retired"
  mock deposit --address "$HOLD_ADDR" --coin "$T_COIN" --amount 1 >/dev/null
  mock deposit --address "$HOLD_ADDR" --coin "$T_COIN" --amount 2 >/dev/null
  nobodys() { # both deposits to the retired address wait for a decision: NOBODY1 (1) and NOBODY2 (2)
    as FINANCE GET "/admin/v1/deposits/review?attention=true&user_id=$NO_OWNER&limit=100" ""
    of() { jq -r --arg a "$HOLD_ADDR" --argjson n "$1" '[.items[] | select(.address == $a and .reason == "UNKNOWN_ADDRESS" and .unclaimed and
      .journal_id != null and (.amount | tonumber) == $n)][0].id // empty' <<<"$BODY"; }
    NOBODY1=$(of 1)
    NOBODY2=$(of 2)
    [[ -n $NOBODY1 && -n $NOBODY2 ]]
  }
  eventually 120 "two deposits of nobody wait in UNCLAIMED_DEPOSIT" nobodys
  as FINANCE GET "/admin/v1/deposits/$NOBODY1" ""
  check ".user_id == \"$NO_OWNER\" and .address_owner == \"$HOLDER\" and .address_owner_retired == true" \
    "nobody's, the user its address's former holder"
  as OPERATOR POST "/admin/v1/deposits/$NOBODY1/assign" "{\"user_id\":\"$HOLDER\",\"reason\":\"e2e\"}"
  expect 403 ADMIN_FORBIDDEN "OPERATOR credits it to nobody"
  KEY=none
  as FINANCE POST "/admin/v1/deposits/$NOBODY1/assign" "{\"user_id\":\"$HOLDER\",\"reason\":\"e2e\"}"
  expect 400 COMMON_INVALID_ARGUMENT "not without an Idempotency-Key"
  as FINANCE POST "/admin/v1/deposits/$NOBODY1/credit" '{"reason":"e2e credit it as it is"}'
  expect 409 WALLET_DEPOSIT_NO_OWNER "it is not credited as it is: nobody owns it"

  # To its former holder: the usual limits.
  ASSIGN=$(jq -nc --arg u "$HOLDER" '{user_id: $u, reason: "e2e the former holder proved the transfer"}')
  KEY="e2e-nobody-$RUN"
  as FINANCE POST "/admin/v1/deposits/$NOBODY1/assign" "$ASSIGN"
  expect 201 - "FINANCE credits it to its former holder"
  check ".kind == \"DEPOSIT_ASSIGN\" and .escalation != \"NOT_ADDRESS_HOLDER\" and .payload.former_holder == \"$HOLDER\" and
    .payload.user_id == \"$HOLDER\"" "the usual limits for the former holder"
  if [[ $(jq -r .status <<<"$BODY") == PENDING ]]; then
    as ADMIN POST "/admin/v1/approvals/$(jq -r .id <<<"$BODY")/decide" '{"approve":true,"reason":"e2e second administrator"}'
    expect 200 - "a second administrator approves it ($(jq -r .escalation <<<"$BODY"))"
  fi
  check ".status == \"EXECUTED\" and .journal_id != null and .payload.deposit_id == \"$NOBODY1\"" "a fund operation, released with its journal"
  ASSIGNED=$(jq -r .id <<<"$BODY")
  KEY="e2e-nobody-$RUN"
  as FINANCE POST "/admin/v1/deposits/$NOBODY1/assign" "$ASSIGN"
  expect 201 - "the same request again (a retry)"
  check ".id == \"$ASSIGNED\" and .status == \"EXECUTED\"" "is the same operation"
  as FINANCE POST "/admin/v1/deposits/$NOBODY1/assign" "$(jq -c '.reason = "e2e once more"' <<<"$ASSIGN")"
  expect 409 ADMIN_DEPOSIT_NOT_UNOWNED "credited once"
  as FINANCE GET "/admin/v1/deposits/$NOBODY1" ""
  check ".user_id == \"$HOLDER\" and .resolution == \"CREDITED\" and .release_journal_id != null and .attention == false" \
    "the former holder's now, handled"
  holder_has() {
    as AUDITOR GET "/admin/v1/users/$HOLDER/balances" ""
    [[ $STATUS == 200 ]] && jq -e --arg a "$T_ASSET" 'any(.balances[]; .asset == $a and .account_type == "SPOT" and (.available | tonumber) == 1)' \
      <<<"$BODY" >/dev/null
  }
  eventually 60 "the former holder has 1 $T_ASSET" holder_has

  # To another user: a second administrator, whatever its worth.
  KEY="e2e-nobody-other-$RUN"
  as FINANCE POST "/admin/v1/deposits/$NOBODY2/assign" "$(jq -nc --arg u "$USER_ID" '{user_id: $u, reason: "e2e the sender proved the transfer"}')"
  expect 201 - "FINANCE credits the other to another user"
  check ".status == \"PENDING\" and .mode == \"TWO_PERSON\" and .escalation == \"NOT_ADDRESS_HOLDER\" and .payload.former_holder == \"$HOLDER\" and
    .payload.user_id == \"$USER_ID\"" "it waits for a second administrator: not the address's holder"
  OTHER_ASSIGN=$(jq -r .id <<<"$BODY")
  as FINANCE POST "/admin/v1/approvals/$OTHER_ASSIGN/decide" '{"approve":true,"reason":"e2e my own"}'
  expect 403 ADMIN_SELF_APPROVAL "not by its requester"
  as ADMIN POST "/admin/v1/deposits/$NOBODY2/assign" "$(jq -nc --arg u "$HOLDER" '{user_id: $u, reason: "e2e a second request for it"}')"
  expect 409 ADMIN_DEPOSIT_ASSIGN_OPEN "one live request per deposit"
  as ADMIN POST "/admin/v1/approvals/$OTHER_ASSIGN/decide" '{"approve":true,"reason":"e2e checked the sender"}'
  expect 200 - "ADMIN approves it"
  check ".status == \"EXECUTED\" and .journal_id != null" "released with its journal"
  as FINANCE GET "/admin/v1/deposits/$NOBODY2" ""
  check ".user_id == \"$USER_ID\" and .resolution == \"CREDITED\" and .attention == false" "the other user's now, handled"
  other_audited() {
    as AUDITOR GET "/admin/v1/audit-logs?target=user:$USER_ID" ""
    [[ $STATUS == 200 ]] && jq -e --arg h "$HOLDER" --arg d "$NOBODY2" 'any(.items[]; .payload.action == "admin.deposits.assign_requested" and
      (.payload.details | contains($d) and contains("\"former_holder\":\"" + $h + "\"") and contains("NOT_ADDRESS_HOLDER")))' <<<"$BODY" >/dev/null
  }
  eventually 60 "the request names the former holder in the trail" other_audited
fi

echo "== the custodians' fees (C6)"
# What the custodians charge on withdrawals, held for a person or booked as
# reported; deciding one moves GAS_SUPPLY (ledger.adjust.approve).
as AUDITOR GET "/admin/v1/custody/fees?provider=UDUNMOCK&status=HELD" ""
expect 200 - "AUDITOR reads the stand-in's held fees"
check "all(.items[]; .provider == \"UDUNMOCK\" and .status == \"HELD\")" "the stand-in's, held"
as AUDITOR GET "/admin/v1/custody/fees?status=PAID" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown status"
NO_FEE=$(uuidgen | tr '[:upper:]' '[:lower:]')
as OPERATOR POST "/admin/v1/custody/fees/$NO_FEE/book" '{"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "OPERATOR books no fee"
as FINANCE POST "/admin/v1/custody/fees/$NO_FEE/book" '{"reason":""}'
expect 400 COMMON_INVALID_ARGUMENT "not without a reason"
as FINANCE POST "/admin/v1/custody/fees/$NO_FEE/write-off" '{"reason":"e2e no such fee"}'
expect 404 WALLET_CUSTODY_FEE_NOT_FOUND "a withdrawal without a custodian's fee"
as AUDITOR GET "/admin/v1/custody/fees?status=BOOKABLE&limit=200" ""
BOOKED_FEE=$(jq -r '[.items[] | select(.journal_id != null)][0].withdrawal_id // empty' <<<"$BODY")
if [[ -n $BOOKED_FEE ]]; then
  as FINANCE POST "/admin/v1/custody/fees/$BOOKED_FEE/write-off" '{"reason":"e2e write off a booked fee"}'
  expect 409 WALLET_CUSTODY_FEE_NOT_HELD "a fee booked already waits for no one"
else
  echo "skip a booked fee: none booked yet"
fi

echo "== a withdrawal's review details and holds"
as FINANCE GET "/admin/v1/withdrawals?held=false&min_risk=0&min_value_usdt=0&max_value_usdt=1000000" ""
expect 200 - "the queue filters by hold, risk and worth"
as FINANCE GET "/admin/v1/withdrawals?status=CONFIRMED&limit=1" ""
WD=$(jq -r '.items[0].id // empty' <<<"$BODY")
if [[ -z $WD ]]; then
  echo "skip a withdrawal's details: no confirmed withdrawal on this server"
else
  as AUDITOR GET "/admin/v1/withdrawals/$WD" ""
  expect 200 - "a withdrawal with what its review needs"
  check ".withdrawal.id == \"$WD\" and (.used_today_usdt | test(\"^[0-9.]+$\")) and (.used_month_usdt | test(\"^[0-9.]+$\")) and has(\"address_book\")" \
    "the user's withdrawals so far and the address book"
  as OPERATOR POST "/admin/v1/withdrawals/$WD/hold" '{"hold":true,"note":"e2e"}'
  expect 403 ADMIN_FORBIDDEN "OPERATOR holds no withdrawal"
  as FINANCE POST "/admin/v1/withdrawals/$WD/hold" '{"hold":true,"note":"e2e hold"}'
  expect 409 WALLET_WITHDRAWAL_NOT_IN_REVIEW "only one in review is held"
fi

echo "== a suspended asset's withdrawals, resumed from the console"
# What the custody checks do when funds go missing, done by hand on ETH
# (exchangectl, as an operator would): the console lists it and only an
# ADMIN resumes it, audited by wallet-service (C5.5 ⑯).
as AUDITOR GET /admin/v1/withdrawals/suspensions ""
expect 200 - "the suspended assets"
if [[ $(jq '[.items[] | select(.asset == "ETH")] | length' <<<"$BODY") == 0 ]]; then
  exchangectl wallet withdrawals-suspend --asset ETH --reason "e2e: the console resumes it" >/dev/null
  # shellcheck disable=SC2016 # a safety net: ETH's withdrawals do not stay suspended after the run
  at_exit 'exchangectl wallet withdrawals-resume --asset ETH --reason "e2e cleanup" >/dev/null 2>&1 || true'
  as AUDITOR GET /admin/v1/withdrawals/suspensions ""
  check 'any(.items[]; .asset == "ETH" and .reason == "e2e: the console resumes it" and .shortfall == "0")' "ETH listed, with its reason"
  as FINANCE POST /admin/v1/withdrawals/suspensions/ETH/resume '{"reason":"e2e not mine to resume"}'
  expect 403 ADMIN_FORBIDDEN "FINANCE resumes nothing"
  as ADMIN POST /admin/v1/withdrawals/suspensions/ETH/resume '{"reason":"e2e the balance matches"}'
  expect 200 - "ADMIN resumes ETH's withdrawals"
  check '.asset == "ETH" and .reason == "e2e: the console resumes it"' "the suspension it lifted"
  as ADMIN POST /admin/v1/withdrawals/suspensions/ETH/resume '{"reason":"e2e again"}'
  expect 404 COMMON_NOT_FOUND "lifted once"
  as AUDITOR GET /admin/v1/withdrawals/suspensions ""
  check '[.items[] | select(.asset == "ETH")] | length == 0' "no longer listed"
  resumed_audited() {
    as AUDITOR GET "/admin/v1/audit-logs?target=asset:ETH" ""
    [[ $STATUS == 200 ]] && jq -e --arg a "$EMAIL_ADMIN" 'any(.items[]; .payload.action == "wallet.withdrawals.resume" and .actor == $a)' <<<"$BODY" >/dev/null
  }
  eventually 60 "wallet-service audits the resume in the ADMIN's name" resumed_audited
else
  echo "skip the suspension: ETH's withdrawals are suspended already, for something else"
fi

echo "== the account's security, history and risk"
as AUDITOR GET "/admin/v1/users/$USER_ID/security" ""
expect 200 - "AUDITOR reads the account's security"
check '(.identities | length) == 1 and .identities[0].kind == "EMAIL" and (.identities[0].value | contains("***")) and .totp.status == "NONE" and (.sessions | length) >= 1 and .locked_seconds == 0' \
  "one masked email, no authenticator, a live session"
as AUDITOR GET "/admin/v1/users/$USER_ID/login-history?limit=1" ""
expect 200 - "the sign-ins, a page of one"
check '(.items | length) == 1 and .items[0].result == "SUCCESS" and (.items[0].ip == "" or (.items[0].ip | contains("*")))' "a success, its address masked"
as AUDITOR GET "/admin/v1/users/$USER_ID/history" ""
expect 200 - "the history"
check '(.consents | length) == 2 and ([.status_changes[].to_status] | index("FROZEN")) != null' "two consents; the freeze is there"
as AUDITOR GET "/admin/v1/users/$USER_ID/risk" ""
expect 200 - "the risk rules' assessments"
check '.assessments | type == "array"' "a list"
as AUDITOR POST "/admin/v1/users/$USER_ID/contacts/reveal" ""
expect 403 ADMIN_FORBIDDEN "AUDITOR cannot unmask the contacts"
as FINANCE POST "/admin/v1/users/$USER_ID/contacts/reveal" ""
expect 200 - "FINANCE unmasks them (audited)"
check ".identities[0].value == \"$EMAIL\"" "the whole email"

echo "== an identity rebind waits for an administrator"
# A fresh access token: the one from the unfreeze may be near its 15 minutes.
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 200 - "the user refreshes"
REFRESH=$(jq -r .refresh_token <<<"$BODY")
UACCESS=$(jq -r .access_token <<<"$BODY")
UAUTH=(-H "Authorization: Bearer $UACCESS")
NEW_EMAIL="e2e-admin-moved-$RUN@example.com"
wait_resend "$EMAIL"
otp STEP_UP "$EMAIL" "$DEVICE" "$UACCESS"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${UAUTH[@]}"
expect 200 - "the user steps up by email"
STEP=$(jq -r .step_up_token <<<"$BODY")
otp REBIND_IDENTITY "$NEW_EMAIL" "$DEVICE" "$UACCESS"
call POST /v1/auth/identity/rebind "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${UAUTH[@]}" -H "X-Step-Up-Token: $STEP"
expect 202 - "and asks to move the only email"
check '.status == "PENDING_REVIEW"' "a single identity waits for review"
reject_pending_rebinds() { # leaves no request waiting when a check below fails
  as OPERATOR GET "/admin/v1/identity-requests?user_id=$USER_ID" "" || return 0
  for id in $(jq -r '.items[]? | select(.status == "PENDING_REVIEW") | .id' <<<"$BODY"); do
    as OPERATOR POST "/admin/v1/identity-requests/$id/decide" '{"approve":false,"reason":"e2e cleanup"}' || true
  done
}
at_exit reject_pending_rebinds
as AUDITOR GET "/admin/v1/identity-requests?user_id=$USER_ID" ""
expect 200 - "the request is listed"
check '(.items | length) == 1 and .items[0].status == "PENDING_REVIEW" and (.items[0].new_value | contains("***")) and (.items[0].current_value | contains("***"))' "pending, masked"
REQ=$(jq -r '.items[0].id' <<<"$BODY")
as OPERATOR GET /admin/v1/todo ""
check '.identity_requests >= 1' "OPERATOR's todo counts it"
as AUDITOR POST "/admin/v1/identity-requests/$REQ/decide" '{"approve":true,"reason":"e2e"}'
expect 403 ADMIN_FORBIDDEN "AUDITOR decides nothing"
as OPERATOR POST "/admin/v1/identity-requests/$REQ/decide" '{"approve":true,"reason":"e2e: checked by phone"}'
expect 200 - "OPERATOR approves it"
check '.status == "APPROVED" and .decided_by != ""' "approved"
as OPERATOR POST "/admin/v1/identity-requests/$REQ/decide" '{"approve":false,"reason":"e2e again"}'
expect 409 COMMON_CONFLICT "a request is decided once"
as OPERATOR GET "/admin/v1/users/lookup?q=$(jq -rn --arg e "$NEW_EMAIL" '$e|@uri')" ""
expect 200 - "the account is found by the new email"
check ".user.id == \"$USER_ID\"" "the same account"

echo "== sessions, authenticator and a temporary password"
as OPERATOR POST "/admin/v1/users/$USER_ID/totp-reset" '{"reason":"e2e lost phone"}'
expect 200 - "OPERATOR resets the authenticator of an account without one"
check '.removed == false' "there was none"
# The user binds an authenticator app (a step-up by mail first), which the
# console then resets: withdrawals wait for review for a day after it.
wait_resend "$NEW_EMAIL"
otp STEP_UP "$NEW_EMAIL" "$DEVICE" "$UACCESS"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${UAUTH[@]}"
expect 200 - "the user steps up by mail"
call POST /v1/auth/totp/setup "" "${UAUTH[@]}" -H "X-Step-Up-Token: $(jq -r .step_up_token <<<"$BODY")"
expect 200 - "sets up an authenticator app"
call POST /v1/auth/totp/confirm "{\"code\":\"$(totp "$(jq -r .secret <<<"$BODY")")\"}" "${UAUTH[@]}"
expect 204 - "and binds it"
as FINANCE POST "/admin/v1/users/$USER_ID/totp-reset" '{"reason":"e2e lost phone"}'
expect 403 ADMIN_FORBIDDEN "FINANCE resets no authenticator"
as OPERATOR POST "/admin/v1/users/$USER_ID/totp-reset" '{"reason":"e2e lost phone"}'
expect 200 - "OPERATOR resets the authenticator"
check '.removed == true' "the bound one is gone"
as AUDITOR GET "/admin/v1/users/$USER_ID/security" ""
expect 200 - "the account's security"
check '.totp.status == "NONE" and (.totp.changed_at | type) == "string" and (now - (.totp.changed_at | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601)) < 600' \
  "records the reset: the next day's withdrawals wait for review"
as OPERATOR POST "/admin/v1/users/$USER_ID/sessions/revoke" '{"reason":""}'
expect 400 COMMON_INVALID_ARGUMENT "ending sessions needs a reason"
as OPERATOR POST "/admin/v1/users/$USER_ID/sessions/revoke" '{"reason":"e2e stolen phone"}'
expect 200 - "OPERATOR ends every session"
check '.revoked >= 1' "at least the user's own"
revoked_token() {
  call GET /v1/user/profile "" "${UAUTH[@]}"
  [[ $STATUS == 401 && $(jq -r .code <<<"$BODY") == AUTH_SESSION_REVOKED ]]
}
eventually 20 "the user's token stops working" revoked_token
as OPERATOR POST "/admin/v1/users/$USER_ID/password-reset" '{"reason":"e2e forgot the password"}'
expect 200 - "OPERATOR sets a temporary password"
TEMP=$(jq -r .temporary_password <<<"$BODY")
check '(.temporary_password | test("^[A-Za-z0-9]{4}(-[A-Za-z0-9]{4}){3}$")) and .sessions_revoked == 0' "four groups of four; nothing left to end"
call POST /v1/auth/login/password "{\"identifier\":\"$NEW_EMAIL\",\"password\":\"e2e admin user $RUN\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 401 AUTH_PASSWORD_INVALID "the old password no longer works"
call POST /v1/auth/login/password "{\"identifier\":\"$NEW_EMAIL\",\"password\":\"$TEMP\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 200 - "the temporary password signs in with the new email"
UAUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")

echo "== administrators from the console"
as OPERATOR GET /admin/v1/admins ""
expect 403 ADMIN_FORBIDDEN "OPERATOR manages no administrator"
as AUDITOR GET /admin/v1/roles ""
expect 200 - "every administrator reads the roles"
check '[.roles[].role] == ["ADMIN","OPERATOR","FINANCE","AUDITOR"] and (.roles[0].permissions | index("admins.manage")) != null and ([.roles[1:][].permissions[]] | index("admins.manage")) == null' \
  "only ADMIN manages administrators"
# link keeps the one-time setup token a create or a reset shows once in
# LINK and scrubs it from BODY and the body file; inspect does the same
# with the authenticator secret a link shows (SECRET_STAFF), so no check
# below can print either (C5.5 ⑪).
link() {
  local tok
  tok=$(jq -r '.setup.token // empty' <<<"$BODY" 2>/dev/null || true)
  [[ -n $tok ]] && LINK=$tok # an answer without one (a refusal) keeps the last
  BODY=$(jq -c 'if type == "object" and (.setup | type) == "object" then .setup |= del(.token) else . end' <<<"$BODY" 2>/dev/null || echo '{}')
  : >"$WORK/body"
}
inspect() {
  local sec
  acall POST /admin/v1/setup/inspect "$(jq -nc --arg t "$LINK" '{token: $t}')" "${CSRF[@]}" -D "$WORK/inspect.headers"
  sec=$(jq -r '.totp_secret // empty' <<<"$BODY" 2>/dev/null || true)
  [[ -n $sec ]] && SECRET_STAFF=$sec
  BODY=$(jq -c 'if type == "object" then (if .totp_secret then .totp_secret = "set" else . end) | del(.totp_uri) else . end' <<<"$BODY" 2>/dev/null || echo '{}')
  : >"$WORK/body"
}
# setup_code is a code a setup spends leaving the next sign-in its own: the
# step before this one (the server takes one step either way), not in the
# last seconds of a step (the request could arrive in the next one).
setup_code() {
  (($(date +%s) % 30 < 25)) || sleep 6
  totp "$SECRET_STAFF" -1
}
# paced runs a sign-in, a setup link's inspection or a setup: they share
# the console's limit of 10 a minute from one address (C5.5 ⑪), and this
# section makes more than that, so the tenth within a minute waits for the
# window to pass (a faster network hit 429 here).
PACED=()
paced() {
  local now t kept=()
  now=$(date +%s)
  for t in ${PACED[@]+"${PACED[@]}"}; do
    ((now - t < 61)) && kept+=("$t")
  done
  PACED=(${kept[@]+"${kept[@]}"})
  if ((${#PACED[@]} >= 9)); then
    sleep $((61 - (now - PACED[0])))
  fi
  PACED+=("$(date +%s)")
  "$@"
}
# complete sets up the link with what JSON adds to the token.
complete() { acall POST /admin/v1/setup "$(jq -c --arg t "$LINK" '. + {token: $t}' <<<"$1")" "${CSRF[@]}"; }
EMAIL_STAFF="e2e-staff-$RUN@example.com" PW_STAFF=$(password) SECRET_STAFF=$(secret) LINK=""
acall POST /admin/v1/admins "$(jq -nc --arg e "$EMAIL_STAFF" '{email: $e, name: "e2e staff", role: "OPERATOR", reason: "e2e hires an operator"}')" \
  -b "$WORK/ADMIN.jar" "${CSRF[@]}" -D "$WORK/staff.headers"
link
expect 201 - "ADMIN creates an OPERATOR"
STAFF_ID=$(jq -r .admin.id <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit "remote \"sudo docker compose \$COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $EMAIL_STAFF --reason 'e2e run over'\" >/dev/null"
check '.admin.role == "OPERATOR" and .admin.status == "ACTIVE" and .admin.sessions == 0 and .admin.last_login_at == null' "active, never signed in"
check '.setup.kind == "CREATE" and .setup.expires_at != null and (has("password") or has("totp_secret") | not)' \
  "with a one-time setup link and no credentials"
[[ ${#LINK} -ge 40 ]] || { echo "FAIL no setup link came back" >&2; exit 1; }
grep -qi '^cache-control: no-store' "$WORK/staff.headers" || { echo "FAIL the setup link may be cached" >&2; exit 1; }
echo "ok   shown once (no-store)"
acall POST /admin/v1/admins "$(jq -nc --arg e "$EMAIL_STAFF" '{email: $e, name: "again", role: "AUDITOR", reason: "e2e twice"}')" -b "$WORK/ADMIN.jar" "${CSRF[@]}"
link
expect 409 ADMIN_EXISTS "an address is an administrator once"
paced login STAFF
expect 401 ADMIN_LOGIN_FAILED "nobody signs in before the setup"
paced inspect
expect 200 - "the link opens without a session"
check ".email == \"$EMAIL_STAFF\" and .kind == \"CREATE\" and .sets_password and .totp_secret == \"set\"" "for the password and an authenticator to bind"
grep -qi '^cache-control: no-store' "$WORK/inspect.headers" || { echo "FAIL the authenticator secret may be cached" >&2; exit 1; }
echo "ok   its secret not cached either"
paced : # the slot first: the code is computed after any wait
complete "$(jq -nc --arg c "$(setup_code)" '{password: "short", totp_code: $c}')"
expect 400 COMMON_INVALID_ARGUMENT "a short password is refused"
paced complete "$(jq -nc --arg p "$PW_STAFF" '{password: $p, totp_code: "000000"}')"
expect 422 ADMIN_TOTP_CODE_WRONG "a wrong code too"
paced : # the slot first: the code is computed after any wait
complete "$(jq -nc --arg p "$PW_STAFF" --arg c "$(setup_code)" '{password: $p, totp_code: $c}')"
[[ $STATUS == 204 ]] || { echo "FAIL the setup: $STATUS $BODY" >&2; exit 1; }
echo "ok   its holder sets the password and binds the authenticator"
paced inspect
expect 404 ADMIN_SETUP_INVALID "the link is spent"
paced login STAFF
expect 200 - "the new OPERATOR signs in with what it set"
check ".admin.role == \"OPERATOR\" and .admin.email == \"$EMAIL_STAFF\" and .admin.must_change_password == false" "as OPERATOR"
as ADMIN GET /admin/v1/admins ""
expect 200 - "ADMIN lists the administrators"
check "any(.admins[]; .id == \"$STAFF_ID\" and .sessions == 1 and .last_login_at != null)" "the new one with its session"
as ADMIN GET "/admin/v1/admins/$STAFF_ID/sessions" ""
expect 200 - "and its sessions"
check '(.sessions | length) == 1 and .sessions[0].user_agent != ""' "one, with its device"
as ADMIN GET /admin/v1/me ""
ADMIN_ID=$(jq -r .id <<<"$BODY")
as ADMIN POST "/admin/v1/admins/$ADMIN_ID/role" '{"role":"AUDITOR","reason":"e2e demotes itself"}'
expect 403 ADMIN_SELF "nobody changes their own account"
as STAFF POST "/admin/v1/admins/$ADMIN_ID/status" '{"enabled":false,"reason":"e2e takes over"}'
expect 403 ADMIN_FORBIDDEN "an OPERATOR disables nobody"
as ADMIN POST "/admin/v1/admins/$STAFF_ID/role" '{"role":"AUDITOR","reason":"e2e reads only"}'
expect 200 - "ADMIN makes it an AUDITOR"
as STAFF GET /admin/v1/me ""
expect 200 - "its next request"
check '.role == "AUDITOR" and (.permissions | index("flags.write")) == null' "is an AUDITOR's"
as ADMIN POST "/admin/v1/admins/$STAFF_ID/password-reset" '{"reason":"e2e forgot it"}'
link
expect 200 - "ADMIN resets its password"
check '.setup.kind == "PASSWORD" and (has("password") | not)' "a link, no password"
as STAFF GET /admin/v1/me ""
expect 401 ADMIN_UNAUTHORIZED "which ends its sessions"
paced login STAFF
expect 401 ADMIN_LOGIN_FAILED "and the old password"
paced inspect
check '.kind == "PASSWORD" and .sets_password and .totp_secret == null' "the link sets a password alone"
PW_STAFF=$(password)
paced complete "$(jq -nc --arg p "$PW_STAFF" '{password: $p}')"
[[ $STATUS == 204 ]] || { echo "FAIL the new password: $STATUS $BODY" >&2; exit 1; }
paced login STAFF
expect 200 - "the new password signs in"
as ADMIN POST "/admin/v1/admins/$STAFF_ID/totp-reset" '{"reason":"e2e lost the phone"}'
link
expect 200 - "ADMIN resets its authenticator"
check '.setup.kind == "TOTP"' "a link to bind a new one"
as STAFF GET /admin/v1/me ""
expect 401 ADMIN_UNAUTHORIZED "which ends its sessions too"
OLD_SECRET=$SECRET_STAFF
paced inspect
check '.kind == "TOTP" and (.sets_password | not) and .totp_secret == "set"' "the link binds an authenticator alone"
[[ $SECRET_STAFF != "$OLD_SECRET" ]] || { echo "FAIL the same authenticator again" >&2; exit 1; }
paced : # the slot first: the code is computed after any wait
complete "$(jq -nc --arg c "$(setup_code)" '{totp_code: $c}')"
[[ $STATUS == 204 ]] || { echo "FAIL the new authenticator: $STATUS $BODY" >&2; exit 1; }
paced login STAFF
expect 200 - "the new authenticator signs in"
NEW_PW=$(password)
as STAFF POST /admin/v1/me/password "$(jq -nc --arg n "$NEW_PW" '{current_password: "not the password", new_password: $n}')"
expect 422 ADMIN_PASSWORD_WRONG "its own password changes with the current one only"
as STAFF POST /admin/v1/me/password "$(jq -nc --arg c "$PW_STAFF" --arg n "$NEW_PW" '{current_password: $c, new_password: $n}')"
[[ $STATUS == 204 ]] || { echo "FAIL its own password: $STATUS $BODY" >&2; exit 1; }
PW_STAFF=$NEW_PW
paced login STAFF
expect 200 - "it changes its own password and signs in with it"
as ADMIN POST "/admin/v1/admins/$STAFF_ID/sessions/revoke" '{"reason":"e2e ends them"}'
[[ $STATUS == 204 ]] || { echo "FAIL ending an administrator's sessions: $STATUS $BODY" >&2; exit 1; }
echo "ok   ADMIN ends its sessions"
as STAFF GET /admin/v1/me ""
expect 401 ADMIN_UNAUTHORIZED "they are over"
as ADMIN POST "/admin/v1/admins/$STAFF_ID/status" '{"enabled":false,"reason":"e2e lets it go"}'
expect 200 - "ADMIN disables it"
check '.status == "DISABLED"' "disabled"
paced login STAFF
expect 401 ADMIN_LOGIN_FAILED "a disabled administrator cannot sign in"
as ADMIN POST "/admin/v1/admins/$STAFF_ID/status" '{"enabled":true,"reason":"e2e takes it back"}'
expect 200 - "ADMIN enables it again"
check '.status == "ACTIVE" and .failed_attempts == 0' "active, its failures forgotten"
paced login STAFF
expect 200 - "it signs in again"

echo "== system health"
as AUDITOR GET "/admin/v1/health?details=true" ""
expect 200 - "AUDITOR reads every service's health with details"
check '(.services | length) >= 15 and all(.services[]; .ready and (.version // "") != "")' "every service ready, with its version"
check 'any(.services[]; .service == "ledger-service" and .kafka_lag != null and .dlq != null) and all(.services[]; .service != "signer" or .kafka_lag == null)' \
  "the consumers' lag and DLQ count, for services with consumers"
check '.feed.state | IN("OK", "DELAYED", "DOWN", "OFF")' "and the reference feed"
as AUDITOR GET /admin/v1/health ""
check 'all(.services[]; has("version") | not) and (has("feed") | not)' "without details, readiness alone"

echo "== an announcement on both sites within a minute"
# One announcement with a fixed slug (articles are never deleted): written
# by the first run, edited by the next ones; each run schedules it,
# publishes it, edits it while shown and takes it off.
SLUG=e2e-console
M_BASE="${M_BASE:-https://m.astras.vip}"
announcement() { # announcement ENGLISH_TITLE: the e2e announcement as the console writes it
  jq -nc --arg s "$SLUG" --arg t "$1" --arg run "$RUN" '{slug: $s, category: "notice", pinned: false, order: 0, texts: [
    {locale: "zh-CN", title: "端到端检查公告", summary: "", body: ("## 检查\n\n第 " + $run + " 次运行；资金为模拟资产。")},
    {locale: "en", title: $t, summary: "", body: ("## Check\n\nRun " + $run + "; funds are simulated.")}]}'
}
as AUDITOR GET "/admin/v1/articles?section=ANNOUNCEMENT" ""
expect 200 - "every administrator reads the announcements"
ARTICLE=$(jq -c --arg s "$SLUG" '[.articles[] | select(.slug == $s)][0] // empty' <<<"$BODY")
as AUDITOR POST /admin/v1/articles "$(announcement e2e | jq -c '. + {section: "ANNOUNCEMENT", reason: "e2e writes nothing"}')"
expect 403 ADMIN_FORBIDDEN "AUDITOR writes no announcement"
if [[ -z $ARTICLE ]]; then
  as OPERATOR POST /admin/v1/articles "$(announcement "E2E check $RUN" | jq -c '. + {section: "ANNOUNCEMENT", reason: "e2e writes its announcement"}')"
  expect 201 - "OPERATOR writes the e2e announcement"
  check '.status == "DRAFT" and .version == 1 and .publish_at == null' "a draft"
else
  as OPERATOR PUT "/admin/v1/articles/$(jq -r .id <<<"$ARTICLE")" \
    "$(announcement "E2E check $RUN" | jq -c --argjson v "$(jq .version <<<"$ARTICLE")" '. + {version: $v, reason: "e2e rewrites its announcement"}')"
  expect 200 - "OPERATOR rewrites the e2e announcement"
fi
ART_ID=$(jq -r .id <<<"$BODY") ART_V=$(jq -r .version <<<"$BODY")
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'as ADMIN GET "/admin/v1/articles/$ART_ID" "" && [[ $(jq -r .status <<<"$BODY") == PUBLISHED ]] &&
  as ADMIN POST "/admin/v1/articles/$ART_ID/archive" "{\"version\":$(jq .version <<<"$BODY"),\"reason\":\"e2e cleanup\"}" >/dev/null'
as OPERATOR POST /admin/v1/articles "$(announcement again | jq -c '. + {section: "ANNOUNCEMENT", reason: "e2e writes it twice"}')"
expect 409 NOTIFY_ARTICLE_EXISTS "a slug is taken once in a section"
as OPERATOR POST "/admin/v1/articles/$ART_ID/publish" "$(jq -nc --argjson v "$ART_V" '{version: $v, publish_at: (now + 3600 | todate), reason: "e2e schedules it an hour ahead"}')"
expect 200 - "OPERATOR schedules it an hour ahead"
check '.status == "PUBLISHED" and (.publish_at | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601) > now' "published from a time to come"
ART_V=$(jq -r .version <<<"$BODY")
call GET "/v1/announcements/$SLUG" ""
expect 404 COMMON_NOT_FOUND "the sites do not show it before its time"
as OPERATOR POST "/admin/v1/articles/$ART_ID/publish" "{\"version\":$ART_V,\"reason\":\"e2e publishes it now\"}"
expect 200 - "OPERATOR publishes it now"
ART_V=$(jq -r .version <<<"$BODY")
listed() { # listed SITE TITLE: the site's API lists the e2e announcement in English with TITLE
  local user_base=$BASE rc=0
  BASE=$1
  call GET "/v1/announcements?locale=en&limit=100" "" -D "$WORK/announcements.headers" || rc=$?
  BASE=$user_base
  [[ $rc == 0 && $STATUS == 200 ]] && jq -e --arg s "$SLUG" --arg t "$2" 'any(.items[]; .slug == $s and .title == $t and .fallback == false)' <<<"$BODY" >/dev/null
}
# About a minute: 50 tries of a call and half a second.
eventually 50 "the PC site lists it within a minute" listed "$BASE" "E2E check $RUN"
eventually 50 "so does the mobile site" listed "$M_BASE" "E2E check $RUN"
grep -qi '^cache-control: public, max-age=15' "$WORK/announcements.headers" || { echo "FAIL the list is not cached for 15 seconds" >&2; exit 1; }
call GET "/v1/announcements/$SLUG" ""
expect 200 - "the article, public"
check '.title == "端到端检查公告" and .locale == "zh-CN" and (.body | contains("模拟资产"))' "in Chinese by default, its Markdown body"
as OPERATOR PUT "/admin/v1/articles/$ART_ID" "$(announcement "E2E check $RUN, edited" | jq -c --argjson v "$ART_V" '. + {version: $v, reason: "e2e edits it while shown"}')"
expect 200 - "OPERATOR edits it while shown"
ART_V=$(jq -r .version <<<"$BODY")
as OPERATOR PUT "/admin/v1/articles/$ART_ID" "$(announcement stale | jq -c --argjson v "$((ART_V - 1))" '. + {version: $v, reason: "e2e edits an old copy"}')"
expect 409 COMMON_CONFLICT "an edit of an older version is refused"
eventually 50 "the PC site shows the edit" listed "$BASE" "E2E check $RUN, edited"
eventually 50 "and the mobile site" listed "$M_BASE" "E2E check $RUN, edited"
as OPERATOR POST "/admin/v1/articles/$ART_ID/archive" "{\"version\":$ART_V,\"reason\":\"e2e takes it off\"}"
expect 200 - "OPERATOR takes it off"
check '.status == "ARCHIVED"' "archived"
withdrawn() {
  call GET /v1/announcements ""
  [[ $STATUS == 200 ]] && jq -e --arg s "$SLUG" '(any(.items[]; .slug == $s) | not) and (.withdrawn | index($s)) != null' <<<"$BODY" >/dev/null
}
eventually 50 "gone from the sites within a minute, its slug withdrawn" withdrawn
call GET "/v1/announcements/$SLUG" ""
expect 404 NOTIFY_ARTICLE_WITHDRAWN "the article says it was taken off (no bundled file stands in)"
call GET "/v1/help?locale=en" ""
expect 200 - "the help articles are public too"
check '(.items | type) == "array" and (.withdrawn | type) == "array"' "a list and the slugs taken off"
call GET "/v1/announcements?limit=1" ""
expect 200 - "the lists come a page at a time (C5.5 12)"
check '(.items | length) <= 1 and has("next_cursor") and all(.items[]; has("body") | not)' "summaries only, with the next page's cursor"
call GET "/v1/announcements?cursor=nope" ""
expect 400 COMMON_INVALID_ARGUMENT "a cursor that is not one is refused"

echo "== an operator's in-app message"
as AUDITOR POST /admin/v1/broadcasts '{"audience":"ALL","title":{"zh-CN":"不发"},"body":{"zh-CN":"不发"},"reason":"e2e sends nothing"}'
expect 403 ADMIN_FORBIDDEN "AUDITOR sends no message"
as OPERATOR POST /admin/v1/broadcasts "{\"audience\":\"TAG\",\"tag\":\"E2E_NOBODY_$RUN\",\"title\":{\"zh-CN\":\"无人\"},\"body\":{\"zh-CN\":\"无人\"},\"reason\":\"e2e writes to nobody\"}"
expect 422 ADMIN_TAG_EMPTY "a tag nobody has reaches nobody"
as OPERATOR POST /admin/v1/broadcasts "{\"audience\":\"USER\",\"user_id\":\"$USER_ID\",\"title\":{\"zh-CN\":\"外链\"},\"body\":{\"zh-CN\":\"外链\"},\"link\":\"//evil.example\",\"reason\":\"e2e leads off the sites\"}"
expect 400 COMMON_INVALID_ARGUMENT "a link off the sites is refused"
MESSAGE=$(jq -nc --arg u "$USER_ID" --arg run "$RUN" '{audience: "USER", user_id: $u,
  title: {"zh-CN": ("端到端消息 " + $run), en: ("E2E message " + $run)}, body: {"zh-CN": "请查看资产。", en: "Have a look at your assets."},
  link: "/assets", email: false, reason: "e2e writes to its user"}')
KEY="e2e-message-$RUN"
as OPERATOR POST /admin/v1/broadcasts "$MESSAGE"
expect 201 - "OPERATOR writes to the user"
check '.audience == "USERS" and .users == 1 and .link == "/assets" and (.status | IN("SENDING", "SENT"))' "one user, being delivered"
BROADCAST_ID=$(jq -r .id <<<"$BODY")
KEY="e2e-message-$RUN"
as OPERATOR POST /admin/v1/broadcasts "$MESSAGE"
expect 201 - "the same message again (a retry)"
check ".id == \"$BROADCAST_ID\"" "is the same message"
received() {
  call GET "/v1/notifications?limit=20" "" "${UAUTH[@]}"
  [[ $STATUS == 200 ]] && jq -e --arg b "$BROADCAST_ID" --arg r "$RUN" \
    'any(.items[]; .type == "BROADCAST" and .data.broadcast_id == $b and .data.link == "/assets" and (.title | endswith($r)) and .read == false)' <<<"$BODY" >/dev/null
}
eventually 60 "the user finds it in their notifications, unread" received
check "[.items[] | select(.data.broadcast_id == \"$BROADCAST_ID\")] | length == 1" "once, though sent twice"
NOTICE_ID=$(jq -r --arg b "$BROADCAST_ID" '[.items[] | select(.data.broadcast_id == $b)][0].id' <<<"$BODY")
call POST /v1/notifications/read "{\"ids\":[\"$NOTICE_ID\"]}" "${UAUTH[@]}"
expect 200 - "the user reads it"
counted() {
  as AUDITOR GET "/admin/v1/broadcasts/$BROADCAST_ID" ""
  [[ $STATUS == 200 ]] && jq -e '.status == "SENT" and .recipients == 1 and .read == 1 and .finished_at != null' <<<"$BODY" >/dev/null
}
eventually 60 "the console counts it delivered and read" counted
check '.failures == 0 and .last_error == "" and .retry_at == null' "no round failed (C5.5 12)"
as OPERATOR POST "/admin/v1/broadcasts/$BROADCAST_ID/resume" '{"reason":"e2e resumes a message delivered"}'
expect 409 COMMON_CONFLICT "only a FAILED message is resumed"
as AUDITOR POST "/admin/v1/broadcasts/$BROADCAST_ID/resume" '{"reason":"e2e resumes nothing"}'
expect 403 ADMIN_FORBIDDEN "AUDITOR resumes nothing"
as AUDITOR GET "/admin/v1/broadcasts?limit=5" ""
expect 200 - "every administrator reads the messages sent"
check "any(.items[]; .id == \"$BROADCAST_ID\")" "this one among the newest"

echo "== the audit trail"
audited() { # audited ROLE QUERY JQ
  as "$1" GET "/admin/v1/audit-logs?$2" ""
  [[ $STATUS == 200 ]] && jq -e "$3" <<<"$BODY" >/dev/null
}
q_admin="actor=$(jq -rn --arg e "$EMAIL_ADMIN" '$e|@uri')"
eventually 60 "the ADMIN's sign-in and approval are in the trail" audited AUDITOR "$q_admin" \
  '[.items[].payload.action] | (index("admin.login") != null and index("admin.ledger.adjustment_approved") != null and index("admin.derivatives.insurance_approved") != null)'
if [[ $TWO_PERSON == false ]]; then
  eventually 60 "the ADMIN's single-person adjustments are in the trail" audited AUDITOR "$q_admin" \
    '[.items[].payload.action] | (index("admin.ledger.adjustment_executed") != null and index("admin.ledger.adjustment_rejected") != null)'
fi
eventually 60 "the freeze is audited on the account, by the OPERATOR" audited AUDITOR "target=user:$USER_ID" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\")] | length >= 3"
eventually 60 "the security actions are audited, the temporary password is not" audited AUDITOR "target=user:$USER_ID" \
  "([.items[].payload.action] | (index(\"admin.users.contacts_revealed\") != null and index(\"admin.users.identity_request_decided\") != null and index(\"admin.users.sessions_revoked\") != null and index(\"admin.users.password_reset\") != null)) and (tostring | contains(\"$TEMP\") | not)"
eventually 60 "the flag switches are audited" audited AUDITOR "target=flag:market.reference_kline" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\")] | length >= 2"
eventually 60 "the console's reference data edits are audited" audited AUDITOR "target=instruments" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\") | .payload.action] | index(\"admin.instruments.applied\") != null"
APPLIED_TOO='true'
[[ -n ${LINK_OPENING}${PERP_RESUMED} ]] && APPLIED_TOO='index("admin.instruments.change_applied") != null'
eventually 60 "the changes of trading parameters are audited: confirmed, canceled, applied" audited AUDITOR "$q_admin" \
  "[.items[].payload.action] | index(\"admin.instruments.change_requested\") != null and index(\"admin.instruments.change_canceled\") != null and $APPLIED_TOO"
if [[ -n ${BACKFILLED:-} ]]; then
  eventually 60 "the deposit decisions are audited on the account, by FINANCE" audited AUDITOR "target=user:$USER_ID" \
    "[.items[] | select(.actor == \"$EMAIL_FINANCE\") | .payload.action] | ((index(\"admin.deposits.backfill_executed\") != null or index(\"admin.deposits.backfill_requested\") != null) and index(\"wallet.deposit.backfilled\") != null and index(\"ledger.unclaimed_released\") != null and index(\"wallet.deposit.dismissed\") != null)"
fi
eventually 60 "the announcement's changes are audited, by the OPERATOR" audited AUDITOR "target=announcement:$SLUG" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\") | .payload.action] | (index(\"admin.content.published\") != null and index(\"admin.content.updated\") != null and index(\"admin.content.archived\") != null)"
eventually 60 "the in-app message is audited, once" audited AUDITOR "target=broadcast:$BROADCAST_ID" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\" and .payload.action == \"admin.notices.sent\")] | length == 1"
eventually 60 "the asset's profile changes are audited" audited AUDITOR "target=asset:LINK" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\") | .payload.action] | map(select(. == \"admin.instruments.profile_updated\")) | length >= 2"
q_operator="actor=$(jq -rn --arg e "$EMAIL_OPERATOR" '$e|@uri')"
eventually 60 "the simulated market's changes are audited, by the OPERATOR" audited AUDITOR "target=sim&$q_operator" \
  '[.items[].payload.action] | (index("admin.sim.event_created") != null and index("admin.sim.event_ended") != null and index("admin.sim.event_requested") != null and index("admin.sim.params_changed") != null and index("admin.sim.params_requested") != null)'
eventually 60 "their approval and rejection, by the ADMIN" audited AUDITOR "$q_admin&limit=200" \
  '[.items[].payload.action] | (index("admin.sim.event_approved") != null and index("admin.sim.params_rejected") != null and index("admin.sim.mint_rejected") != null)'
eventually 60 "the mints, by FINANCE" audited AUDITOR "target=sim&actor=$(jq -rn --arg e "$EMAIL_FINANCE" '$e|@uri')" \
  '[.items[].payload.action] | index("admin.sim.mint_requested") != null'
eventually 60 "the administrator's changes are audited, its credentials and links are not" audited AUDITOR "target=admin:$STAFF_ID" \
  "([.items[].payload.action] | (index(\"admin.created\") != null and index(\"admin.role_changed\") != null and index(\"admin.password_reset\") != null and index(\"admin.totp_reset\") != null and index(\"admin.sessions_revoked\") != null and index(\"admin.disabled\") != null and index(\"admin.enabled\") != null and index(\"admin.setup_completed\") != null and index(\"admin.password_changed\") != null)) and (tostring | (contains(\"$PW_STAFF\") or contains(\"$SECRET_STAFF\") or contains(\"$LINK\")) | not)"
exported() {
  acall GET "/admin/v1/audit-logs/export?target=admin:$STAFF_ID" "" -b "$WORK/AUDITOR.jar" -D "$WORK/export.headers"
  [[ $STATUS == 200 ]] && grep -q 'admin.enabled' <<<"$BODY"
}
eventually 60 "AUDITOR exports them as CSV" exported
grep -qi '^content-type: text/csv' "$WORK/export.headers" && grep -qi '^content-disposition: attachment; filename="audit-' "$WORK/export.headers" &&
  grep -qi '^x-truncated: false' "$WORK/export.headers" || { echo "FAIL the export's headers:" >&2; cat "$WORK/export.headers" >&2; exit 1; }
[[ $(head -c 3 "$WORK/body" | od -An -tx1 | tr -d ' \n') == efbbbf ]] &&
  [[ $(head -1 "$WORK/body" | tail -c +4 | tr -d '\r') == "occurred_at,event_type,actor,target,action,reason,details,event_id" ]] ||
  { echo "FAIL the export does not start with a byte order mark and its header row" >&2; exit 1; }
echo "ok   a CSV file with a byte order mark, its header row, nothing left out"
q_auditor="actor=$(jq -rn --arg e "$EMAIL_AUDITOR" '$e|@uri')"
eventually 60 "the export itself is audited" audited ADMIN "$q_auditor" '[.items[].payload.action] | index("admin.audit.exported") != null'

echo "== sign-out"
as AUDITOR POST /admin/v1/logout ""
[[ $STATUS == 204 ]] || { echo "FAIL logout: $STATUS" >&2; exit 1; }
echo "ok   AUDITOR signs out"
as AUDITOR GET /admin/v1/me ""
expect 401 ADMIN_UNAUTHORIZED "the session is gone"
remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $EMAIL_OPERATOR --reason 'e2e disable check'" >/dev/null
as OPERATOR GET /admin/v1/me ""
expect 401 ADMIN_UNAUTHORIZED "disabling an administrator ends their sessions"
echo "all admin console checks passed"
