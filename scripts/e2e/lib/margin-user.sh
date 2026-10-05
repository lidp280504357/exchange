#!/usr/bin/env bash
# Margin trading for the browser smoke tests' own users (scripts/e2e/web.sh
# runs them under the ops lock), as scripts/e2e/margin.sh does for its user:
#
#   margin-user.sh on USER_ID   margin.enabled on for USER_ID too
#   margin-user.sh back         margin.enabled as it was before the first "on"
#
# The first "on" records the switch as it was in $MARGIN_USER_STATE; later
# ones add their users to the same allowed list; "back" restores the record
# and removes it (a second "back" does nothing). A switch on for everyone is
# left alone. Services see a change within 5 seconds (flags.RefreshInterval).
set -euo pipefail

state=${MARGIN_USER_STATE:?MARGIN_USER_STATE names the file that keeps the switch as it was}
key=margin.enabled

# ctl ARGS... runs exchangectl in a service container on the test server.
ctl() {
  ssh -o ConnectTimeout=20 exchange \
    "cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T ledger-service /app/exchangectl $(printf '%q ' "$@")"
}

case ${1:-} in
on)
  user=${2:?margin-user.sh on USER_ID}
  current=$(ctl flags show "$key" 2>/dev/null || echo '{"enabled":false,"rules":{}}')
  if jq -e '.enabled and (.rules == {} or .rules == null)' <<<"$current" >/dev/null; then
    echo "note: $key is on for everyone"
    exit 0
  fi
  users=$(jq -r '(.rules.users.allow // []) | join(",")' <<<"$current")
  [[ -s $state ]] || printf '%s %s\n' "$(jq -r .enabled <<<"$current")" "$users" >"$state"
  ctl flags set "$key" --on --allow-users "${users:+$users,}$user" --reason "e2e web.sh: margin trading for its smoke user $user" >/dev/null
  echo "note: $key on for $user until web.sh ends"
  ;;
back)
  [[ -s $state ]] || exit 0
  read -r enabled users <"$state"
  onoff=--off
  [[ $enabled == true ]] && onoff=--on
  ctl flags set "$key" "$onoff" --allow-users "${users:-}" --reason "e2e web.sh: back as it was" >/dev/null ||
    { echo "WARN could not put $key back (enabled $enabled, allowed users '${users:-}')" >&2; exit 1; }
  rm -f "$state"
  ;;
*)
  echo "usage: margin-user.sh on USER_ID | back" >&2
  exit 2
  ;;
esac
