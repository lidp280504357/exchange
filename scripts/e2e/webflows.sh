#!/usr/bin/env bash
# The checklist flows (docs/runbook/ui-checklist.md, docs/阶段4验收报告.md
# §8) in headless Chrome: what the user would tick by hand on the PC site
# (web/e2e/pc-flows.mjs), the mobile site (web/e2e/m-flows.mjs) and the
# admin console (web/e2e/admin-flows.mjs, with a throwaway ADMIN and a
# throwaway AUDITOR made over ssh, disabled when the script ends). The
# name sorts after web.sh, so task e2e runs the flows after the smokes.
# A failed step leaves screenshots and a log under FLOWS_OUT (printed);
# every site runs even when an earlier one failed. Skipped without Chrome
# (before any administrator is made).
#
#   scripts/e2e/webflows.sh [pc] [m] [admin]
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
M_BASE="${M_BASE:-https://m.astras.vip}"
ADMIN_BASE="${ADMIN_BASE:-https://admin.astras.vip}"
FLOWS_OUT="${FLOWS_OUT:-$HOME/.cache/exchange-e2e/flows/$RUN}"
export FLOWS_OUT CAPTCHA_BYPASS_TOKEN="$BYPASS"
FLOWS="$(dirname "$0")/../../web/e2e"
sites=("$@")
[[ ${#sites[@]} -gt 0 ]] || sites=(pc m admin)

# Without Chrome nothing runs (lib.mjs looks in the same places): say so
# before making the console's throwaway administrators for nothing.
chrome=${CHROME:-}
if [[ -z $chrome ]]; then
  for c in "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" /usr/bin/google-chrome /usr/bin/chromium /usr/bin/chromium-browser; do
    if [[ -x $c ]]; then
      chrome=$c
      break
    fi
  done
fi
if [[ -z $chrome ]]; then
  echo "SKIP the checklist flows: no Chrome found (set CHROME)"
  exit 0
fi

# make_admin ROLE creates a throwaway administrator (random password and
# authenticator secret on stdin, never printed), disabled when the script
# ends; sets MADE_EMAIL and MADE_PASSWORD.
make_admin() {
  local role=$1 secret out
  MADE_EMAIL="e2e-flows-$(tr '[:upper:]' '[:lower:]' <<<"$role")-$RUN@example.com"
  MADE_PASSWORD=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24 || true)
  secret=$(LC_ALL=C tr -dc 'A-Z2-7' </dev/urandom | head -c 32 || true)
  out=$(remote "sudo docker compose $COMPOSE_FILES exec -T admin-service /app/exchangectl admin create --email $MADE_EMAIL --name 'e2e flows' --role $role --secrets-stdin" \
    "$(printf '%s\n%s\n' "$MADE_PASSWORD" "$secret")")
  grep -q "^created .* $MADE_EMAIL ($role)" <<<"$out" || {
    echo "FAIL admin create $role: $out" >&2
    exit 1
  }
  at_exit "remote \"sudo docker compose \$COMPOSE_FILES exec -T admin-service /app/exchangectl admin disable $MADE_EMAIL --reason 'e2e flows over'\" >/dev/null"
}

failed=()
for site in "${sites[@]}"; do
  case $site in
    pc)
      echo "== PC site's checklist ($BASE)"
      APP="$BASE" node "$FLOWS/pc-flows.mjs" || failed+=(pc)
      ;;
    m)
      echo "== mobile site's checklist ($M_BASE)"
      APP="$M_BASE" node "$FLOWS/m-flows.mjs" || failed+=(m)
      ;;
    admin)
      echo "== admin console's checklist ($ADMIN_BASE)"
      # The browser signs in with the password alone (admin.login_without_totp
      # on the test server).
      # shellcheck source=lib/remote.sh
      source "$(dirname "$0")/lib/remote.sh"
      make_admin ADMIN
      ADMIN_EMAIL=$MADE_EMAIL ADMIN_PASSWORD=$MADE_PASSWORD
      make_admin AUDITOR
      AUDITOR_EMAIL=$MADE_EMAIL AUDITOR_PASSWORD=$MADE_PASSWORD
      ADMIN_EMAIL="$ADMIN_EMAIL" ADMIN_PASSWORD="$ADMIN_PASSWORD" AUDITOR_EMAIL="$AUDITOR_EMAIL" AUDITOR_PASSWORD="$AUDITOR_PASSWORD" APP="$ADMIN_BASE" \
        node "$FLOWS/admin-flows.mjs" || failed+=(admin)
      ;;
    *)
      echo "unknown site $site (pc, m or admin)" >&2
      exit 2
      ;;
  esac
done
if [[ ${#failed[@]} -gt 0 ]]; then
  echo "FAIL the checklist flows of: ${failed[*]} (evidence under $FLOWS_OUT)" >&2
  exit 1
fi
echo "ok   the checklist flows of ${sites[*]} (summaries under $FLOWS_OUT)"
