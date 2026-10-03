#!/usr/bin/env bash
# Lighthouse on the admin console's pages behind its sign-in (design
# 2026-10-02 §6: a console page scores at least 85 for performance; the
# sign-in page, at least 90, is in admin.json). A throwaway ADMIN (random
# password and authenticator secret on stdin, never printed) signs in with
# the password alone (admin.login_without_totp on the test server); its
# session goes to Lighthouse in a header file in the run's private work
# directory, and the administrator is signed out and disabled when the
# script ends. Reports (JSON and HTML) in .lighthouseci/console/.
#
#   web/lighthouse/console.sh [PATH...]     default: the pages below
set -euo pipefail
cd "$(dirname "$0")/../.."
# common.sh reads the human-check bypass from .env when unset; the console
# needs none.
export CAPTCHA_BYPASS_TOKEN="${CAPTCHA_BYPASS_TOKEN:-none}"
# shellcheck source=../../scripts/e2e/lib/common.sh
source scripts/e2e/lib/common.sh
# shellcheck source=../../scripts/e2e/lib/remote.sh
source scripts/e2e/lib/remote.sh
ADMIN_BASE="${ADMIN_BASE:-https://admin.astras.vip}"
PAGES=("$@")
((${#PAGES[@]})) || PAGES=(/ /users /orders /withdrawals /approvals /instruments /sim /reports /audit)
MIN_SCORE="${MIN_SCORE:-0.85}"
OUT=.lighthouseci/console
mkdir -p "$OUT"

EMAIL="e2e-lighthouse-$RUN@example.com"
pw=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true)
secret=$(LC_ALL=C tr -dc 'A-Z2-7' </dev/urandom | head -c 32 || true)
out=$(remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin create --email $EMAIL --name 'lighthouse' --role ADMIN --secrets-stdin" \
  "$(printf '%s\n%s\n' "$pw" "$secret")")
grep -q "^created .* $EMAIL (ADMIN)" <<<"$out" || { echo "FAIL admin create: $out" >&2; exit 1; }
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $EMAIL --reason \"lighthouse run over\"" >/dev/null'
JAR="$WORK/jar"
curl -s -o /dev/null -c "$JAR" -H 'X-Admin-CSRF: 1' -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg e "$EMAIL" --arg p "$pw" '{email: $e, password: $p, totp_code: ""}')" "$ADMIN_BASE/admin/v1/login"
session=$(awk '$6 == "admin_session" {print $7}' "$JAR")
[[ -n $session ]] || { echo "FAIL the throwaway administrator did not sign in" >&2; exit 1; }
# shellcheck disable=SC2016 # expanded when the script ends
at_exit 'curl -s -o /dev/null -b "$JAR" -H "X-Admin-CSRF: 1" -X POST "$ADMIN_BASE/admin/v1/logout"'
HEADERS="$WORK/headers.json"
jq -nc --arg c "admin_session=$session" '{Cookie: $c}' >"$HEADERS"

failed=0
printf '%-14s %6s %6s %6s %8s %8s\n' page perf a11y best LCP CLS
for p in "${PAGES[@]}"; do
  name=$(tr '/' '-' <<<"${p#/}")
  name=${name:-overview}
  pnpm --dir web dlx lighthouse@12 "$ADMIN_BASE$p" --preset=desktop --extra-headers="$HEADERS" --chrome-flags="--headless=new" \
    --only-categories=performance,accessibility,best-practices --output=json --output=html --output-path="$PWD/$OUT/$name" --quiet ||
    { echo "FAIL lighthouse on $p" >&2; failed=1; continue; }
  report="$OUT/$name.report.json"
  # A page that bounced to the sign-in measured the wrong page.
  [[ $(jq -r .finalDisplayedUrl "$report") == "$ADMIN_BASE$p" ]] || { echo "FAIL $p ended at $(jq -r .finalDisplayedUrl "$report")" >&2; failed=1; }
  jq -r --arg p "$p" '[$p, (.categories.performance.score, .categories.accessibility.score, .categories["best-practices"].score | . * 100 | round),
    (.audits["largest-contentful-paint"].displayValue // "-"), (.audits["cumulative-layout-shift"].displayValue // "-")] | @tsv' "$report" |
    awk -F'\t' '{printf "%-14s %6s %6s %6s %8s %8s\n", $1, $2, $3, $4, $5, $6}'
  jq -e --argjson min "$MIN_SCORE" '.categories.performance.score >= $min' "$report" >/dev/null ||
    { echo "FAIL $p scores below $MIN_SCORE for performance" >&2; failed=1; }
done
exit $failed
