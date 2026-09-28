#!/usr/bin/env bash
# Authenticator apps end to end (implementation plan §6.3 task 8): after a
# step-up by mail a user sets up TOTP, confirms it with a code, and from
# then on step-ups need the app (a mailed code is refused); a code works
# once; the app is removed with a step-up proven by the app itself; both
# changes leave a security notice. Codes come from lib/totp.mjs.
#
#   scripts/e2e/totp.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

EMAIL="e2e-totp-$RUN@example.com"
DEVICE="e2e-totp-$RUN"
echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "e2e totp $RUN"
TOKEN=$(jq -r .access_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $TOKEN")
code() { node "$(dirname "$0")/lib/totp.mjs" "$SECRET" "${1:-0}"; }

call GET /v1/auth/totp "" "${AUTH[@]}"
check '.enabled == false and .pending == false' "no authenticator app yet"
call POST /v1/auth/totp/setup "" "${AUTH[@]}"
expect 403 AUTH_STEP_UP_REQUIRED "setting one up needs a step-up"

echo "== set up after a step-up by mail"
wait_resend "$EMAIL"
otp STEP_UP "$EMAIL" "$DEVICE" "$TOKEN"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
expect 200 - "step-up by mail"
STEP_UP=$(jq -r .step_up_token <<<"$BODY")
call POST /v1/auth/totp/setup "" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP_UP"
expect 200 - "setup"
SECRET=$(jq -r .secret <<<"$BODY")
check '(.otpauth_uri | startswith("otpauth://totp/Exchange:")) and (.secret | test("^[A-Z2-7]{32}$"))' "a base32 secret and an otpauth URI"
call POST /v1/auth/totp/confirm '{"code":"000000"}' "${AUTH[@]}"
expect 422 AUTH_TOTP_INVALID "a wrong code does not bind it"
call POST /v1/auth/totp/confirm "{\"code\":\"$(code)\"}" "${AUTH[@]}"
expect 204 - "the app's code binds it"
call GET /v1/auth/totp "" "${AUTH[@]}"
check '.enabled == true' "bound"

echo "== step-ups need the app"
call POST /v1/auth/step-up "{\"otp_ticket\":\"x\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
expect 403 AUTH_TOTP_REQUIRED "a code by mail is no longer enough"
# The next step's code: the server allows one step of clock skew.
NEXT=$(code 1)
call POST /v1/auth/step-up "{\"totp_code\":\"$NEXT\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
expect 200 - "step-up with the app"
STEP_UP=$(jq -r .step_up_token <<<"$BODY")
call POST /v1/auth/step-up "{\"totp_code\":\"$NEXT\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
expect 422 AUTH_TOTP_INVALID "the same code works once"

echo "== remove it"
call DELETE /v1/auth/totp "" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP_UP"
expect 204 - "removed with the app's step-up"
call GET /v1/auth/totp "" "${AUTH[@]}"
check '.enabled == false' "gone"
notices() {
  call GET "/v1/notifications?limit=10" "" "${AUTH[@]}"
  [[ $(jq '[.items[] | select(.title | test("身份验证器|Authenticator"))] | length' <<<"$BODY") == 2 ]]
}
eventually 40 "security notices for setting up and removing it" notices

echo "all totp checks passed"
