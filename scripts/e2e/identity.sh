#!/usr/bin/env bash
# End-to-end check of phone identities and identity changes (acceptance
# criterion 1 of phase 1): sign-up and sign-in by phone, binding an email
# after a step-up by SMS, rebinding the phone (refused with a step-up of
# the same kind, done with one through the email), and signing in with
# the new identities. Needs the auth.sms flag (SMS go to the mock provider
# on the test environment). Takes about two minutes: codes to the same
# address must be 60 seconds apart.
#
#   scripts/e2e/identity.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# Singapore mobile numbers (8 digits starting 91/92), unique per run.
SUFFIX=$(printf '%06d' $((RUN % 1000000)))
PHONE="+6591$SUFFIX"
NEW_PHONE="+6592$SUFFIX"
EMAIL="e2e-id-$RUN@example.com"
DEVICE="e2e-id-$RUN"
PASSWORD="e2e identity $RUN"

login() { # login IDENTIFIER
  call POST /v1/auth/login/password "{\"identifier\":\"$1\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
}

echo "== register by phone $PHONE"
call GET /v1/auth/terms ""
expect 200 - "terms"
TERMS=$(jq -r .terms_version <<<"$BODY")
RISK=$(jq -r .risk_disclosure_version <<<"$BODY")
otp_via SMS REGISTER "$PHONE" "$DEVICE"
call POST /v1/auth/register/complete "{\"otp_ticket\":\"$TICKET\",\"password\":\"$PASSWORD\",\"terms_version\":\"$TERMS\",\"risk_disclosure_version\":\"$RISK\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 201 - "register with a phone number"
ACCESS=$(jq -r .access_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $ACCESS")
call GET /v1/user/profile "" "${AUTH[@]}"
check '.region == "SG"' "the region comes from the phone number"
login "$PHONE"
expect 200 - "sign in by phone"

echo "== bind an email (step-up by SMS)"
call POST /v1/auth/identity/bind '{"otp_ticket":"x","device_id":"'"$DEVICE"'"}' "${AUTH[@]}"
expect 403 AUTH_STEP_UP_REQUIRED "binding needs a step-up"
wait_resend "$PHONE"
otp_via SMS STEP_UP "$PHONE" "$DEVICE" "$ACCESS"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
expect 200 - "step-up by SMS"
STEP_UP=$(jq -r .step_up_token <<<"$BODY")
otp_via EMAIL BIND_IDENTITY "$EMAIL" "$DEVICE" "$ACCESS"
call POST /v1/auth/identity/bind "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $STEP_UP"
expect 204 - "email bound"
login "$EMAIL"
expect 200 - "sign in by the new email"

echo "== rebind the phone (waits for the resend windows)"
wait_resend "$PHONE"
wait_resend "$EMAIL"
otp_via SMS STEP_UP "$PHONE" "$DEVICE" "$ACCESS"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
SAME_KIND=$(jq -r .step_up_token <<<"$BODY")
otp_via SMS REBIND_IDENTITY "$NEW_PHONE" "$DEVICE" "$ACCESS"
REBIND_TICKET=$TICKET
call POST /v1/auth/identity/rebind "{\"otp_ticket\":\"$REBIND_TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $SAME_KIND"
expect 403 AUTH_STEP_UP_REQUIRED "rebinding the phone with a step-up by that phone is refused"
otp_via EMAIL STEP_UP "$EMAIL" "$DEVICE" "$ACCESS"
call POST /v1/auth/step-up "{\"otp_ticket\":\"$TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}"
OTHER_KIND=$(jq -r .step_up_token <<<"$BODY")
call POST /v1/auth/identity/rebind "{\"otp_ticket\":\"$REBIND_TICKET\",\"device_id\":\"$DEVICE\"}" "${AUTH[@]}" -H "X-Step-Up-Token: $OTHER_KIND"
expect 200 - "rebind with a step-up through the email"
check '.status == "DONE"' "the phone was replaced at once"

echo "== sign in with the new identities"
login "$NEW_PHONE"
expect 200 - "sign in by the new phone"
login "$PHONE"
expect 401 AUTH_PASSWORD_INVALID "the old phone no longer signs in"

echo "== notices"
identity_notices() {
  call GET /v1/notifications "" "${AUTH[@]}"
  [[ $STATUS == 200 && $(jq '[.items[] | select(.type == "IDENTITY_CHANGED")] | length' <<<"$BODY") -ge 2 ]]
}
eventually 30 "bind and rebind produced identity notices" identity_notices

echo "all identity checks passed"
