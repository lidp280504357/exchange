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
# an account, cancelling its orders, a pair's status round trip, a flag
# round trip, a two-person ledger adjustment, the settings and
# single-person mode (adjustments booked alone, one above the limit
# waiting and withdrawn), the counts and their event stream, the
# withdrawal list, the perpetual contracts (states, reduce-only, a status
# round trip, the liquidation monitor, a two-person insurance fund
# contribution), the reports, the audit trail and sign-out.
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
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin create --email $email --name 'e2e $lower' --role $role --secrets-stdin" \
    "$(printf '%s\n%s\n' "$pw" "$sec")")
  grep -q "^created .* $email ($role)" <<<"$out" || { echo "FAIL admin create $role: $out" >&2; exit 1; }
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
as() { # as ROLE METHOD PATH JSON: a call with the role's session
  local role=$1
  shift
  acall "$1" "$2" "$3" -b "$WORK/$role.jar" "${CSRF[@]}"
}

echo "== sign-in"
acall GET /admin/v1/login-options ""
expect 200 - "the sign-in options need no session"
TOTP_REQUIRED=$(jq -r .totp_required <<<"$BODY")
login ADMIN
expect 200 - "ADMIN signs in with password and code"
check ".admin.role == \"ADMIN\" and (.admin.permissions | length) == 16" "with every permission"
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

echo "== a pair's status (ETH-BTC)"
as OPERATOR POST /admin/v1/instruments/pairs/ETH-BTC/status '{"to":"HALT","reason":"e2e halt"}'
expect 200 - "OPERATOR halts ETH-BTC"
check '.from == "TRADING" and .to == "HALT"' "TRADING → HALT"
# shellcheck disable=SC2016 # a safety net: the pair trades again whatever happens
at_exit 'as OPERATOR POST /admin/v1/instruments/pairs/ETH-BTC/status "{\"to\":\"TRADING\",\"reason\":\"e2e cleanup\"}"'
as OPERATOR POST /admin/v1/instruments/pairs/ETH-BTC/status '{"to":"PREPARE","reason":"e2e"}'
expect 409 INSTRUMENT_STATUS_TRANSITION_INVALID "HALT cannot go back to PREPARE"
as OPERATOR POST /admin/v1/instruments/pairs/ETH-BTC/status '{"to":"TRADING","reason":"e2e resume"}'
expect 200 - "and resumes it"
as AUDITOR GET /admin/v1/instruments ""
expect 200 - "instruments"
check '(.pairs[] | select(.symbol == "ETH-BTC") | .status) == "TRADING" and (.assets | map(.asset_code) | index("ETH")) != null' "ETH-BTC trades again; assets are listed"

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
check '(.withdrawals | type) == "number" and (.approvals | type) == "number" and (.partial | length) == 0' "withdrawals and fund operations"
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
expect 200 - "OPERATOR halts ETH-USDT-PERP"
check '.from == "TRADING" and .to == "HALT"' "TRADING → HALT"
# shellcheck disable=SC2016 # a safety net: the contract trades again whatever happens
at_exit 'as OPERATOR POST /admin/v1/derivatives/contracts/ETH-USDT-PERP/status "{\"to\":\"TRADING\",\"reason\":\"e2e cleanup\"}"'
as OPERATOR POST /admin/v1/derivatives/contracts/ETH-USDT-PERP/status '{"to":"TRADING","reason":"e2e resume"}'
expect 200 - "and resumes it"
as AUDITOR GET /admin/v1/instruments ""
check '(.contracts[] | select(.symbol == "ETH-USDT-PERP") | .status) == "TRADING"' "the instruments list the contracts"
as AUDITOR GET /admin/v1/derivatives/risk ""
expect 200 - "positions near liquidation"
check '.positions | type == "array"' "a list"
as AUDITOR GET "/admin/v1/derivatives/liquidations?days=30" ""
expect 200 - "liquidation steps"
check '.items | type == "array"' "a list"
as AUDITOR GET "/admin/v1/derivatives/liquidations?kind=SIDEWAYS" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown kind"

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
eventually 60 "the flag switches are audited" audited AUDITOR "target=flag:market.reference_kline" \
  "[.items[] | select(.actor == \"$EMAIL_OPERATOR\")] | length >= 2"

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
