#!/usr/bin/env bash
# Risk rules end to end (requirements §5.13, implementation plan §6.3 task
# 1): a login from a second device is scored; a third registration from
# one device is scored for review, and where risk.enforce is on (in the
# test environment only for region AQ, so that other tests are not
# reviewed) user-service moves that account to RISK_REVIEW. Reads the
# assessments with exchangectl over ssh (REMOTE, default: ssh exchange).
#
#   scripts/e2e/risk.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

echo "== risk.enforce is on for region AQ only"
exchangectl flags show risk.enforce >"$WORK/flag" 2>/dev/null || true
jq -e '.enabled and .rules.regions.allow == ["AQ"]' "$WORK/flag" >/dev/null 2>&1 ||
  { echo "FAIL expected risk.enforce on for AQ only; run: exchangectl flags set risk.enforce --on --allow-regions AQ --reason \"e2e: enforce risk rules for the test region\"" >&2; exit 1; }
echo "ok   enforcement limited to the test region"

# assessed USER PATTERN succeeds once an assessment of USER matches PATTERN.
assessed() { exchangectl risk assessments --user "$1" | grep -qE "$2"; }

echo "== a login from a second device"
PASSWORD="e2e risk $RUN"
register "e2e-risk-$RUN@example.com" "e2e-risk-a-$RUN" "$PASSWORD"
USER=$(jq -r .user_id <<<"$BODY")
call POST /v1/auth/login/password "{\"identifier\":\"e2e-risk-$RUN@example.com\",\"password\":\"$PASSWORD\",\"device_id\":\"e2e-risk-b-$RUN\"}" "${APP[@]}"
expect 200 - "password login from a second device"
eventually 40 "the second device is scored (new_device_login, NONE)" assessed "$USER" 'auth\.LoginSucceeded +20 +NONE +false +new_device_login'

echo "== three registrations from one device in the enforced region"
SHARED="e2e-risk-shared-$RUN"
for i in 1 2 3; do
  register "e2e-risk-burst$i-$RUN@example.com" "$SHARED" "$PASSWORD" AQ
  eval "USER$i=$(jq -r .user_id <<<"$BODY")"
done
ACCESS=$(jq -r .access_token <<<"$BODY") REFRESH=$(jq -r .refresh_token <<<"$BODY")
# Other rules may hit as well (frequent test runs share one network), so
# only the device rule is asserted.
eventually 40 "the third is scored for review and enforced" assessed "$USER3" 'auth\.UserRegistered +[0-9]+ +REVIEW +true +.*registration_burst_device'
if assessed "$USER1" registration_burst_device || assessed "$USER2" registration_burst_device; then
  echo "FAIL the device rule hit the first two registrations" >&2
  exit 1
fi
echo "ok   the first two registrations from the device passed"

AUTH=(-H "Authorization: Bearer $ACCESS")
stale_token() {
  call GET /v1/user/profile "" "${AUTH[@]}"
  [[ $STATUS == 401 && $(jq -r .code <<<"$BODY") == AUTH_TOKEN_EXPIRED ]]
}
eventually 40 "user-service changed the status (the old token is sent to refresh)" stale_token
sleep 1 # tokens issued within a second of the change are stale too
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$SHARED\"}" "${APP[@]}"
expect 200 - "refresh"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
call GET /v1/user/profile "" "${AUTH[@]}"
expect 200 - "profile"
check '.status == "RISK_REVIEW"' "the account waits in RISK_REVIEW"
call GET "/v1/user/eligibility?feature=TRANSFER" "" "${AUTH[@]}"
check '.allowed == false and .reason_code == "USER_RISK_REVIEW"' "transfers wait for the review"
exchangectl users show "$USER3" | grep -q 'RISK_RULE' || { echo "FAIL the status history lacks the RISK_RULE reason" >&2; exit 1; }
echo "ok   the history names the reason (RISK_RULE) and the actor"

echo "all risk checks passed"
