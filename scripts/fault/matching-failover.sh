#!/usr/bin/env bash
# Fault injection: the matching engine's primary crashes (implementation
# plan §6.3 task 12, requirements §5.7). A second instance starts as a
# standby waiting for the engine lease (a PostgreSQL advisory lock). An
# order rests on the primary; the primary's process is killed (SIGKILL: no
# graceful release) and a market order arrives while no engine runs. The
# standby takes the lease, rebuilds the books (the users' and HOUSE's
# reference books, ADR-0015) from the snapshot and the WAL and continues
# from the committed offsets: the order sent during the crash fills
# against HOUSE exactly once, the resting order is still there and cancels.
# Docker restarts the crashed instance, which now waits as the standby. At
# the end the second instance is stopped gracefully and the first takes
# over again (a second handover), leaving one instance as before. One new
# user on ETH-BTC (every order trades against HOUSE); about three minutes.
set -euo pipefail

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
pid=$(remote "sudo docker inspect -f '{{.State.Pid}}' $FIRST")
remote "sudo kill -9 $pid"
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a market buy of 0.0005 BTC is accepted"
MKT=$(jq -r .order_id <<<"$BODY")
took_over() { remote "sudo docker logs --since 5m $SECOND 2>&1" | grep -q '"service started"'; }
eventually 120 "the standby takes the lease and starts" took_over
restarted_as_standby() {
  [[ $(remote "sudo docker inspect -f '{{.State.Running}} {{.RestartCount}}' $FIRST") == "true "[1-9]* ]] &&
    remote "sudo docker logs --since 1m $FIRST 2>&1" | grep -q '"waiting for the engine lease"'
}
eventually 120 "Docker restarts the crashed instance, which waits as the standby" restarted_as_standby

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

echo "== the second instance stops, the first takes over again"
remote "sudo docker stop -t 15 $SECOND >/dev/null && sudo docker rm $SECOND >/dev/null"
first_started() { remote "sudo docker logs --since 1m $FIRST 2>&1" | grep -q '"service started"'; }
eventually 120 "the first instance takes the lease" first_started
wait_healthy matching-engine 180
call POST /v1/orders '{"symbol":"ETH-BTC","side":"BUY","type":"MARKET","quote_amount":"0.0005"}' "${AUTH[@]}"
expect 202 - "a new market buy"
AFTER=$(jq -r .order_id <<<"$BODY")
eventually 40 "the engine fills it against HOUSE" status_is "$AFTER" FILLED
echo "matching engine failover survived"
