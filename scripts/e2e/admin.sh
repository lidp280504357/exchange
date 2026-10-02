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
# contribution), the reports, every user's positions and the liquidation
# log, a pair listed from the console (LINK-BTC: a reference symbol checked
# with Binance, previewed, applied, opened, an order resting on it), the
# deposits that need a person (with the
# custodian's stand-in: a deposit whose callback comes late, backfilled
# and then confirmed by it; one below the minimum credited to the user,
# another rejected), a withdrawal's review details and holds, the audit
# trail and sign-out.
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
check ".admin.role == \"ADMIN\" and (.admin.permissions | length) == 21" "with every permission"
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
as AUDITOR GET "/admin/v1/derivatives/liquidations?days=7&kind=FILLED&symbol=ETH-USDT-PERP" ""
expect 200 - "liquidation steps of a kind and a contract"
check 'all(.items[]; .kind == "FILLED" and .symbol == "ETH-USDT-PERP")' "only those"
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
as FINANCE POST "/admin/v1/users/$USER_ID/holds" '{"asset":"USDT","amount":"1.5","reason":"e2e chargeback check"}'
expect 201 - "FINANCE holds 1.5 USDT"
check ".active == true and .amount == \"1.5\" and .actor == \"$EMAIL_FINANCE\"" "an active hold by FINANCE"
HOLD=$(jq -r .id <<<"$BODY")
[[ $(jq -n --arg a "$(spot_usdt frozen)" --arg b "$FROZEN_BEFORE" '($a | tonumber) - ($b | tonumber) == 1.5') == true ]] ||
  { echo "FAIL the hold is frozen: $FROZEN_BEFORE -> $(spot_usdt frozen)" >&2; exit 1; }
echo "ok   1.5 USDT more frozen"
call GET "/v1/account/ledger?asset=USDT&type=ADMIN_FREEZE" "" "${UAUTH[@]}"
expect 200 - "the user's fund flow"
check '(.items | length) == 2 and all(.items[]; .entry_type == "ADMIN_FREEZE")' "shows the hold (available to frozen)"
as FINANCE DELETE "/admin/v1/users/$USER_ID/holds/$HOLD" '{"reason":"e2e cleared"}'
expect 200 - "and releases it"
check '.active == false and .released_by != "" and .release_journal_id != null' "released"
as FINANCE DELETE "/admin/v1/users/$USER_ID/holds/$HOLD" '{"reason":"e2e again"}'
expect 409 LEDGER_HOLD_RELEASED "a hold is released once"
[[ $(spot_usdt frozen) == "$FROZEN_BEFORE" ]] || { echo "FAIL the release: $FROZEN_BEFORE -> $(spot_usdt frozen)" >&2; exit 1; }
echo "ok   the frozen balance is back"
as AUDITOR GET "/admin/v1/users/$USER_ID/holds" ""
check "(.holds | length) == 1 and .holds[0].id == \"$HOLD\"" "the hold stays listed"

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
  check '(.positions | length) >= 1' "HOUSE holds the other side"
  as FINANCE POST "/admin/v1/users/$USER_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e force close"}'
  expect 403 ADMIN_FORBIDDEN "FINANCE closes no positions"
  as OPERATOR POST "/admin/v1/users/$USER_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e force close"}'
  expect 200 - "OPERATOR closes it at the market"
  check '.type == "MARKET" and .reduce_only == true and .side == "SELL" and .quantity == "0.1"' "a reduce-only market sell of 0.1"
  user_flat() {
    as AUDITOR GET "/admin/v1/users/$USER_ID/positions" ""
    [[ $STATUS == 200 ]] && jq -e '.positions | length == 0' <<<"$BODY" >/dev/null
  }
  eventually 40 "the position is closed" user_flat
  as OPERATOR POST "/admin/v1/users/$USER_ID/positions/close" '{"symbol":"ETH-USDT-PERP","position_side":"BOTH","reason":"e2e again"}'
  expect 422 DERIV_NO_POSITION "nothing left to close"
fi

echo "== listing a pair from the console (LINK-BTC)"
as AUDITOR GET /admin/v1/instruments/config ""
expect 200 - "the reference data as a config document"
check '(.pairs | length) >= 50 and (.fee_schedules | map(.tier) | index("default")) != null and (.assets | map(.asset_code) | index("LINK")) != null' \
  "pairs, fee tiers and assets in the reference file's shape"
# The pair's minimum order value is the other of 0.0001 and 0.0002 than
# it has now, so each run changes it (the run's parity collided half the
# time).
NOTIONAL=1
if [[ $(jq '[.pairs[] | select(.symbol == "LINK-BTC") | .min_notional | tonumber == 0.0001] | any' <<<"$BODY") == true ]]; then
  NOTIONAL=2
