#!/usr/bin/env bash
# End-to-end check of profiles, eligibility, account status and user
# notifications against a deployed non-production environment. Status
# changes run exchangectl inside the user-service container, through
# EXCHANGECTL (default: ssh to the test server).
#
#   scripts/e2e/account.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
EXCHANGECTL="${EXCHANGECTL:-ssh exchange sudo docker exec exchange-infra-user-service-1 /app/exchangectl}"
EMAIL="e2e-acct-$RUN@example.com"
DEVICE="e2e-acct-$RUN"
PASSWORD="e2e account $RUN"

echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "$PASSWORD"
ACCESS=$(jq -r .access_token <<<"$BODY")
REFRESH=$(jq -r .refresh_token <<<"$BODY")
USER_ID=$(jq -r .user_id <<<"$BODY")
AUTH=(-H "Authorization: Bearer $ACCESS")

echo "== profile"
call GET /v1/user/profile "" "${AUTH[@]}"
expect 200 - "profile"
check '.status == "ACTIVE" and .region == "SG" and .anti_phishing_code == ""' "new profile is active in SG"
call PATCH /v1/user/profile '{"language":"en","timezone":"Asia/Singapore"}' "${AUTH[@]}"
expect 200 - "change language and time zone"
check '.language == "en" and .timezone == "Asia/Singapore" and .version == 2' "profile updated"
call PATCH /v1/user/profile '{"anti_phishing_code":"Blue42"}' "${AUTH[@]}"
expect 403 AUTH_STEP_UP_REQUIRED "anti-phishing code needs a step-up"
call PATCH /v1/user/profile '{"timezone":"Mars/Olympus"}' "${AUTH[@]}"
expect 400 COMMON_INVALID_ARGUMENT "bad time zone"

echo "== eligibility"
call GET "/v1/user/eligibility?feature=SPOT_TRADE" "" "${AUTH[@]}"
expect 200 - "eligibility"
check '.allowed == true' "spot trading allowed"
call GET "/v1/user/eligibility?feature=DERIVATIVES_TRADE" "" "${AUTH[@]}"
check '.allowed == false and .reason_code == "USER_NOT_ELIGIBLE"' "derivatives switched off (phase 3)"
call GET "/v1/user/eligibility?feature=WITHDRAW" "" "${AUTH[@]}"
check '.allowed == true' "withdrawals on in the test environment (wallet.withdraw)"
call GET "/v1/user/eligibility?feature=MINING" "" "${AUTH[@]}"
expect 400 COMMON_INVALID_ARGUMENT "unknown feature"

echo "== notifications"
has_notice() {
  call GET /v1/notifications "" "${AUTH[@]}"
  [[ $STATUS == 200 && $(jq --arg t "$1" '[.items[] | select(.type == $t)] | length' <<<"$BODY") -ge 1 ]]
}
eventually 20 "welcome notice" has_notice WELCOME
mails_before=$(inbox_count "$EMAIL")
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE-new\"}" "${APP[@]}"
expect 200 - "login from a new device"
eventually 20 "new-device notice" has_notice NEW_DEVICE_LOGIN
security_mail() { (( $(inbox_count "$EMAIL") > mails_before )); }
eventually 20 "new-device mail" security_mail
curl -s "$BASE/v1/dev/messages?target=$(jq -rn --arg e "$EMAIL" '$e|@uri')" | jq -e '.messages[0].subject | test("New device sign-in")' >/dev/null
echo "ok   mail is in the user's language"

echo "== freeze"
$EXCHANGECTL users status "$USER_ID" --to FROZEN --reason E2E_TEST --note "scripts/e2e/account.sh"
stale_token() {
  call GET /v1/user/profile "" "${AUTH[@]}"
  [[ $STATUS == 401 && $(jq -r .code <<<"$BODY") == AUTH_TOKEN_EXPIRED ]]
}
eventually 30 "tokens from before the change are sent to refresh" stale_token
sleep 1 # tokens issued within a second of the change are stale too
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 200 - "refresh"
check '.scope == "read"' "frozen account gets a read-only token"
REFRESH=$(jq -r .refresh_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
call GET /v1/user/profile "" "${AUTH[@]}"
expect 200 - "frozen account may read"
check '.status == "FROZEN"' "status is FROZEN"
call PATCH /v1/user/profile '{"language":"zh-CN"}' "${AUTH[@]}"
expect 403 USER_FROZEN "frozen account may not write"
call GET "/v1/user/eligibility?feature=SPOT_TRADE" "" "${AUTH[@]}"
check '.allowed == false and .reason_code == "USER_FROZEN"' "frozen account may not trade"
eventually 20 "status notice" has_notice STATUS_CHANGED

echo "== unfreeze"
$EXCHANGECTL users status "$USER_ID" --to ACTIVE --reason E2E_TEST_DONE
eventually 30 "read-only token sent to refresh" stale_token
sleep 1
call POST /v1/auth/token/refresh "{\"refresh_token\":\"$REFRESH\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
check '.scope == "full"' "active again"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
call POST /v1/notifications/read '{"all":true}' "${AUTH[@]}"
expect 200 - "mark all read"
call GET /v1/notifications "" "${AUTH[@]}"
check '.unread_count == 0 and (.items | length) >= 4' "inbox read"

echo "all account checks passed"
