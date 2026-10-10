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
  [[ $a == --dry-run ]] && dry=1
done
if [[ -z ${OPS_LOCK_HELD:-} && -z $dry ]]; then
  exec "$(dirname "$0")/lock.sh" run --owner "ops retention.sh" -- bash "$0" "$@"
fi
printf -v args ' %q' "$@"
# A one-off container of the services' image and settings (as the server's
# daily run), not one inside user-service.
ssh exchange "cd $INFRA && $COMPOSE run --rm --no-deps -T -e EXCHANGECTL_ACTOR=${EXCHANGECTL_ACTOR:-ops-retention} user-service /app/exchangectl retention run$args" </dev/null
