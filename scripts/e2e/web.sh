#!/usr/bin/env bash
# End-to-end check of the three sites as nginx serves them (ADR-0012): each
# site's page and SPA fallback, cache headers, the device routing between
# astras.vip and m.astras.vip (site_pref overrides it), the admin console's
# security headers, the API reference, the design system catalogue and the
# PWA manifest and service worker; then the browser smoke tests in
# headless Chrome (skipped when no Chrome is found): the PC site
# (web/e2e/pc-smoke.mjs), the mobile site (web/e2e/m-smoke.mjs) and the
# admin console (web/e2e/admin-smoke.mjs, with a throwaway administrator
# made over ssh). The PC and mobile smokes open margin trading for their own
# users (lib/margin-user.sh, margin.enabled put back when the script ends),
# so the script holds the ops lock as the others that change switches; the
# console's smoke opens the cross margin account of a user of this script's
# own, 10 USDT of its welcome funds moved in (and back when it is done).
#
#   scripts/e2e/web.sh
set -euo pipefail
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "e2e $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# One ssh connection for the margin switch and the admin console's steps.
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"
MARGIN_USER_HELPER="$(cd "$(dirname "$0")" && pwd)/lib/margin-user.sh"
export MARGIN_USER_HELPER MARGIN_USER_STATE="$WORK/margin-user" REMOTE
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'bash "$MARGIN_USER_HELPER" back || EXIT_FAILED=1'
M_BASE="${M_BASE:-https://m.astras.vip}"
ADMIN_BASE="${ADMIN_BASE:-https://admin.astras.vip}"
DESKTOP="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"
PHONE="Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"

header() { tr -d '\r' | awk -F': ' -v h="$1" 'tolower($1) == h {print $2}'; }
fail() { echo "FAIL $1" >&2; exit 1; }
ok() { echo "ok   $1"; }
# page URL UA [COOKIE]: the status and the redirect target of a page request.
page() { curl -s -o /dev/null -A "$2" ${3:+-b "$3"} -w '%{http_code} %{redirect_url}' "$1"; }

echo "== PC site ($BASE)"
index=$(curl -s -A "$DESKTOP" "$BASE/")
grep -q '<div id="root">' <<<"$index" || fail "index.html: $index"
deep=$(curl -s -D - -o "$WORK/deep" -A "$DESKTOP" "$BASE/markets")
[[ $(head -1 <<<"$deep") == *" 200"* ]] && grep -q '<div id="root">' "$WORK/deep" || fail "SPA fallback: $deep"
[[ $(header cache-control <<<"$deep") == "no-cache" ]] || fail "index.html must be revalidated: $deep"
asset=$(grep -oE '/static/index-[A-Za-z0-9_-]+\.js' <<<"$index" | head -1)
[[ $(curl -s -D - -o /dev/null "$BASE$asset" | header cache-control) == *immutable* ]] || fail "$asset is not immutable"
[[ $(curl -s -D - -o /dev/null -H 'Accept-Encoding: gzip' "$BASE$asset" | header content-encoding) == gzip ]] || fail "$asset is not compressed"
ok "pages fall back to index.html, which revalidates; hashed assets are immutable and compressed"

echo "== device routing"
[[ $(page "$BASE/markets" "$PHONE") == "302 $M_BASE/markets" ]] || fail "a phone on the PC site: $(page "$BASE/markets" "$PHONE")"
[[ $(page "$BASE/markets" "$PHONE" "site_pref=pc") == "200 " ]] || fail "site_pref=pc keeps a phone on the PC site"
[[ $(page "$M_BASE/trade/BTC-USDT" "$DESKTOP") == "302 $BASE/trade/BTC-USDT" ]] || fail "a desktop on the mobile site: $(page "$M_BASE/trade/BTC-USDT" "$DESKTOP")"
[[ $(page "$M_BASE/trade/BTC-USDT" "$DESKTOP" "site_pref=m") == "200 " ]] || fail "site_pref=m keeps a desktop on the mobile site"
[[ $(page "$BASE/docs/" "$PHONE") == "200 " ]] || fail "the API reference is not routed: $(page "$BASE/docs/" "$PHONE")"
[[ $(page "$BASE/h5/transfer" "$PHONE") == "301 $BASE/" ]] || fail "the retired H5 must lead home: $(page "$BASE/h5/transfer" "$PHONE")"
ok "phones go to m.astras.vip and desktops back, path kept; site_pref overrides both; /h5/ leads home"

