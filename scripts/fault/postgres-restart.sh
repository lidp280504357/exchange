#!/usr/bin/env bash
# Fault injection: PostgreSQL restarts. Connection pools reconnect on their
# own: a few requests may fail during the restart, then reads and writes
# work again without restarting any service, and the ledger still
# reconciles. Disrupts the test environment for a few seconds.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'compose "start postgres" >/dev/null 2>&1 || true; cleanup_remote' EXIT
EMAIL="e2e-fault-pg-$RUN@example.com"
DEVICE="e2e-fault-pg-$RUN"

register "$EMAIL" "$DEVICE" "e2e fault $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")

echo "== restart postgres"
compose "restart postgres" >/dev/null
wait_healthy postgres
reads() { call GET /v1/market/pairs ""; [[ $STATUS == 200 ]]; }
eventually 60 "reads work again" reads
transfer() {
  call POST /v1/account/transfers '{"asset":"USDT","amount":"1","from_account_type":"SPOT","to_account_type":"FUTURES"}' "${AUTH[@]}" -H "Idempotency-Key: fault-pg-$RUN"
  [[ $STATUS == 201 ]]
}
eventually 60 "writes work again (the same Idempotency-Key, so at most one transfer)" transfer
call GET /v1/account/transfers "" "${AUTH[@]}"
check '(.items | length) == 1' "exactly one transfer recorded"
exchangectl ledger reconcile >/dev/null && echo "ok   the ledger reconciles after the restart"
for svc in auth-service user-service ledger-service instrument-service notification-service; do
  wait_healthy "$svc" 60
done
echo "postgres restart survived"
