#!/usr/bin/env bash
# Fault injection: the matching engine's primary crashes (implementation
# plan §6.3 task 12, requirements §5.7). A second instance starts as a
# standby waiting for the engine lease (a PostgreSQL advisory lock). The
# primary's process is killed (SIGKILL: no graceful release); the standby
# takes the lease, rebuilds the books from the snapshot and the WAL and
# continues from the committed offsets, so an order resting before the
# crash fills afterwards, exactly once. Docker restarts the crashed
# instance, which now waits as the standby. At the end the second instance
# is stopped gracefully and the first takes over again (a second
# handover), leaving one instance as before. Trades on ETH-BTC with two new
# users; about three minutes.
set -euo pipefail

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
FIRST=exchange-infra-matching-engine-1
SECOND=exchange-infra-matching-engine-2
cleanup() {
  call DELETE /v1/orders "" "${MAKER[@]}" >/dev/null 2>&1 || true
  call DELETE /v1/orders "" "${TAKER[@]}" >/dev/null 2>&1 || true
  remote "sudo docker start $FIRST >/dev/null 2>&1; sudo docker stop -t 15 $SECOND >/dev/null 2>&1; sudo docker rm $SECOND >/dev/null 2>&1" || true
  cleanup_remote
}
MAKER=() TAKER=()
trap cleanup EXIT

call GET /v1/market/pairs/ETH-BTC ""
check '.status == "TRADING"' "ETH-BTC accepts orders"

signup() { # signup NAME: registers a user, sets NAME_AUTH
  register "e2e-failover-$1-$RUN@example.com" "e2e-failover-$1-$RUN" "e2e failover $RUN"
  eval "${1}_AUTH=(-H \"Authorization: Bearer $(jq -r .access_token <<<"$BODY")\")"
}
signup maker
signup taker
# shellcheck disable=SC2154 # set by signup
MAKER=("${maker_AUTH[@]}")
TAKER=("${taker_AUTH[@]}")
balance() { # balance ASSET AUTH... prints "available frozen"
  local asset=$1
  shift
  call GET /v1/account/balances "" "$@"
  jq -r --arg a "$asset" '.balances[] | select(.asset == $a and .account_type == "SPOT") | "\(.available) \(.frozen)"' <<<"$BODY"
}
funded() { [[ $(balance BTC "${MAKER[@]}") == "0.1 0" && $(balance ETH "${TAKER[@]}") == "2 0" ]]; }
eventually 40 "welcome funds arrived for both" funded
status_is() { # status_is ORDER STATUS AUTH...
  local id=$1 want=$2
  shift 2
  call GET "/v1/orders/$id" "" "$@"
  [[ $(jq -r .status <<<"$BODY") == "$want" ]]
}
# A price between the book's best bid and best ask, so the maker's buy is
# the best bid and the taker's sell meets it first.
call GET "/v1/market/ETH-BTC/depth?limit=1" ""
PRICE=$(jq -r '[(.bids[0][0] // "0.01" | tonumber) + 0.00001, 0.02] | max | . * 100000 | round / 100000 | tostring' <<<"$BODY")
ask=$(jq -r '.asks[0][0] // empty' <<<"$BODY")
if [[ -n $ask ]] && awk -v p="$PRICE" -v a="$ask" 'BEGIN { exit !(p >= a) }'; then
  echo "FAIL the ETH-BTC book has no room between its best bid and best ask ($ask)" >&2
  exit 1
fi

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
call POST /v1/orders "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"0.1\"}" "${MAKER[@]}"
expect 202 - "the maker bids 0.1 ETH at $PRICE"
BUY=$(jq -r .order_id <<<"$BODY")
eventually 40 "it rests (OPEN)" status_is "$BUY" OPEN "${MAKER[@]}"

echo "== the primary crashes"
pid=$(remote "sudo docker inspect -f '{{.State.Pid}}' $FIRST")
remote "sudo kill -9 $pid"
took_over() { remote "sudo docker logs --since 5m $SECOND 2>&1" | grep -q '"service started"'; }
eventually 120 "the standby takes the lease and starts" took_over
restarted_as_standby() {
  [[ $(remote "sudo docker inspect -f '{{.State.Running}} {{.RestartCount}}' $FIRST") == "true "[1-9]* ]] &&
    remote "sudo docker logs --since 1m $FIRST 2>&1" | grep -q '"waiting for the engine lease"'
}
eventually 120 "Docker restarts the crashed instance, which waits as the standby" restarted_as_standby

echo "== the resting order fills after the takeover"
call POST /v1/orders "{\"symbol\":\"ETH-BTC\",\"side\":\"SELL\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"0.1\"}" "${TAKER[@]}"
expect 202 - "the taker sells 0.1 ETH at $PRICE"
SELL=$(jq -r .order_id <<<"$BODY")
eventually 60 "the maker's order is FILLED" status_is "$BUY" FILLED "${MAKER[@]}"
eventually 60 "the taker's order is FILLED" status_is "$SELL" FILLED "${TAKER[@]}"
call GET "/v1/orders/$BUY/fills" "" "${MAKER[@]}"
check '(.fills | length == 1) and .fills[0].role == "MAKER"' "exactly one fill, as the maker"
settled() { [[ $(balance BTC "${MAKER[@]}" | cut -d' ' -f2) == 0 && $(balance ETH "${TAKER[@]}" | cut -d' ' -f2) == 0 ]]; }
eventually 40 "the ledger settled it (nothing left frozen)" settled

echo "== the second instance stops, the first takes over again"
remote "sudo docker stop -t 15 $SECOND >/dev/null && sudo docker rm $SECOND >/dev/null"
first_started() { remote "sudo docker logs --since 1m $FIRST 2>&1" | grep -q '"service started"'; }
eventually 120 "the first instance takes the lease" first_started
wait_healthy matching-engine 180
call POST /v1/orders "{\"symbol\":\"ETH-BTC\",\"side\":\"BUY\",\"type\":\"LIMIT\",\"price\":\"$PRICE\",\"quantity\":\"0.01\"}" "${MAKER[@]}"
expect 202 - "a new order"
AFTER=$(jq -r .order_id <<<"$BODY")
eventually 40 "the engine takes it (OPEN)" status_is "$AFTER" OPEN "${MAKER[@]}"
call DELETE "/v1/orders/$AFTER" "" "${MAKER[@]}"
eventually 40 "and cancels it" status_is "$AFTER" CANCELED "${MAKER[@]}"
echo "matching engine failover survived"