echo "== mobile site ($M_BASE)"
mindex=$(curl -s -A "$PHONE" "$M_BASE/")
grep -q '<div id="root">' <<<"$mindex" || fail "mobile index.html"
mdeep=$(curl -s -D - -o "$WORK/mdeep" -A "$PHONE" "$M_BASE/assets/transfer")
[[ $(head -1 <<<"$mdeep") == *" 200"* ]] && grep -q '<div id="root">' "$WORK/mdeep" || fail "mobile SPA fallback: $mdeep"
masset=$(grep -oE '/static/index-[A-Za-z0-9_-]+\.js' <<<"$mindex" | head -1)
[[ $(curl -s -D - -o /dev/null "$M_BASE$masset" | header cache-control) == *immutable* ]] || fail "$masset is not immutable"
manifest=$(curl -s "$M_BASE/manifest.webmanifest")
jq -e '.icons | map(.sizes) | index("192x192") and index("512x512")' <<<"$manifest" >/dev/null || fail "manifest: $manifest"
# nginx serves sw.js with no-cache, but Cloudflare's browser TTL rewrites it for every .js; update checks
# of a service worker bypass the HTTP cache anyway (updateViaCache "imports").
sw=$(curl -s -D - -o "$WORK/sw" "$M_BASE/sw.js")
[[ $(header content-type <<<"$sw") == *javascript* ]] && grep -q 'offline.html' "$WORK/sw" || fail "service worker: $sw"
grep -q '<html' <<<"$(curl -s "$M_BASE/offline.html")" || fail "the offline page is missing"
ok "the mobile site: pages fall back to index.html, hashed assets immutable, PWA manifest, service worker and offline page"

echo "== admin console ($ADMIN_BASE)"
# The console is restricted by an IP allowlist (403 elsewhere) or by
# Cloudflare Access (a redirect to its login); from an allowed address the
# page, its security headers and the API's session check are verified.
admin=$(curl -s -D - -o "$WORK/admin" "$ADMIN_BASE/")
status=$(head -1 <<<"$admin" | awk '{print $2}')
if [[ $status == 403 ]]; then
  ok "the console is restricted to the IP allowlist (403 from here)"
elif [[ $status == 302 && $(header location <<<"$admin") == *cloudflareaccess.com* ]]; then
  ok "the console is behind Cloudflare Access"
else
  grep -q '<div id="root">' "$WORK/admin" || fail "admin index.html: $admin"
  [[ $(header x-frame-options <<<"$admin") == DENY && $(header x-robots-tag <<<"$admin") == "noindex, nofollow" ]] || fail "admin headers: $admin"
  [[ $(header content-security-policy <<<"$admin") == *"frame-ancestors 'none'"* ]] || fail "admin CSP: $admin"
  code=$(curl -s "$ADMIN_BASE/admin/v1/me" | jq -r .code)
  [[ $code == ADMIN_UNAUTHORIZED ]] || fail "admin API without a session: $code"
  ok "the console with its security headers; its API asks for a session"
fi

echo "== API reference and design system"
grep -q '<redoc spec-url="/docs/openapi.json"' <<<"$(curl -s "$BASE/docs/")" || fail "/docs/ is not the API reference"
spec=$(curl -s "$BASE/docs/openapi.json")
jq -e '.paths["/v1/market/summary"].get and .paths["/v1/user/favorites"].put and ([."x-tagGroups"[].name] | length) >= 6' <<<"$spec" >/dev/null ||
  fail "/docs/openapi.json is not the merged contract"
