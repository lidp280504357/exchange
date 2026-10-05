#!/usr/bin/env bash
# Fault injection: the matching engine's primary crashes (implementation
# plan §6.3 task 12, requirements §5.7). A second instance starts as a
# standby waiting for the engine lease (a PostgreSQL advisory lock). An
# order rests on the primary; the primary's process is killed (SIGKILL: no
# graceful release) and a market order arrives while no engine runs. Its
# restart is held back (restart policy no) until the standby has taken
# over: Docker brings a killed container back within a second, and the
# restarted primary took the lease back before the standby's next try,
# which comes once a second (B63, review BU). The standby takes the lease,
# rebuilds the books (the users' and HOUSE's reference books, ADR-0015)
# from the snapshot and the WAL and continues from the committed offsets:
# the order sent during the crash fills against HOUSE exactly once, while
# the primary is still down, the resting order is still there and
# cancels, and a new order fills on the standby alone. The primary gets
# compose's restart policy back and starts again, waiting as the standby
# (it starting instead is a split brain). At the end the second instance
# is stopped gracefully and the first takes over again (a second
# handover), leaving one instance as before. One new user on ETH-BTC
# (every order trades against HOUSE); about three minutes.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
FIRST=exchange-infra-matching-engine-1
SECOND=exchange-infra-matching-engine-2
# The primary's restart policy, put back whatever happens.
POLICY=""
cleanup() {
  call DELETE /v1/orders "" "${AUTH[@]}" >/dev/null 2>&1 || true
  [[ -z $POLICY ]] || remote "sudo docker update --restart=$POLICY $FIRST >/dev/null 2>&1" || true
  remote "sudo docker start $FIRST >/dev/null 2>&1; sudo docker stop -t 15 $SECOND >/dev/null 2>&1; sudo docker rm $SECOND >/dev/null 2>&1" || true
  cleanup_remote
}
AUTH=()
trap cleanup EXIT

call GET /v1/market/pairs/ETH-BTC ""
check '.status == "TRADING"' "ETH-BTC accepts orders"

register "e2e-failover-$RUN@example.com" "e2e-failover-$RUN" "e2e failover $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")
balance() { # balance ASSET prints "available frozen"
  call GET /v1/account/balances "" "${AUTH[@]}"
  jq -r --arg a "$1" '.balances[] | select(.asset == $a and .account_type == "SPOT") | "\(.available) \(.frozen)"' <<<"$BODY"
}
funded() { [[ $(balance BTC) == "0.1 0" ]]; }
eventually 40 "welcome funds arrived" funded
status_is() { # status_is ORDER STATUS
  call GET "/v1/orders/$1" "" "${AUTH[@]}"
  [[ $(jq -r .status <<<"$BODY") == "$2" ]]
}
# A buy 3% under HOUSE's bid rests; HOUSE's ask would have to fall that far
# within the drill to fill it.
call GET "/v1/market/ETH-BTC/depth?limit=1" ""
LOW=$(jq -r '.bids[0][0] | tonumber * 0.97 * 100000 | floor / 100000 | tostring' <<<"$BODY")

echo "== a standby next to the primary"
compose "up -d --no-recreate --scale matching-engine=2 matching-engine" >/dev/null 2>&1
waiting() { remote "sudo docker logs --since 5m $SECOND 2>&1" | grep -q '"waiting for the engine lease"'; }
eventually 60 "the second instance waits for the lease" waiting
if remote "sudo docker logs --since 5m $SECOND 2>&1" | grep -q '"service started"'; then
  echo "FAIL the second instance started although the first holds the lease" >&2
  exit 1
fi
echo "ok   it does not start while the first holds the lease"

echo "== an order rests on the primary"
call POST /v1/orders "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$LOW\",\"quantity\":\"0.1\"}" "${AUTH[@]}"
expect 202 - "a bid for 0.1 ETH at $LOW"
REST=$(jq -r .order_id <<<"$BODY")
eventually 40 "it rests (OPEN)" status_is "$REST" OPEN

