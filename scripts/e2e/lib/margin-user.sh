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
# left alone. Only the users dimension changes: the switch's other rules
# (regions, statuses, ...) still apply, so a switch on for everyone within
# such rules is on for the allowed users alone until "back". Services see a
# change within 5 seconds (flags.RefreshInterval).
#
# exchangectl runs in a service container over ssh ($REMOTE when web.sh
# shares its connection), tried again when ssh itself fails (status 255). A
# switch that cannot be read is left as it is and nothing is recorded; one
# that cannot be put back is reported with the command that does it by hand.
set -euo pipefail

state=${MARGIN_USER_STATE:?MARGIN_USER_STATE names the file that keeps the switch as it was}
key=margin.enabled
infra=/opt/exchange/infra
exchangectl="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T ledger-service /app/exchangectl"

# ctl ARGS... runs exchangectl on the test server.
ctl() {
  local cmd rc=255 attempt
  cmd="cd $infra && $exchangectl $(printf '%q ' "$@")"
  for attempt in 1 2 3; do
    # shellcheck disable=SC2086 # REMOTE is a command line
    if ${REMOTE:-ssh -o ConnectTimeout=20 exchange} "$cmd"; then
      return 0
    else
      rc=$?
    fi
    [[ $rc -ne 255 ]] && return "$rc"
    echo "note: ssh failed (attempt $attempt)" >&2
    sleep 3
  done
  return "$rc"
}

# show prints the switch as JSON; one never set is off without rules. Any
# other failure (the connection, the container, the answer) fails.
show() {
  local out err rc=0
  err=$(mktemp)
  out=$(ctl flags show "$key" 2>"$err") || rc=$?
  if [[ $rc -eq 0 ]] && jq -e 'has("enabled")' <<<"$out" >/dev/null 2>&1; then
    rm -f "$err"
    printf '%s\n' "$out"
    return 0
  fi
  if [[ $rc -ne 0 ]] && grep -qF "flag $key is not set" "$err"; then
    rm -f "$err"
    echo '{"enabled":false,"rules":{}}'
    return 0
  fi
  echo "could not read $key (status $rc): $(tail -3 "$err")" >&2
  rm -f "$err"
  return 1
}

case ${1:-} in
on)
  user=${2:?margin-user.sh on USER_ID}
  current=$(show) || { echo "FAIL $key left as it is: it could not be read" >&2; exit 1; }
  if jq -e '.enabled and (.rules == {} or .rules == null)' <<<"$current" >/dev/null; then
    echo "note: $key is on for everyone"
    exit 0
  fi
  users=$(jq -r '(.rules.users.allow // []) | join(",")' <<<"$current")
  [[ -s $state ]] || printf '%s %s\n' "$(jq -r .enabled <<<"$current")" "$users" >"$state"
  ctl flags set "$key" --on --allow-users "${users:+$users,}$user" --reason "e2e web.sh: margin trading for its smoke user $user" >/dev/null ||
    { echo "FAIL could not open $key for $user" >&2; exit 1; }
  echo "note: $key on for $user until web.sh ends"
  ;;
back)
  [[ -s $state ]] || exit 0
  read -r enabled users <"$state"
  onoff=--off
  [[ $enabled == true ]] && onoff=--on
  if ! ctl flags set "$key" "$onoff" --allow-users "${users:-}" --reason "e2e web.sh: back as it was" >/dev/null; then
    {
      echo "FAIL could not put $key back (enabled $enabled, allowed users '${users:-}'); put it back by hand:"
      echo "  ssh exchange 'cd $infra && $exchangectl flags set $key $onoff --allow-users \"${users:-}\" --reason \"back after e2e web.sh\"'"
    } >&2
    exit 1
  fi
  rm -f "$state"
  ;;
*)
  echo "usage: margin-user.sh on USER_ID | back" >&2
  exit 2
  ;;
esac