ok "API reference at /docs/ ($(jq '.paths | length' <<<"$spec") paths)"
curl -s "$BASE/storybook/index.json" | jq -e '.entries | length > 20' >/dev/null || fail "/storybook/ has no stories"
ok "design system catalogue at /storybook/ ($(curl -s "$BASE/storybook/index.json" | jq '.entries | length') stories)"

echo "== PC site in the browser"
CAPTCHA_BYPASS_TOKEN="$BYPASS" APP="$BASE" node "$(dirname "$0")/../../web/e2e/pc-smoke.mjs"

echo "== mobile site in the browser"
CAPTCHA_BYPASS_TOKEN="$BYPASS" APP="$M_BASE" node "$(dirname "$0")/../../web/e2e/m-smoke.mjs"

echo "== a margin account for the console"
# The console's smoke opens a margin account's detail (margin design §8,
# E5): this user's cross account with 10 USDT of its welcome funds, moved
# back once the smoke is done (or when the script ends) under the same key.
MEMAIL="e2e-console-margin-$RUN@example.com"
register "$MEMAIL" "e2e-console-margin-$RUN" "e2e console margin $RUN"
MARGIN_USER_ID=$(jq -r .user_id <<<"$BODY")
MAUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
bash "$MARGIN_USER_HELPER" on "$MARGIN_USER_ID"
margin_funded() {
  call GET /v1/account/balances "" "${MAUTH[@]}"
  jq -e '([.balances[] | select(.asset == "USDT" and .account_type == "SPOT")][0].available // "0" | tonumber) >= 10' <<<"$BODY" >/dev/null
}
eventually 40 "the console's margin user has its welcome funds" margin_funded
margin_out() {
  call POST /v1/margin/transfer '{"direction":"OUT","account":"MARGIN_CROSS","asset":"USDT","amount":"10"}' "${MAUTH[@]}" \
    -H "Idempotency-Key: e2e-console-margin-$RUN-out"
}
call POST /v1/margin/transfer '{"direction":"IN","account":"MARGIN_CROSS","asset":"USDT","amount":"10"}' "${MAUTH[@]}" \
  -H "Idempotency-Key: e2e-console-margin-$RUN-in"
[[ $STATUS == 200 ]] || fail "10 USDT into the console's margin account: $STATUS $BODY"
at_exit 'margin_out >/dev/null'
ok "a cross margin account with 10 USDT for the console ($MARGIN_USER_ID)"

echo "== admin console in the browser"
# A throwaway administrator (random password and authenticator secret on
# stdin, never printed), disabled when the script ends; the browser signs
# in with the password alone (admin.login_without_totp on the test server).
ADMIN_EMAIL="e2e-console-$RUN@example.com"
ADMIN_PASSWORD=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true)
secret=$(LC_ALL=C tr -dc 'A-Z2-7' </dev/urandom | head -c 32 || true)
out=$(remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin create --email $ADMIN_EMAIL --name 'e2e console' --role ADMIN --secrets-stdin" \
  "$(printf '%s\n%s\n' "$ADMIN_PASSWORD" "$secret")")
grep -q "^created .* $ADMIN_EMAIL (ADMIN)" <<<"$out" || fail "admin create: $out"
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $ADMIN_EMAIL --reason \"e2e run over\"" >/dev/null'
ADMIN_EMAIL="$ADMIN_EMAIL" ADMIN_PASSWORD="$ADMIN_PASSWORD" APP="$ADMIN_BASE" CAPTCHA_BYPASS_TOKEN="$BYPASS" MARGIN_USER_ID="$MARGIN_USER_ID" \
  node "$(dirname "$0")/../../web/e2e/admin-smoke.mjs"
margin_out
[[ $STATUS == 200 ]] || fail "the console's margin account's 10 USDT back to SPOT: $STATUS $BODY"
ok "the console's margin account's 10 USDT back in its SPOT account"