echo "== the primary crashes; an order arrives while no engine runs"
server_now() { remote "date -u +%Y-%m-%dT%H:%M:%S.%NZ"; }
# started_since T C: instance C logged its start after T (the server's clock).
started_since() { remote "sudo docker logs --since $1 $2 2>&1" | grep -q '"service started"'; }
down() { [[ $(remote "sudo docker inspect -f '{{.State.Running}}' $FIRST") == false ]]; }
# The policy compose gives every app container; the primary gets it back
# afterwards, whatever it had (a drill cut short leaves "no", and compose
# does not mend a container's restart policy; review BV).
POLICY=unless-stopped
had=$(remote "sudo docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' $FIRST")
[[ $had == "$POLICY" ]] || echo "note the primary's restart policy was '$had' (an earlier drill cut short?); it is $POLICY again at the end"
remote "sudo docker update --restart=no $FIRST >/dev/null"
pid=$(remote "sudo docker inspect -f '{{.State.Pid}}' $FIRST")
KILLED=$(server_now)
remote "sudo kill -9 $pid"
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a market buy of 0.0005 BTC is accepted"
MKT=$(jq -r .order_id <<<"$BODY")
took_over() { started_since "$KILLED" "$SECOND"; }
eventually 120 "the standby takes the lease and starts" took_over
if started_since "$KILLED" "$FIRST"; then
  echo "FAIL both instances started" >&2
  exit 1
fi
down || { echo "FAIL the crashed primary is running again" >&2; exit 1; }
echo "ok   the crashed primary stays down (its restart held back)"

echo "== after the takeover"
eventually 60 "the order sent during the crash is FILLED" status_is "$MKT" FILLED
Q=$(jq -r .filled_quantity <<<"$BODY")
down || { echo "FAIL the crashed primary is running again" >&2; exit 1; }
echo "ok   by the standby: the primary is still down"
call GET "/v1/orders/$MKT/fills" "" "${AUTH[@]}"
check "all(.fills[]; .role == \"TAKER\") and (([.fills[].quantity | tonumber] | add) - ($Q | tonumber) | fabs) < 1e-12" "filled once, against HOUSE: its fills add up to its $Q ETH"
call GET "/v1/orders/$REST" "" "${AUTH[@]}"
check '.status == "OPEN" and .filled_quantity == "0"' "the resting bid survived the takeover"
call DELETE "/v1/orders/$REST" "" "${AUTH[@]}"
expect 202 - "cancel it"
eventually 40 "it is CANCELED" status_is "$REST" CANCELED
settled() { [[ $(balance BTC | cut -d' ' -f2) == 0 ]]; }
eventually 40 "nothing is left frozen" settled
# A new order on the standby alone.
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a new market buy while the primary is down"
ON_STANDBY=$(jq -r .order_id <<<"$BODY")
eventually 40 "the standby fills it against HOUSE" status_is "$ON_STANDBY" FILLED
down || { echo "FAIL the crashed primary is running again" >&2; exit 1; }

echo "== the crashed instance comes back as the standby"
BACK=$(server_now)
remote "sudo docker update --restart=$POLICY $FIRST >/dev/null && sudo docker start $FIRST >/dev/null"
[[ $(remote "sudo docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' $FIRST") == "$POLICY" ]] ||
  { echo "FAIL the primary's restart policy is not $POLICY again" >&2; exit 1; }
SPLIT=""
first_waits() {
  # Started while the second holds the lease: two engines (stop waiting, fail below).
  if started_since "$BACK" "$FIRST"; then
    SPLIT=1
    return 0
  fi
  remote "sudo docker logs --since $BACK $FIRST 2>&1" | grep -q '"waiting for the engine lease"'
}
eventually 120 "it starts again with its restart policy ($POLICY) and waits for the lease" first_waits
[[ -z $SPLIT ]] || { echo "FAIL split-brain: the first instance started while the second holds the lease" >&2; exit 1; }

echo "== the second instance stops, the first takes over again"
HANDOVER=$(server_now)
remote "sudo docker stop -t 15 $SECOND >/dev/null && sudo docker rm $SECOND >/dev/null"
first_started() { started_since "$HANDOVER" "$FIRST"; }
eventually 120 "the first instance takes the lease" first_started
wait_healthy matching-engine 180
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a new market buy"
AFTER=$(jq -r .order_id <<<"$BODY")
eventually 40 "the engine fills it against HOUSE" status_is "$AFTER" FILLED
echo "matching engine failover survived"