fi
link_pair() { # link_pair [REFERENCE]: LINK-BTC as a config document
  jq -nc --arg r "${1:-}" --arg n "0.000$NOTIONAL" '{pairs: [{symbol: "LINK-BTC", base_asset: "LINK", quote_asset: "BTC", tick_size: "0.0000001",
    lot_size: "0.1", min_quantity: "0.1", max_quantity: "100000", min_notional: $n, price_band: "0.1", fee_tier: "default", status: "PREPARE",
    reference_symbol: $r, reference_multiplier: "1"}]}'
}
as AUDITOR POST /admin/v1/instruments/preview "{\"config\":$(link_pair)}"
expect 403 ADMIN_FORBIDDEN "AUDITOR previews no change"
as OPERATOR POST /admin/v1/instruments/preview "{\"config\":$(link_pair NOPECOINBTC)}"
expect 422 ADMIN_REFERENCE_UNKNOWN "a reference symbol Binance does not list is refused"
as OPERATOR POST /admin/v1/instruments/preview "{\"config\":$(link_pair LINKBTC)}"
expect 200 - "a preview with Binance's LINKBTC"
check '(.changes | length) == 1 and .changes[0].entity == "TRADING_PAIR" and .changes[0].after.reference_symbol == "LINKBTC" and
  ([.warnings[].code] | index("HOUSE_NOT_LISTED") != null and index("STREAMS_RECONNECT") != null)' "checked, with HOUSE's list and the reconnect noted"
as OPERATOR POST /admin/v1/instruments/apply "{\"config\":$(link_pair),\"reason\":\"e2e lists LINK-BTC\"}"
expect 200 - "OPERATOR applies LINK-BTC (no reference: users trade with each other)"
check "(.changes | length) == 1 and (.changes[0].action == \"CREATE\" or .changes[0].action == \"UPDATE\") and .changes[0].after.min_notional == \"0.000$NOTIONAL\"" \
  "created or changed, versioned"
as OPERATOR POST /admin/v1/instruments/apply "{\"config\":$(link_pair),\"reason\":\"e2e again\"}"
expect 200 - "the same document again"
check '(.changes | length) == 0 and .unchanged == 1' "changes nothing"
pair_listed() {
  call GET /v1/market/pairs ""
  [[ $STATUS == 200 ]] && jq -e --arg n "0.000$NOTIONAL" '.pairs[] | select(.symbol == "LINK-BTC" and .min_notional == $n)' <<<"$BODY" >/dev/null
}
eventually 60 "the sites list LINK-BTC as changed" pair_listed
LINK_STATUS=$(jq -r '.pairs[] | select(.symbol == "LINK-BTC") | .status' <<<"$BODY")
if [[ $LINK_STATUS != TRADING ]]; then
  as OPERATOR POST /admin/v1/instruments/pairs/LINK-BTC/status '{"to":"TRADING","reason":"e2e opens LINK-BTC"}'
  expect 200 - "and opens it for trading (from $LINK_STATUS)"
fi
at_exit 'as OPERATOR POST /admin/v1/instruments/pairs/LINK-BTC/status "{\"to\":\"HALT\",\"reason\":\"e2e cleanup\"}" >/dev/null'
link_order() {
  call POST /v1/orders '{"symbol":"LINK-BTC","side":"BUY","type":"LIMIT","price":"0.0001","quantity":"1"}' "${UAUTH[@]}" -H "Idempotency-Key: e2e-admin-link-$RUN"
  [[ $STATUS == 202 ]]
}
eventually 40 "the user rests a buy on LINK-BTC" link_order
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
eventually 40 "and is canceled" link_canceled

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
  # The custodian receives 2 USDT and its callback is held back 45 s: lost, for now.
  at_exit "mock delay --seconds 0 >/dev/null"
  mock delay --seconds 45 >/dev/null
  read -r TRADE TX < <(mock deposit --address "$ADDR" --coin "$USDT_TRC20" --amount 2 | jq -r '"\(.trade_id) \(.tx_id)"')
  mock delay --seconds 0 >/dev/null
  echo "     the custodian received 2 USDT (trade $TRADE); its callback is late"
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
as FINANCE POST "/admin/v1/users/$USER_ID/totp-reset" '{"reason":"e2e lost phone"}'
expect 403 ADMIN_FORBIDDEN "FINANCE resets no authenticator"
as OPERATOR POST "/admin/v1/users/$USER_ID/totp-reset" '{"reason":"e2e lost phone"}'
expect 200 - "OPERATOR resets the authenticator"
check '.removed == false' "there was none"
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
if [[ -n ${BACKFILLED:-} ]]; then
  eventually 60 "the deposit decisions are audited on the account, by FINANCE" audited AUDITOR "target=user:$USER_ID" \
    "[.items[] | select(.actor == \"$EMAIL_FINANCE\") | .payload.action] | ((index(\"admin.deposits.backfill_executed\") != null or index(\"admin.deposits.backfill_requested\") != null) and index(\"wallet.deposit.backfilled\") != null and index(\"ledger.unclaimed_released\") != null and index(\"wallet.deposit.dismissed\") != null)"
fi

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
