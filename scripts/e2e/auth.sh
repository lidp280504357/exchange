#!/usr/bin/env bash
# End-to-end check of registration, login, tokens, sessions and step-up
# against a deployed non-production environment (default https://astras.vip).
# It reads codes from the dev inbox and passes human verification with the
# environment's CAPTCHA_BYPASS_TOKEN (taken from .env when not exported).
# Takes about a minute: a second code to the same address must wait 60 s.
#
#   scripts/e2e/auth.sh            # or BASE=http://localhost:8080 scripts/e2e/auth.sh
set -euo pipefail

BASE="${BASE:-https://astras.vip}"
BYPASS="${CAPTCHA_BYPASS_TOKEN:-$(grep '^CAPTCHA_BYPASS_TOKEN=' .env | cut -d= -f2- | tr -d '"')}"
RUN="$(date +%s)"
EMAIL="e2e-$RUN@example.com"
DEVICE="e2e-device-$RUN"
PASSWORD="e2e password $RUN"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

STATUS="" BODY=""

# call METHOD PATH JSON [curl args...] sets STATUS and BODY.
call() {
  local method=$1 path=$2 data=$3
  shift 3
  local args=(-s -o "$WORK/body" -w '%{http_code}' -X "$method" "$BASE$path")
  if [[ -n "$data" ]]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  STATUS=$(curl "${args[@]}" "$@")
  BODY=$(cat "$WORK/body")
}

# expect STATUS CODE WHAT: checks the last call; CODE is "-" for success.
expect() {
  local code
  code=$(jq -r '.code // "-"' <<<"$BODY" 2>/dev/null || true)
  code=${code:--}
  if [[ "$STATUS" != "$1" || "$code" != "$2" ]]; then
    printf 'FAIL %s: got %s %s, want %s %s\n%s\n' "$3" "$STATUS" "$code" "$1" "$2" "$BODY" >&2
    exit 1
  fi
  printf 'ok   %s\n' "$3"
}

# otp SCENE [TOKEN] requests a code for EMAIL and returns a ticket.
otp() {
  local scene=$1 token=${2:-} before inbox code
  local auth=()
  [[ -n "$token" ]] && auth=(-H "Authorization: Bearer $token")
  local target
  target=$(jq -rn --arg e "$EMAIL" '$e|@uri')
  before=$(curl -s "$BASE/v1/dev/messages?target=$target" | jq '.messages | length')
  call POST /v1/auth/otp/request "{\"scene\":\"$scene\",\"channel\":\"EMAIL\",\"identifier\":\"$EMAIL\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$DEVICE\"}" ${auth[@]+"${auth[@]}"}
  expect 200 - "otp/request $scene"
  local challenge
  challenge=$(jq -r .challenge_id <<<"$BODY")
  for _ in $(seq 20); do
    inbox=$(curl -s "$BASE/v1/dev/messages?target=$target")
    if (( $(jq '.messages | length' <<<"$inbox") > before )); then
      break
    fi
    sleep 0.5
  done
  code=$(jq -r '.messages[0].subject' <<<"$inbox" | grep -oE '[0-9]{6}')
  call POST /v1/auth/otp/verify "{\"challenge_id\":\"$challenge\",\"code\":\"$code\",\"device_id\":\"$DEVICE\"}"
  expect 200 - "otp/verify $scene"
  TICKET=$(jq -r .otp_ticket <<<"$BODY")
}

APP=(-H 'X-Client-Type: APP')
FIRST_CODE_AT=$(date +%s)

echo "== register $EMAIL (APP client)"
call GET /v1/auth/terms ""
expect 200 - "terms"
TERMS=$(jq -r .terms_version <<<"$BODY")
RISK=$(jq -r .risk_disclosure_version <<<"$BODY")
otp REGISTER
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
otp STEP_UP "$ACCESS"
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
