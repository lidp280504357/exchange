#!/usr/bin/env bash
# Fault injection: Redis goes away (requirements §4: Redis holds only data
# that may be lost). Signed-in traffic keeps working: rate limits and the
# revocation check fail open. Sending codes and password logins fail
# closed with 503 COMMON_UNAVAILABLE: without their counters the quotas
# and the lockout could not protect accounts. Everything recovers when
# Redis is back. Disrupts sign-ins for about half a minute; always
# restarts Redis.
set -euo pipefail

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'compose "start redis" >/dev/null 2>&1 || true; cleanup_remote' EXIT
EMAIL="e2e-fault-rd-$RUN@example.com"
DEVICE="e2e-fault-rd-$RUN"
PASSWORD="e2e fault $RUN"

register "$EMAIL" "$DEVICE" "$PASSWORD"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")

echo "== redis down"
compose "stop redis" >/dev/null
call GET /v1/market/pairs ""
expect 200 - "public reads work (rate limits fail open)"
call GET /v1/account/balances "" "${AUTH[@]}"
expect 200 - "signed-in requests work (revocation check fails open)"
call POST /v1/auth/otp/request "{\"scene\":\"LOGIN\",\"channel\":\"EMAIL\",\"identifier\":\"$EMAIL\",\"captcha_token\":\"$BYPASS\",\"device_id\":\"$DEVICE\"}"
expect 503 COMMON_UNAVAILABLE "sending codes fails closed"
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 503 COMMON_UNAVAILABLE "password logins fail closed"

echo "== redis back"
compose "start redis" >/dev/null
wait_healthy redis
login_ok() {
  call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
  [[ $STATUS == 200 ]]
}
eventually 60 "password logins work again" login_ok
echo "redis outage survived"
