#!/usr/bin/env bash
# Checks that personal data stays out of the analytics store and the logs
# (acceptance criterion 3 of phase 1, requirements §12.1): a fresh user's
# email, and the codes sent to it, may appear in ClickHouse and in the
# container logs only masked. Reaches the server with REMOTE (default:
# ssh exchange).
#
#   scripts/e2e/pii.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"
EMAIL="e2e-pii-$RUN@example.com"
MASK="e***@example.com"
DEVICE="e2e-pii-$RUN"
PASSWORD="e2e pii $RUN"

echo "== a user with some history"
register "$EMAIL" "$DEVICE" "$PASSWORD"
USER_ID=$(jq -r .user_id <<<"$BODY")
CODE=$(curl -s "$BASE/v1/dev/messages?target=$(jq -rn --arg e "$EMAIL" '$e|@uri')" | jq -r '.messages[0].subject' | grep -oE '[0-9]{6}')
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"wrong password here\",\"device_id\":\"$DEVICE\"}" "${APP[@]}"
expect 401 AUTH_PASSWORD_INVALID "a failed login (LoginFailed event)"
call POST /v1/auth/login/password "{\"identifier\":\"$EMAIL\",\"password\":\"$PASSWORD\",\"device_id\":\"$DEVICE-2\"}" "${APP[@]}"
expect 200 - "a login from a new device (LoginSucceeded, notice)"

echo "== ClickHouse"
ingested() { (( $(ch "SELECT count() FROM events WHERE aggregate_id = '$USER_ID'") >= 4 )); }
eventually 60 "the user's events reached ClickHouse" ingested
masked=$(ch "SELECT count() FROM events WHERE aggregate_id = '$USER_ID' AND position(payload, '$MASK') > 0")
(( masked >= 1 )) || { echo "FAIL the events should carry the masked email $MASK" >&2; exit 1; }
echo "ok   events carry the email masked ($masked with $MASK)"
for table in events audit_logs; do
  leaked=$(ch "SELECT count() FROM $table WHERE position(payload, '$EMAIL') > 0")
  [[ "$leaked" == 0 ]] || { echo "FAIL $leaked rows of $table contain $EMAIL" >&2; exit 1; }
done
echo "ok   no row of events or audit_logs contains the email in clear"
leaked=$(ch "SELECT count() FROM events WHERE aggregate_id = '$USER_ID' AND position(payload, '$CODE') > 0")
[[ "$leaked" == 0 ]] || { echo "FAIL the one-time code appears in $leaked events" >&2; exit 1; }
echo "ok   the one-time code is not in the events"

echo "== logs"
found=$(logs 15m | grep -c -F "$EMAIL" || true)
[[ "$found" == 0 ]] || { echo "FAIL $found log lines contain $EMAIL" >&2; logs 15m | grep -F "$EMAIL" | head -3 >&2; exit 1; }
echo "ok   no container log line of the last 15 minutes contains the email in clear"
found=$(logs 15m | grep -F "$USER_ID" | grep -c -E "(^|[^0-9])$CODE([^0-9]|$)" || true)
[[ "$found" == 0 ]] || { echo "FAIL the one-time code appears in $found log lines about the user" >&2; exit 1; }
echo "ok   the one-time code is not in the logs"

echo "all personal-data checks passed"
