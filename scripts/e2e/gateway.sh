#!/usr/bin/env bash
# End-to-end check of the gateway's cross-cutting behavior: rate-limit
# headers, idempotent replays and the WebSocket private channels (needs
# Node 22+ for scripts/e2e/lib/ws-check.mjs, and ledger.welcome_credit on).
#
#   scripts/e2e/gateway.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
EMAIL="e2e-gw-$RUN@example.com"
DEVICE="e2e-gw-$RUN"
PASSWORD="e2e gateway $RUN"

echo "== rate-limit headers"
headers=$(curl -s -D - -o /dev/null "$BASE/v1/market/pairs" | tr -d '\r')
grep -qi '^x-ratelimit-limit: 1200' <<<"$headers" && grep -qi '^x-ratelimit-remaining: [0-9]' <<<"$headers" || { echo "FAIL headers: $headers" >&2; exit 1; }
echo "ok   X-RateLimit-* on public requests"

echo "== cross-origin calls of the desktop app"
# Its WebView's origin is tauri://localhost (http://tauri.localhost on
# Windows); it sends bearer tokens, so no credentials are allowed.
preflight=$(curl -s -D - -o /dev/null -X OPTIONS "$BASE/v1/auth/login/password" -H 'Origin: tauri://localhost' \
  -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers: content-type, x-client-type' | tr -d '\r')
grep -q '^HTTP/[0-9.]* 204' <<<"$preflight" && grep -qi '^access-control-allow-origin: tauri://localhost' <<<"$preflight" &&
  grep -qi '^access-control-allow-headers:.*X-Client-Type' <<<"$preflight" && ! grep -qi '^access-control-allow-credentials' <<<"$preflight" ||
  { echo "FAIL preflight from tauri://localhost: $preflight" >&2; exit 1; }
echo "ok   a preflight from tauri://localhost is allowed, without credentials"
simple=$(curl -s -D - -o /dev/null "$BASE/v1/market/pairs" -H 'Origin: http://tauri.localhost' | tr -d '\r')
grep -qi '^access-control-allow-origin: http://tauri.localhost' <<<"$simple" || { echo "FAIL request from http://tauri.localhost: $simple" >&2; exit 1; }
echo "ok   so is a request from http://tauri.localhost (Windows)"
other=$(curl -s -D - -o /dev/null -X OPTIONS "$BASE/v1/auth/login/password" -H 'Origin: https://evil.example' \
  -H 'Access-Control-Request-Method: POST' | tr -d '\r')
if grep -qi '^access-control-allow-origin' <<<"$other"; then
  echo "FAIL another origin was allowed: $other" >&2
  exit 1
fi
echo "ok   other origins get no CORS headers"

echo "== register $EMAIL"
register "$EMAIL" "$DEVICE" "$PASSWORD"
ACCESS=$(jq -r .access_token <<<"$BODY")
AUTH=(-H "Authorization: Bearer $ACCESS")
funded() {
  call GET "/v1/account/balances?account_type=SPOT" "" "${AUTH[@]}"
  [[ $(jq -r '[.balances[] | select(.asset == "USDT")][0].available // "0"' <<<"$BODY") != "0" ]]
}
eventually 40 "welcome funds credited" funded

echo "== idempotent replay"
KEY="gw-$RUN-replay"
body='{"asset":"USDT","amount":"1","from_account_type":"SPOT","to_account_type":"FUTURES"}'
curl -s -o /dev/null -X POST "$BASE/v1/account/transfers" "${AUTH[@]}" -H 'Content-Type: application/json' -H "Idempotency-Key: $KEY" -d "$body"
replay=$(curl -s -D - -o /dev/null -X POST "$BASE/v1/account/transfers" "${AUTH[@]}" -H 'Content-Type: application/json' -H "Idempotency-Key: $KEY" -d "$body" | tr -d '\r')
grep -q '^HTTP/[0-9.]* 201' <<<"$replay" && grep -qi '^idempotent-replayed: true' <<<"$replay" || { echo "FAIL replay: $replay" >&2; exit 1; }
echo "ok   the gateway replays the first response"
call POST /v1/account/transfers '{"asset":"USDT","amount":"1","from_account_type":"SPOT","to_account_type":"FUTURES"}' "${AUTH[@]}" -H "Idempotency-Key: bad key!"
expect 400 COMMON_INVALID_ARGUMENT "malformed Idempotency-Key"

echo "== websocket"
node "$(dirname "$0")/lib/ws-check.mjs" "$BASE" "$ACCESS" "$EMAIL" "$PASSWORD"
