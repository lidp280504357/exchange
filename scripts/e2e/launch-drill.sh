#!/usr/bin/env bash
# The launch drill (design 2026-10-04 §6, D3's "演练上线"): go live from
# the admin console alone on the test server, check it, and go back.
#   1. With a throwaway ADMIN, through the console's API: no welcome
#      credits (a fresh account then gets nothing), the exchange's name,
#      short name, mark and favicon, its domain, the learning banner off,
#      sign-ups open, the six legal pages and the home hero published from
#      the bundled drafts ("publish the default"), the platform coin's
#      profile (set unless it is).
#   2. The launch checklist: every item the console sets is OK; the others
#      (switches the test server keeps by decision, the custodian's
#      stand-in, third parties, administrators, items added later) are
#      printed, not failed.
#   3. The sites: the name and title within a minute, no learning banner,
#      no test-environment badge, no welcome-credit copy, the terms served
#      (web/e2e/branding.mjs).
#   4. Back to the learning setup at the end, also after a failure, and
#      checked: banner on, the credits as they were, the built-in name.
#
#   scripts/e2e/launch-drill.sh
set -euo pipefail
# It changes what every visitor sees: one run at a time on the server, as
# the fault drills (task e2e holds the lock for all the scripts).
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "e2e $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
# shellcheck source=lib/remote.sh
source "$(dirname "$0")/lib/remote.sh"

