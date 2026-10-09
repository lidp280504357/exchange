#!/usr/bin/env bash
# The test server's test-account purge (L4, the user-kind design
# 2026-10-09 §1 #9): exchangectl users purge in the user-service container,
# under the ops lock (another session's run must not lose its accounts
# halfway), or users exempt for the accounts it must leave alone.
#
#   scripts/ops/purge.sh [--dry-run] [--older-than 24h] [--limit N] [--user ID,...] [--email-like P] [--reason TEXT]
#       clears the TEST accounts out: spot orders canceled, margin balances
#       without a debt back to spot, spot and futures balances to the
#       ADJUSTMENT account, the account closed and marked purged; contract
#       positions or orders, a margin debt, a withdrawal in flight and exempt
#       accounts are skipped and listed. Prints the counts before and after
#       and what was recovered. The reason defaults to "test accounts
#       cleared out (scripts/ops/purge.sh)".
#   scripts/ops/purge.sh exempt (--user ID[,ID...] | --email-like P) [--off] --reason TEXT
#       keeps accounts out of the purge (funding.sh's standing hedges), or
#       lets them in again.
#
# Needs ledger.manual_adjustment on (the sweep is a manual adjustment).
set -euo pipefail

INFRA=/opt/exchange/infra
COMPOSE="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml"

# ctl ARGS... runs exchangectl in the user-service container as this script.
ctl() {
  local args
  printf -v args ' %q' "$@"
  ssh exchange "cd $INFRA && $COMPOSE exec -T -e EXCHANGECTL_ACTOR=${EXCHANGECTL_ACTOR:-ops-purge} user-service /app/exchangectl$args" </dev/null
}

if [[ ${1:-} == exempt ]]; then
  shift
  ctl users exempt "$@"
  exit
fi

dry=""
for a in "$@"; do
  [[ $a == --dry-run ]] && dry=1
done
if [[ -z ${OPS_LOCK_HELD:-} && -z $dry ]]; then
  exec "$(dirname "$0")/lock.sh" run --owner "ops purge.sh" -- bash "$0" "$@"
fi
has_reason=""
for a in "$@"; do
  [[ $a == --reason ]] && has_reason=1
done
if [[ -n $has_reason ]]; then
  ctl users purge "$@"
else
  ctl users purge "$@" --reason "test accounts cleared out (scripts/ops/purge.sh)"
fi
