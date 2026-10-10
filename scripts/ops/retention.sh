#!/usr/bin/env bash
# The test server's history retention (M1, ADR-0022): exchangectl
# retention run in a one-off container of the services, under the ops lock (a
# deploy or an end-to-end run must not see its tables thinned halfway);
# a dry run deletes nothing and takes no lock. The daily run is the
# server's cron (deploy/retention/retention.sh).
#
#   scripts/ops/retention.sh [--dry-run] [--days 15] [--key-days 90] [--only SCHEMA,...]
#       prints each table's rows and size deleted (or that would be with
#       --dry-run); see docs/runbook/retention.md.
set -euo pipefail

INFRA=/opt/exchange/infra
COMPOSE="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml"

dry=""
for a in "$@"; do
  # As exchangectl reads it (Go's flag package; B203).
  if [[ $a =~ ^--?dry-run(=(1|t|T|true|TRUE|True))?$ ]]; then dry=1; fi
done
if [[ -z ${OPS_LOCK_HELD:-} && -z $dry ]]; then
  exec "$(dirname "$0")/lock.sh" run --owner "ops retention.sh" -- bash "$0" "$@"
fi
printf -v args ' %q' "$@"
# A one-off container of the services' image and settings (as the server's
# daily run), not one inside user-service; named, so that a stop here
# (Ctrl-C) stops it there too - docker compose run alone lets it run on
# (B201) - and a dry run beside the daily run does not take its name.
name="exchange-retention-ops-$(date +%s)"
trap 'ssh exchange "sudo docker stop -t 60 $name >/dev/null 2>&1" </dev/null || true' INT TERM
ssh exchange "cd $INFRA && $COMPOSE run --rm --no-deps -T --name $name -e EXCHANGECTL_ACTOR=${EXCHANGECTL_ACTOR:-ops-retention} user-service /app/exchangectl retention run$args" </dev/null