# Only on the test server: its stand-in custodian (ADR-0017), which no
# live deployment has, says so (as custody.sh checks).
if [[ $(remote "sudo docker compose $COMPOSE_FILES exec -T wallet-service printenv UDUNMOCK_GATEWAY_URL </dev/null || true") != http://udun-mock:* ]]; then
  echo "SKIP launch drill: no stand-in custodian, so not the test server"
  exit 0
fi
ADMIN_BASE="${ADMIN_BASE:-https://admin.astras.vip}"
M_BASE="${M_BASE:-https://m.astras.vip}"
CONTENT="$(dirname "$0")/../../web/packages/core/content"
START=$SECONDS

# The items the drill sets through the console; the checklist's others are
# the deployment's or the test server's (its switches by the user's
# decisions: sign-in without an authenticator, the hidden test asset).
CONSOLE_ITEMS="welcome_credits learning_mode registration brand domain coin_profile legal"

# --- the console's API --------------------------------------------------
CSRF=(-H 'X-Admin-CSRF: 1')
JAR="$WORK/admin.jar"
acall() { # acall METHOD PATH JSON: call on the console's domain with the session
  local user_base=$BASE rc=0
  BASE=$ADMIN_BASE
  if [[ $1 == GET ]]; then
    call "$1" "$2" "$3" -b "$JAR" "${CSRF[@]}" || rc=$?
  else
    call "$1" "$2" "$3" -b "$JAR" "${CSRF[@]}" -H "Idempotency-Key: drill-$RUN-$RANDOM$RANDOM" || rc=$?
  fi
  BASE=$user_base
  return $rc
}

# internal METHOD SERVICE PORT PATH [JSON] calls a service's internal
# endpoint from the test server (the restore does not depend on a console
# session) and sets STATUS and BODY.
internal() {
  local method=$1 svc=$2 port=$3 path=$4 body=${5-} data="" out
  [[ -n "$body" ]] && data="--data-binary @-"
  # shellcheck disable=SC2016 # expanded on the server
  out=$(remote "ip=\$(sudo docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' \$(sudo docker compose $COMPOSE_FILES ps -q $svc) | awk '{print \$1}') && curl -s -m 20 -X $method -H 'Content-Type: application/json' $data -w '\n%{http_code}' http://\$ip:$port$path" "$body")
  STATUS=${out##*$'\n'}
  BODY=${out%$'\n'*}
}

# md_text FILE LOCALE prints a bundled draft as an article text: the front
# matter's title and summary, and the body.
md_text() {
  node -e '
    const fs = require("fs");
    const [file, locale] = process.argv.slice(1);
    const src = fs.readFileSync(file, "utf8");
    const m = /^---\n([\s\S]*?)\n---\n?([\s\S]*)$/.exec(src);
    const data = {};
    for (const line of (m ? m[1] : "").split("\n")) {
      const i = line.indexOf(":");
      if (i > 0) data[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    }
    process.stdout.write(JSON.stringify({ locale, title: data.title || "", summary: data.summary || "", body: (m ? m[2] : src).trim() }));
  ' "$1" "$2"
}

echo "== a throwaway ADMIN"
EMAIL="e2e-drill-$RUN@example.com"
PW=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true)
SECRET=$(LC_ALL=C tr -dc 'A-Z2-7' </dev/urandom | head -c 32 || true)
out=$(remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin create --email $EMAIL --name 'e2e launch drill' --role ADMIN --secrets-stdin" \
  "$(printf '%s\n%s\n' "$PW" "$SECRET")" 2>&1 || true)
grep -qE "^created .* $EMAIL \(ADMIN\)|ADMIN_EXISTS" <<<"$out" || { echo "FAIL admin create: $out" >&2; exit 1; }
# shellcheck disable=SC2016 # expanded when the script ends
at_exit "remote \"sudo docker compose \$COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $EMAIL --reason 'e2e run over'\" >/dev/null"
BASE_USER=$BASE
BASE=$ADMIN_BASE
call POST /admin/v1/login "$(jq -nc --arg e "$EMAIL" --arg p "$PW" --arg c "$(node "$(dirname "$0")/lib/totp.mjs" "$SECRET" 0)" \
  '{email: $e, password: $p, totp_code: $c}')" "${CSRF[@]}" -c "$JAR"
BASE=$BASE_USER
expect 200 - "signed in to the console"

echo "== the learning setup, to go back to"
acall GET /admin/v1/platform/profile ""
expect 200 - "the platform's profile"
ORIG=$BODY
acall GET /admin/v1/platform/welcome-credits ""
expect 200 - "the welcome credits"
ORIG_CREDITS=$(jq -c .credits <<<"$BODY")
printf 'ok   %s, learning mode %s, credits %s\n' "$(jq -r .name <<<"$ORIG")" "$(jq -r .learning_mode.enabled <<<"$ORIG")" "$ORIG_CREDITS"
CREATED=()
UPLOADED=()

# back_to_learning puts the learning setup back through the services'
# internal endpoints: the profile, its images, the credits; the pages the
# drill published are deleted, so the sites show their bundled drafts again
# (taking them off would hide those too).
BACK=""
back_to_learning() {
  [[ -z $BACK ]] || return 0
  BACK=1
  internal GET instrument-service 8084 /internal/platform/profile
  internal PUT instrument-service 8084 /internal/platform/profile "$(jq -c --argjson cur "$BODY" '{name, short_name, domain, theme_color,
    brand_color, footer, contact, social, default_locale, learning_mode, registration, expected_version: $cur.version, actor: "e2e:launch-drill",
    reason: "launch drill: back to the learning setup"}' <<<"$ORIG")"
  [[ $STATUS == 200 ]] || echo "warning: the profile was not put back ($STATUS $BODY)" >&2
  local kind
  for kind in ${UPLOADED[@]+"${UPLOADED[@]}"}; do
    internal DELETE instrument-service 8084 "/internal/platform/images/$kind" '{"actor":"e2e:launch-drill","reason":"launch drill: the built-in image again"}'
  done
  internal GET ledger-service 8085 /internal/ledger/settings/welcome-credits
  internal PUT ledger-service 8085 /internal/ledger/settings/welcome-credits \
    "$(jq -c --argjson c "$ORIG_CREDITS" '{credits: $c, expected_version: .version, actor: "e2e:launch-drill", reason: "launch drill: back to the learning setup"}' <<<"$BODY")"
  [[ $STATUS == 200 ]] || echo "warning: the welcome credits were not put back ($STATUS $BODY)" >&2
  if ((${#CREATED[@]} > 0)); then
    pg "DELETE FROM notify.articles WHERE id IN ($(printf "'%s'," "${CREATED[@]}" | sed 's/,$//'))" >/dev/null ||
      echo "warning: the drill's pages were not deleted" >&2
  fi
}
at_exit back_to_learning

echo "== go live: no welcome credits, and a fresh account gets nothing"
acall PUT /admin/v1/platform/welcome-credits "$(jq -c '{credits: [], expected_version: .version, reason: "launch drill: no welcome credits"}' <<<"$BODY")"
expect 200 - "the welcome credits cleared by one ADMIN (lowering)"
# Before any renaming: the code's mail carries the name notification-service read.
USER_EMAIL="e2e-drill-user-$RUN@example.com"
register "$USER_EMAIL" "e2e-drill-$RUN" "e2e launch drill $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
sleep 12
call GET "/v1/account/balances?account_type=SPOT" "" "${AUTH[@]}"
expect 200 - "the new account's balances"
check '[.balances[] | select((.available | tonumber) > 0)] | length == 0' "a fresh account gets nothing"

echo "== go live: the name, the mark, the favicon, the domain, the banner off, sign-ups open"
NAME="Drill Exchange ${RUN: -4}"
acall GET /admin/v1/platform/profile ""
acall PUT /admin/v1/platform/profile "$(jq -c --arg n "$NAME" '{name: $n, short_name: "Drill", domain: "astras.vip", theme_color, brand_color, footer,
  contact, social, default_locale, learning_mode: (.learning_mode | .enabled = false), registration: (.registration | .status = "OPEN"),
  expected_version: .version, reason: "launch drill: the platform as it goes live"}' <<<"$BODY")"
expect 200 - "renamed to $NAME, domain astras.vip, banner off, sign-ups open"
MARK='<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#0f766e"/><path d="M6 12h12M12 6v12" stroke="#fff" stroke-width="3"/></svg>'
# An image the platform has already stays: the drill uploads only what is
# missing and removes only what it uploaded (review BF).
for kind in logo_dark favicon; do
  if [[ $(jq -r ".images.$kind // \"\"" <<<"$ORIG") != "" ]]; then
    echo "ok   $kind: the platform's own already"
    continue
  fi
  acall PUT "/admin/v1/platform/images/$kind" "$(jq -nc --arg d "$(printf '%s' "$MARK" | base64 | tr -d '\n')" \
    '{data: $d, mime: "image/svg+xml", reason: "launch drill: our own image"}')"
  expect 200 - "$kind uploaded"
  UPLOADED+=("$kind")
done

echo "== go live: the legal pages and the home hero, published from the defaults"
publish_default() { # publish_default SECTION DIR SLUG
  local section=$1 dir=$2 slug=$3 zh en a
  acall GET "/admin/v1/articles?section=$section" ""
  if [[ $(jq -r --arg s "$slug" '[.articles[] | select(.slug == $s)] | length' <<<"$BODY") != 0 ]]; then
    printf 'ok   %s/%s: the console has its own already (%s)\n' "$section" "$slug" \
      "$(jq -r --arg s "$slug" '.articles[] | select(.slug == $s) | .status' <<<"$BODY")"
    return 0
  fi
  zh=$(md_text "$CONTENT/$dir/$slug.zh-CN.md" zh-CN)
  en=$(md_text "$CONTENT/$dir/$slug.en.md" en)
  acall POST /admin/v1/articles "$(jq -nc --arg sec "$section" --arg slug "$slug" --argjson zh "$zh" --argjson en "$en" \
    '{section: $sec, slug: $slug, category: "", pinned: false, order: 0, texts: [$zh, $en], reason: "launch drill: publish the default"}')"
  expect 201 - "$section/$slug from its default"
  a=$BODY
  CREATED+=("$(jq -r .id <<<"$a")")
  [[ $slug != terms ]] || TERMS_OURS=1
  acall POST "/admin/v1/articles/$(jq -r .id <<<"$a")/publish" "$(jq -c '{version, reason: "launch drill: publish the default"}' <<<"$a")"
  expect 200 - "published"
}
for slug in terms privacy risk fees about contact; do
  publish_default LEGAL legal "$slug"
done
publish_default HOME home home-hero

echo "== go live: the platform coin's profile"
acall GET /admin/v1/assets/ASTRA/profile ""
expect 200 - "ASTRA's profile"
if [[ $(jq -r '(.display_name // "") != "" and (.logo_url // "") != ""' <<<"$BODY") == true ]]; then
  echo "ok   ASTRA is $(jq -r .display_name <<<"$BODY"), with its logo"
else
  echo "FAIL ASTRA has no display name or logo: set them in the console (scripts/ops/astra.sh profile)" >&2
  exit 1
fi

echo "== the launch checklist"
acall GET /admin/v1/launch-checklist ""
expect 200 - "the checklist"
CHECKLIST=$BODY
failed=0
while IFS=$'\t' read -r key status value; do
  if [[ " $CONSOLE_ITEMS " == *" $key "* ]]; then
    if [[ $status == OK ]]; then
      printf 'ok   %-16s OK   %s\n' "$key" "$value"
    else
      printf 'FAIL %-16s %s %s\n' "$key" "$status" "$value" >&2
      failed=1
    fi
  else
    printf 'note %-16s %-4s %s (the deployment or the test server)\n' "$key" "$status" "$value"
  fi
done < <(jq -r '.items[] | [.key, .status, (.value | tojson)] | @tsv' <<<"$CHECKLIST")
[[ $failed == 0 ]] || { echo "FAIL the console's items are not all OK" >&2; exit 1; }

echo "== the sites, within a minute"
# The copy promising credits goes once instrument-service has read the
# ledger's (every minute).
no_credits() { call GET /v1/platform/profile "" && [[ $(jq -c .welcome_credits <<<"$BODY") == "[]" ]]; }
eventually 150 "the profile promises no credits" no_credits
call GET /v1/legal/terms ""
expect 200 - "the terms are the console's"
# The bundled draft's title, when the drill published it (an operator's may differ).
[[ -z ${TERMS_OURS:-} ]] || check '.title == "用户协议"' "the terms' title"
for site in pc m; do
  SITE=$site BRAND=$NAME FAVICON=1 LEARNING=0 BANNER="$(jq -r '.learning_mode.text["zh-CN"]' <<<"$ORIG")" LAUNCH=1 CAPTCHA_BYPASS_TOKEN="$BYPASS" \
    node "$(dirname "$0")/../../web/e2e/branding.mjs"
done

echo "== back to the learning setup"
back_to_learning
learning_again() {
  call GET /v1/platform/profile "" &&
    [[ $(jq -r .name <<<"$BODY") == "$(jq -r .name <<<"$ORIG")" && $(jq -r .learning_mode.enabled <<<"$BODY") == "$(jq -r .learning_mode.enabled <<<"$ORIG")" ]]
}
eventually 20 "the name and the banner are back ($(jq -r .name <<<"$ORIG"), learning mode $(jq -r .learning_mode.enabled <<<"$ORIG"))" learning_again
internal GET ledger-service 8085 /internal/ledger/settings/welcome-credits
[[ $(jq -c .credits <<<"$BODY") == "$ORIG_CREDITS" ]] || { echo "FAIL the credits are $(jq -c .credits <<<"$BODY")" >&2; exit 1; }
echo "ok   the welcome credits are back: $ORIG_CREDITS"
if [[ -n ${TERMS_OURS:-} ]]; then
  bundled() { call GET /v1/legal/terms "" && [[ $STATUS == 404 ]]; }
  eventually 40 "the terms are the bundled draft again" bundled
fi
echo "launch drill passed in $((SECONDS - START)) s"
