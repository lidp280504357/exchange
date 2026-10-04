#!/usr/bin/env bash
# End-to-end check of the platform profile and the welcome credits (design
# 2026-10-04, batch D1) against a deployed test environment:
#   - renaming the exchange and uploading a favicon show on the PC and
#     mobile sites without a build: the public profile at once, the mobile
#     site's manifest, the console's image proxy, and a browser on each
#     site within a minute (web/e2e/branding.mjs);
#   - the learning banner follows its switch;
#   - closed sign-ups refuse REGISTER codes and new accounts (403
#     AUTH_REGISTRATION_CLOSED) and leave the other codes alone;
#   - with no welcome credits a new account gets nothing, and the profile
#     stops promising them.
# The profile, the image and the credits are put back at the end, also
# after a failure. Changes go through the services' internal endpoints,
# as the admin console makes them.
#
#   scripts/e2e/branding.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"
M_BASE="${M_BASE:-https://m.astras.vip}"
ADMIN_BASE="${ADMIN_BASE:-https://admin.astras.vip}"
ACTOR="e2e:branding"

# internal METHOD SERVICE PORT PATH [JSON] calls a service's internal
# endpoint (the gateway does not route /internal) from the test server and
# sets STATUS and BODY.
internal() {
  local method=$1 svc=$2 port=$3 path=$4 body=${5-} data="" out
  [[ -n "$body" ]] && data="--data-binary @-"
  # shellcheck disable=SC2016 # expanded on the server
  out=$(remote "ip=\$(sudo docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' \$(sudo docker compose $COMPOSE_FILES ps -q $svc) | awk '{print \$1}') && curl -s -m 20 -X $method -H 'Content-Type: application/json' $data -w '\n%{http_code}' http://\$ip:$port$path" "$body")
  STATUS=${out##*$'\n'}
  BODY=${out%$'\n'*}
}

# profile reads the profile as the console does.
profile() {
  internal GET instrument-service 8084 /internal/platform/profile
  [[ $STATUS == 200 ]] || { printf 'FAIL the profile: %s %s\n' "$STATUS" "$BODY" >&2; exit 1; }
}

# set_profile JQ REASON changes the current profile with a jq filter ($orig
# is the profile as it was at the start) and saves it on the version read.
set_profile() {
  local edited
  profile
  edited=$(jq -c --argjson orig "$ORIG" --arg actor "$ACTOR" --arg reason "$2" '. as $cur | ('"$1"') | {name, short_name, domain,
    theme_color, brand_color, footer, contact, social, default_locale, learning_mode, registration, expected_version: $cur.version,
    actor: $actor, reason: $reason}' <<<"$BODY")
  internal PUT instrument-service 8084 /internal/platform/profile "$edited"
}

echo "== the profile as it is"
profile
ORIG=$BODY
ORIG_NAME=$(jq -r .name <<<"$ORIG")
LEARNING_TEXT=$(jq -r '.learning_mode.text["zh-CN"]' <<<"$ORIG")
printf 'ok   %s, version %s, learning mode %s\n' "$ORIG_NAME" "$(jq -r .version <<<"$ORIG")" "$(jq -r .learning_mode.enabled <<<"$ORIG")"
# An uploaded favicon is kept to be put back.
FAVICON_URL=$(jq -r '.images.favicon // ""' <<<"$ORIG")
if [[ -n $FAVICON_URL ]]; then
  FAVICON_MIME=$(curl -s -o "$WORK/favicon" -w '%{content_type}' "$BASE$FAVICON_URL")
  FAVICON_B64=$(base64 <"$WORK/favicon" | tr -d '\n')
fi

PROFILE_BACK=""
restore_profile() {
  [[ -z $PROFILE_BACK ]] || return 0
  PROFILE_BACK=1
  set_profile '$orig' "e2e: put back"
  [[ $STATUS == 200 ]] || echo "warning: the profile was not put back ($STATUS)" >&2
  if [[ -n $FAVICON_URL ]]; then
    internal PUT instrument-service 8084 /internal/platform/images/favicon \
      "{\"data\":\"$FAVICON_B64\",\"mime\":\"$FAVICON_MIME\",\"actor\":\"$ACTOR\",\"reason\":\"e2e: the favicon put back\"}"
  else
    internal DELETE instrument-service 8084 /internal/platform/images/favicon "{\"actor\":\"$ACTOR\",\"reason\":\"e2e: no favicon again\"}"
  fi
}
at_exit restore_profile

echo "== rename the exchange and upload a favicon"
NAME="E2E Exchange $RUN"
set_profile ".name = \"$NAME\" | .short_name = \"E2E\"" "e2e: a new name"
expect 200 - "renamed to $NAME"
SVG='<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" rx="3" fill="#1e40af"/></svg>'
internal PUT instrument-service 8084 /internal/platform/images/favicon \
  "{\"data\":\"$(printf '%s' "$SVG" | base64 | tr -d '\n')\",\"mime\":\"image/svg+xml\",\"actor\":\"$ACTOR\",\"reason\":\"e2e: a favicon\"}"
expect 200 - "a favicon uploaded"
VERSION=$(jq -r .version <<<"$BODY")
ICON=$(jq -r .images.favicon <<<"$BODY")
[[ $ICON == "/v1/platform/images/favicon?v=$VERSION" ]] || { echo "FAIL the favicon's URL $ICON" >&2; exit 1; }

public_named() {
  call GET /v1/platform/profile ""
  [[ $STATUS == 200 && $(jq -r .name <<<"$BODY") == "$NAME" && $(jq -r .images.favicon <<<"$BODY") == "$ICON" ]]
}
eventually 20 "the public profile has the name and the favicon" public_named
TAG=$(curl -s -D - -o /dev/null "$BASE/v1/platform/profile" | tr -d '\r' | awk -F': ' 'tolower($1) == "etag" {print $2}')
[[ $(curl -s -o /dev/null -w '%{http_code}' -H "If-None-Match: $TAG" "$BASE/v1/platform/profile") == 304 ]] ||
  { echo "FAIL no 304 for the ETag $TAG" >&2; exit 1; }
echo "ok   304 for its ETag"
call GET "/v1/platform/images/favicon?v=$VERSION" ""
[[ $STATUS == 200 && $BODY == *"<rect"* ]] || { echo "FAIL the favicon $STATUS" >&2; exit 1; }
echo "ok   the favicon from the API"
[[ $(curl -s -o /dev/null -w '%{http_code} %{content_type}' "$ADMIN_BASE/v1/platform/images/favicon?v=$VERSION") == "200 image/svg+xml" ]] ||
  { echo "FAIL the console's image proxy" >&2; exit 1; }
echo "ok   the console shows it on its own origin"
MANIFEST=$(curl -s "$M_BASE/manifest.webmanifest")
[[ $(jq -r .name <<<"$MANIFEST") == "$NAME" && $(jq -r '.icons[0].src' <<<"$MANIFEST") == "$ICON" ]] ||
  { echo "FAIL the manifest $MANIFEST" >&2; exit 1; }
echo "ok   the mobile site's manifest has the name and the icon"

echo "== the sites show it, without a build"
LEARNING=$(jq -r 'if .learning_mode.enabled then 1 else 0 end' <<<"$ORIG")
for site in pc m; do
  SITE=$site BRAND=$NAME FAVICON=1 LEARNING=$LEARNING BANNER=$LEARNING_TEXT CAPTCHA_BYPASS_TOKEN="$BYPASS" \
    node "$(dirname "$0")/../../web/e2e/branding.mjs"
done

echo "== the learning banner follows its switch"
if [[ $LEARNING == 1 ]]; then OFF=0; else OFF=1; fi
set_profile ".learning_mode.enabled = $([[ $OFF == 1 ]] && echo true || echo false)" "e2e: the banner switched"
expect 200 - "the banner switched"
eventually 20 "the public profile has it" bash -c "curl -s '$BASE/v1/platform/profile' | jq -e '.learning_mode.enabled == $([[ $OFF == 1 ]] && echo true || echo false)'"
SITE=pc BRAND=$NAME FAVICON=1 LEARNING=$OFF BANNER=$LEARNING_TEXT CAPTCHA_BYPASS_TOKEN="$BYPASS" node "$(dirname "$0")/../../web/e2e/branding.mjs"

echo "== closed sign-ups"
set_profile '.registration.status = "CLOSED"' "e2e: sign-ups closed"
expect 200 - "sign-ups closed"
EMAIL="e2e-branding-$RUN@example.com"
DEVICE="e2e-branding-$RUN"
register_code() {
  call POST /v1/auth/otp/request "{\"scene\":\"REGISTER\",\"channel\":\"EMAIL\",\"identifier\":\"$EMAIL\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$DEVICE\"}"
}
refused() { register_code && [[ $STATUS == 403 && $(jq -r .code <<<"$BODY") == AUTH_REGISTRATION_CLOSED ]]; }
eventually 60 "a REGISTER code is refused within the gateway's 15 seconds" refused
call POST /v1/auth/register/complete '{"otp_ticket":"x","password":"x","country":"SG","terms_version":"x","risk_disclosure_version":"x","device_id":"x"}' "${APP[@]}"
expect 403 AUTH_REGISTRATION_CLOSED "and so is an account"
call POST /v1/auth/otp/request "{\"scene\":\"LOGIN\",\"channel\":\"EMAIL\",\"identifier\":\"nobody-$RUN@example.com\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$DEVICE\"}"
expect 200 - "a LOGIN code still goes"
set_profile '.registration.status = "OPEN"' "e2e: sign-ups open"
expect 200 - "sign-ups open again"
opened() { register_code && [[ $STATUS == 200 ]]; }
eventually 60 "a REGISTER code goes again" opened
# That code started the 60-second wait before another to the same address.
date +%s >"$(code_stamp "$EMAIL")"

echo "== no welcome credits"
internal GET ledger-service 8085 /internal/ledger/settings/welcome-credits
expect 200 - "the ledger's welcome credits"
CREDITS=$(jq -c .credits <<<"$BODY")
printf 'ok   %s, flag %s\n' "$CREDITS" "$(jq -r .flag_enabled <<<"$BODY")"
set_credits() { # set_credits JSON_LIST REASON
  internal GET ledger-service 8085 /internal/ledger/settings/welcome-credits
  internal PUT ledger-service 8085 /internal/ledger/settings/welcome-credits \
    "$(jq -c --argjson c "$1" --arg actor "$ACTOR" --arg reason "$2" '{credits: $c, expected_version: .version, actor: $actor, reason: $reason}' <<<"$BODY")"
}
CREDITS_BACK=""
restore_credits() {
  [[ -z $CREDITS_BACK ]] || return 0
  CREDITS_BACK=1
  set_credits "$CREDITS" "e2e: put back"
  [[ $STATUS == 200 ]] || echo "warning: the welcome credits were not put back ($STATUS)" >&2
}
at_exit restore_credits
set_credits '[]' "e2e: no welcome credits"
expect 200 - "the welcome credits cleared"
# The grants read the setting at most 30 seconds old (at once on the
# ledger that took the change); the address's next code waits a minute.
wait_resend "$EMAIL"
register "$EMAIL" "$DEVICE" "e2e branding $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
# A grant would follow the registration within seconds.
sleep 15
call GET "/v1/account/balances?account_type=SPOT" "" "${AUTH[@]}"
expect 200 - "the new account's balances"
check '[.balances[] | select((.available | tonumber) > 0)] | length == 0' "a new account gets nothing"
promised() {
  call GET /v1/platform/profile "" && [[ $(jq -c .welcome_credits <<<"$BODY") == "[]" ]]
}
eventually 150 "the profile stops promising credits within the minute it reads the ledger" promised

echo "== put back"
restore_credits
expect 200 - "the welcome credits put back"
restore_profile
expect 200 - "the profile put back"
back() { call GET /v1/platform/profile "" && [[ $(jq -r .name <<<"$BODY") == "$ORIG_NAME" ]]; }
eventually 20 "the sites see $ORIG_NAME again" back
echo "all platform profile checks passed"
