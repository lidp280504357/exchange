#!/usr/bin/env bash
# End-to-end check of registration, login, tokens, sessions and step-up
# against a deployed non-production environment (default https://astras.vip).
# It reads codes from the dev inbox and passes human verification with the
# environment's CAPTCHA_BYPASS_TOKEN (taken from .env when not exported).
# Takes about a minute: a second code to the same address must wait 60 s.
#
#   scripts/e2e/auth.sh            # or BASE=http://localhost:8080 scripts/e2e/auth.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
EMAIL="e2e-$RUN@example.com"
DEVICE="e2e-device-$RUN"
PASSWORD="e2e password $RUN"
FIRST_CODE_AT=$(date +%s)

echo "== register $EMAIL (APP client)"
call GET /v1/auth/terms ""
expect 200 - "terms"
TERMS=$(jq -r .terms_version <<<"$BODY")
RISK=$(jq -r .risk_disclosure_version <<<"$BODY")
otp REGISTER "$EMAIL" "$DEVICE"
call POST /v1/auth/register/complete "{\"otp_ticket\":\"$TICKET\",\"password\":\"123456789012\",\"country\":\"SG\",\"terms_version\":\"$TERMS\",\"risk_disclosure_version\":\"$RISK\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 400 AUTH_PASSWORD_WEAK "weak password rejected"
call POST /v1/auth/register/complete "{\"otp_ticket\":\"$TICKET\",\"password\":\"$PASSWORD\",\"country\":\"SG\",\"terms_version\":\"$TERMS\",\"risk_disclosure_version\":\"$RISK\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 201 - "register"
ACCESS=$(jq -r .access_token <<<"$BODY")
REFRESH=$(jq -r .refresh_token <<<"$BODY")
USER_ID=$(jq -r .user_id <<<"$BODY")
echo "     user $USER_ID"

echo "== access token"
call GET /v1/auth/sessions ""
expect 401 COMMON_UNAUTHORIZED "sessions without a token"
call GET /v1/auth/sessions "" -H "Authorization: Bearer $ACCESS"
expect 200 - "sessions with the token"
[[ $(jq '.sessions | length' <<<"$BODY") == 1 ]] || { echo "FAIL one session expected: $BODY"; exit 1; }
call GET /v1/auth/sessions "" -H "Authorization: Bearer ${ACCESS}x"
expect 401 COMMON_UNAUTHORIZED "tampered token"

echo "== refresh rotation and replay"
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 200 - "refresh"
NEW_ACCESS=$(jq -r .access_token <<<"$BODY")
sleep 6
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 401 AUTH_SESSION_REVOKED "replayed refresh token revokes the session"
call GET /v1/auth/sessions "" -H "Authorization: Bearer $NEW_ACCESS"
expect 401 AUTH_SESSION_REVOKED "access token of the revoked session"

echo "== password login"
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"wrong password here\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 401 AUTH_PASSWORD_INVALID "wrong password"
call POST /v1/auth/login/password "{\"identifier\":\"nobody-$RUN@example.com\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 401 AUTH_PASSWORD_INVALID "unknown account looks the same"
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 200 - "login (APP)"
ACCESS=$(jq -r .access_token <<<"$BODY")

echo "== browser refresh cookie"
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE-web\"}" -c "$WORK/jar"
expect 200 - "login (WEB)"
[[ $(jq -r '.refresh_token // "none"' <<<"$BODY") == none ]] || { echo "FAIL web body must not carry the refresh token"; exit 1; }
grep -q $'\trt\t' "$WORK/jar" || { echo "FAIL rt cookie missing"; exit 1; }
WEB_SESSION=$(jq -r .session_id <<<"$BODY")
call POST /v1/auth/token/refresh "" -b "$WORK/jar" -c "$WORK/jar" -H 'Origin: https://evil.example'
expect 403 COMMON_FORBIDDEN "refresh from a foreign origin"
call POST /v1/auth/token/refresh "" -b "$WORK/jar" -c "$WORK/jar" -H "Origin: https://astras.vip"
expect 200 - "refresh with the cookie"

echo "== step-up (waits for the 60 s resend window)"
wait=$(( FIRST_CODE_AT + 62 - $(date +%s) ))
(( wait > 0 )) && sleep "$wait"
call DELETE "/v1/auth/sessions/$WEB_SESSION" "" -H "Authorization: Bearer $ACCESS"
expect 403 AUTH_STEP_UP_REQUIRED "revoking another device needs a step-up"
otp STEP_UP "$EMAIL" "$DEVICE" "$ACCESS"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" -H "Authorization: Bearer $ACCESS"
expect 200 - "step-up"
STEP_UP=$(jq -r .step_up_token <<<"$BODY")
call DELETE "/v1/auth/sessions/$WEB_SESSION" "" -H "Authorization: Bearer $ACCESS" -H "X-Step-Up-Token: $STEP_UP"
expect 204 - "revoke the browser session"
call POST /v1/auth/token/refresh "" -b "$WORK/jar" -H "Origin: https://astras.vip"
expect 401 AUTH_SESSION_REVOKED "revoked browser session cannot refresh"

echo "== history and logout"
call GET "/v1/auth/login-history?limit=2" "" -H "Authorization: Bearer $ACCESS"
expect 200 - "login history"
[[ $(jq '.items | length' <<<"$BODY") == 2 && $(jq -r .next_cursor <<<"$BODY") != null ]] || { echo "FAIL history page: $BODY"; exit 1; }
call POST /v1/auth/logout "" -H "Authorization: Bearer $ACCESS"
expect 204 - "logout"
call GET /v1/auth/sessions "" -H "Authorization: Bearer $ACCESS"
expect 401 AUTH_SESSION_REVOKED "token of the ended session"

echo "all auth checks passed"
