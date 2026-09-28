#!/usr/bin/env bash
# End-to-end check of the H5 as nginx serves it: SPA fallback, cache
# headers and the PWA manifest, then the browser smoke test
# (web/h5/e2e/smoke.mjs, headless Chrome; skipped when no Chrome is found).
#
#   scripts/e2e/h5.sh            # or BASE=http://localhost:5173 scripts/e2e/h5.sh (dev server)
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

echo "== static site"
header() { tr -d '\r' | awk -F': ' -v h="$1" 'tolower($1) == h {print $2}'; }
index=$(curl -s "$BASE/")
grep -q '<div id="root">' <<<"$index" || { echo "FAIL index.html: $index" >&2; exit 1; }
echo "ok   index.html served"
deep=$(curl -s -D - -o "$WORK/deep" "$BASE/transfer")
[[ $(head -1 <<<"$deep") == *" 200"* ]] && grep -q '<div id="root">' "$WORK/deep" || { echo "FAIL SPA fallback: $deep" >&2; exit 1; }
echo "ok   deep links fall back to index.html"
if [[ "$BASE" != http://localhost* ]]; then
  [[ $(header cache-control <<<"$deep") == "no-cache" ]] || { echo "FAIL index.html must be revalidated: $deep" >&2; exit 1; }
  asset=$(grep -oE '/assets/index-[A-Za-z0-9_-]+\.js' <<<"$index" | head -1)
  cache=$(curl -s -D - -o /dev/null "$BASE$asset" | header cache-control)
  [[ "$cache" == *immutable* ]] || { echo "FAIL $asset cache-control: $cache" >&2; exit 1; }
  echo "ok   index.html revalidates, hashed assets are immutable"
fi
manifest=$(curl -s "$BASE/manifest.webmanifest")
jq -e '.icons | map(.sizes) | index("192x192") and index("512x512")' <<<"$manifest" >/dev/null || { echo "FAIL manifest: $manifest" >&2; exit 1; }
echo "ok   PWA manifest with 192 and 512 px icons"

echo "== browser"
CAPTCHA_BYPASS_TOKEN="$BYPASS" APP="$BASE" node "$(dirname "$0")/../../web/h5/e2e/smoke.mjs"
