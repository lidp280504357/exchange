#!/usr/bin/env bash
# End-to-end check of the three sites as nginx serves them (ADR-0012): each
# site's page and SPA fallback, cache headers, the device routing between
# astras.vip and m.astras.vip (site_pref overrides it), the admin console's
# security headers, the API reference, the design system catalogue and the
# PWA manifest and service worker; then the browser smoke tests in
# headless Chrome (skipped when no Chrome is found): the PC site
# (web/e2e/pc-smoke.mjs) and the mobile site (web/e2e/m-smoke.mjs).
#
#   scripts/e2e/web.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
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
sw=$(curl -s -D - -o "$WORK/sw" "$M_BASE/sw.js")
[[ $(header cache-control <<<"$sw") == "no-cache" ]] && grep -q 'offline.html' "$WORK/sw" || fail "service worker: $sw"
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
