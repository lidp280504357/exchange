#!/usr/bin/env bash
# End-to-end check of profiles (the username and avatar too), eligibility,
# account status and user notifications against a deployed non-production
# environment. Status
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
check '.allowed == true' "derivatives on in the test environment (derivatives.trading)"
call GET "/v1/user/eligibility?feature=WITHDRAW" "" "${AUTH[@]}"
check '.allowed == true' "withdrawals on in the test environment (wallet.withdraw)"
call GET "/v1/user/eligibility?feature=MINING" "" "${AUTH[@]}"
expect 400 COMMON_INVALID_ARGUMENT "unknown feature"

echo "== favorites"
call GET /v1/user/favorites "" "${AUTH[@]}"
expect 200 - "favorites"
check '.symbols == [] and .updated_at == null' "none until set"
call PUT /v1/user/favorites '{"symbols":["eth-usdt","BTC-USDT","ETH-USDT","BTC-USDT-PERP"]}' "${AUTH[@]}"
expect 200 - "set favorites"
check '.symbols == ["ETH-USDT","BTC-USDT","BTC-USDT-PERP"] and (.updated_at | type) == "string"' "upper-cased, repeats dropped, order kept"
call GET /v1/user/favorites "" "${AUTH[@]}"
check '.symbols | length == 3' "stored"
call PUT /v1/user/favorites '{"symbols":["BTC/USDT"]}' "${AUTH[@]}"
expect 400 COMMON_INVALID_ARGUMENT "not a market symbol"

echo "== username and avatar (design 2026-10-07, avatars and usernames)"
call GET /v1/user/profile "" "${AUTH[@]}"
check '(.username | test("^user_[a-z0-9]{8}$")) and .username_changed_at == null and .avatar_url == null and .avatar_thumb_url == null' \
  "a drawn username and the default avatar"
NAME="E2e_${RUN: -10}"
call PUT /v1/user/username '{"username":"astras_admin"}' "${AUTH[@]}"
expect 400 USER_USERNAME_INVALID "a reserved name"
call PUT /v1/user/username "{\"username\":\"$NAME\"}" "${AUTH[@]}"
expect 200 - "username changed"
check ".username == \"$NAME\" and (.username_changed_at | type) == \"string\"" "the new name and when"
call PUT /v1/user/username "{\"username\":\"${NAME}x\"}" "${AUTH[@]}"
expect 409 USER_USERNAME_COOLDOWN "once in 7 days"
check '(.details.next_change_at | type) == "string"' "says when it may change again"
# A 64 x 64 checkered PNG.
printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAeklEQVR42uzZsQmAQAwF0CgHuoUzuH/lBM7gFnY6QwQ1B+/XIfAgReC3a5sik2U/U/PHOr+6f4zOAwAAAAAAAAAAAPBfWrX/PrvfCQEAAAAAAAAAAAA8zqAfcEIAAAAAAAAAAAAR+oEa/71+AAAAAAAAAAAAAOCz3AMA/WAZ4C1vwAMAAAAASUVORK5CYII=' |
  base64 --decode >"$WORK/avatar.png"
call POST /v1/user/avatar "" "${AUTH[@]}" -F "file=@$WORK/avatar.png;type=image/png"
expect 200 - "avatar uploaded"
check '(.avatar_url | test("^/uploads/avatars/[0-9a-f-]{36}/[a-z0-9]{16}[.]webp$")) and (.avatar_thumb_url | endswith("_64.webp"))' \
  "the two files' paths"
AVATAR=$(jq -r .avatar_url <<<"$BODY")
GOT=$(curl -s -o "$WORK/avatar.webp" -w '%{http_code} %{content_type}' "$BASE$AVATAR")
[[ $GOT == "200 image/webp" && $(head -c 4 "$WORK/avatar.webp") == RIFF ]] || { echo "FAIL the avatar at $AVATAR: $GOT" >&2; exit 1; }
echo "ok   the avatar is served as WebP ($(wc -c <"$WORK/avatar.webp" | tr -d ' ') bytes)"
printf 'not an image' >"$WORK/bad.png"
call POST /v1/user/avatar "" "${AUTH[@]}" -F "file=@$WORK/bad.png;type=image/png"
expect 400 USER_AVATAR_INVALID "not an image"
call DELETE /v1/user/avatar "" "${AUTH[@]}"
expect 200 - "back to the default avatar"
check '.avatar_url == null and .avatar_thumb_url == null' "no avatar"

echo "== wallet networks and address checks"
call GET "/v1/wallet/networks?asset=eth" "" "${AUTH[@]}"
expect 200 - "ETH networks"
check '(.networks[] | select(.network == "ETH-SEPOLIA")) | .display_name == "Sepolia" and .address_format == "EVM" and .confirmations == 12 and (.explorer_tx_url | contains("{tx}"))' "Sepolia, with its explorer link"
call POST /v1/wallet/withdraw-addresses/validate '{"network":"ETH-SEPOLIA","address":"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"}' "${AUTH[@]}"
expect 200 - "a valid address"
check '.valid == true and .normalized == "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" and .internal == false' "in its checksum case"
call POST /v1/wallet/withdraw-addresses/validate '{"network":"ETH-SEPOLIA","address":"0x5AAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}' "${AUTH[@]}"
check '.valid == false and .reason == "ADDRESS_CHECKSUM"' "a typo in the mixed case"
call POST /v1/wallet/withdraw-addresses/validate '{"network":"ETH-SEPOLIA","address":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"}' "${AUTH[@]}"
check '.valid == false and .reason == "ADDRESS_FORMAT"' "a TRON address on an EVM network"
call POST /v1/wallet/withdraw-addresses/validate '{"network":"NOPE","address":"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"}' "${AUTH[@]}"
expect 404 WALLET_NETWORK_UNKNOWN "an unknown network"

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
# Any of the inbox's mails, not the newest: another may have come since.
in_language() {
  call GET "/v1/dev/messages?target=$(jq -rn --arg e "$EMAIL" '$e|@uri')" "" &&
    jq -e 'any(.messages[]; .subject | test("New device sign-in"))' <<<"$BODY" >/dev/null
}
eventually 20 "mail is in the user's language" in_language

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

echo "== Traditional Chinese (design 2026-10-06 繁体中文 §2.2)"
call PATCH /v1/user/profile '{"language":"zh-TW"}' "${AUTH[@]}"
expect 200 - "language zh-TW"
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE-tw\"}" "${APP[@]}"
expect 200 - "login from another new device"
traditional_notice() {
  call GET /v1/notifications "" "${AUTH[@]}"
  [[ $STATUS == 200 ]] && jq -e 'any(.items[]; .type == "NEW_DEVICE_LOGIN" and .title == "新裝置登入提醒" and (.body | test("您的帳戶於")))' <<<"$BODY" >/dev/null
}
eventually 20 "the new-device notice in Traditional Chinese" traditional_notice
traditional_mail() {
  call GET "/v1/dev/messages?target=$(jq -rn --arg e "$EMAIL" '$e|@uri')" "" &&
    jq -e 'any(.messages[]; (.subject | test("新裝置登入提醒")) and (.body | test("此為安全通知")))' <<<"$BODY" >/dev/null
}
eventually 20 "the new-device mail in Traditional Chinese" traditional_mail

echo "all account checks passed"
