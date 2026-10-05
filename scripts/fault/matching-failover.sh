#!/usr/bin/env bash
# Fault injection: the matching engine's primary crashes (implementation
# plan §6.3 task 12, requirements §5.7). A second instance starts as a
# standby waiting for the engine lease (a PostgreSQL advisory lock). An
# order rests on the primary; the primary's process is killed (SIGKILL: no
# graceful release) and a market order arrives while no engine runs.
# Docker restarts the crashed instance within a second and the standby
# asks for the lease every second, so either can take it (B63 saw the
# restarted one win): it rebuilds the books (the users' and HOUSE's
# reference books, ADR-0015) from the snapshot and the WAL and continues
# from the committed offsets: the order sent during the crash fills
# against HOUSE exactly once, the resting order is still there and cancels;
# the other instance waits. Then the running instance is stopped
# gracefully and the waiting one takes over (a second handover); the
# drill ends with the first instance alone, as before. One new user on
# ETH-BTC (every order trades against HOUSE); about three minutes.
set -euo pipefail
# One drill at a time on the server (scripts/ops/lock.sh); task fault holds the lock for all of them.
[[ -n ${OPS_LOCK_HELD:-} ]] || exec "$(dirname "$0")/../ops/lock.sh" run --owner "fault $(basename "$0")" -- bash "$0" "$@"

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
FIRST=exchange-infra-matching-engine-1
SECOND=exchange-infra-matching-engine-2
cleanup() {
  call DELETE /v1/orders "" "${AUTH[@]}" >/dev/null 2>&1 || true
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
pid=$(remote "sudo docker inspect -f '{{.State.Pid}}' $FIRST")
KILLED=$(server_now)
remote "sudo kill -9 $pid"
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a market buy of 0.0005 BTC is accepted"
MKT=$(jq -r .order_id <<<"$BODY")
ACTIVE="" WAITING=""
took_over() {
  if started_since "$KILLED" "$SECOND"; then
    ACTIVE=$SECOND WAITING=$FIRST
  elif started_since "$KILLED" "$FIRST"; then
    ACTIVE=$FIRST WAITING=$SECOND
  else
    return 1
  fi
}
eventually 120 "an instance takes the lease and starts" took_over
echo "ok   it is ${ACTIVE#exchange-infra-}"
restarted() { [[ $(remote "sudo docker inspect -f '{{.State.Running}} {{.RestartCount}}' $FIRST") == "true "[1-9]* ]]; }
eventually 120 "Docker restarted the crashed instance" restarted
# The other one waits: the restarted first logs that it waits; the second
# has waited since it started.
waits() {
  [[ $(remote "sudo docker inspect -f '{{.State.Running}}' $WAITING") == true ]] && ! started_since "$KILLED" "$WAITING" &&
    { [[ $WAITING == "$SECOND" ]] || remote "sudo docker logs --since $KILLED $FIRST 2>&1" | grep -q '"waiting for the engine lease"'; }
}
eventually 120 "the other instance waits as the standby" waits

echo "== after the takeover"
eventually 60 "the order sent during the crash is FILLED" status_is "$MKT" FILLED
Q=$(jq -r .filled_quantity <<<"$BODY")
call GET "/v1/orders/$MKT/fills" "" "${AUTH[@]}"
check "all(.fills[]; .role == \"TAKER\") and (([.fills[].quantity | tonumber] | add) - ($Q | tonumber) | fabs) < 1e-12" "filled once, against HOUSE: its fills add up to its $Q ETH"
call GET "/v1/orders/$REST" "" "${AUTH[@]}"
check '.status == "OPEN" and .filled_quantity == "0"' "the resting bid survived the takeover"
call DELETE "/v1/orders/$REST" "" "${AUTH[@]}"
expect 202 - "cancel it"
eventually 40 "it is CANCELED" status_is "$REST" CANCELED
settled() { [[ $(balance BTC | cut -d' ' -f2) == 0 ]]; }
eventually 40 "nothing is left frozen" settled

echo "== the running instance stops, the waiting one takes over"
HANDOVER=$(server_now)
remote "sudo docker stop -t 15 $ACTIVE >/dev/null"
handed_over() { started_since "$HANDOVER" "$WAITING"; }
eventually 120 "the waiting instance takes the lease" handed_over
if [[ $ACTIVE == "$FIRST" ]]; then
  # Back to the first instance alone: it starts again, waits, and takes
  # over when the second leaves (a third handover).
  remote "sudo docker start $FIRST >/dev/null"
  first_waits() { remote "sudo docker logs --since $HANDOVER $FIRST 2>&1" | grep -q '"waiting for the engine lease"'; }
  eventually 120 "the first instance starts again and waits" first_waits
  HANDOVER=$(server_now)
  remote "sudo docker stop -t 15 $SECOND >/dev/null"
  first_back() { started_since "$HANDOVER" "$FIRST"; }
  eventually 120 "the first instance takes the lease back" first_back
fi
remote "sudo docker rm $SECOND >/dev/null"
wait_healthy matching-engine 180
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a new market buy"
AFTER=$(jq -r .order_id <<<"$BODY")
eventually 40 "the engine fills it against HOUSE" status_is "$AFTER" FILLED
echo "matching engine failover survived"
