#!/usr/bin/env bash
# Fault injection: the chain node goes silent (implementation plan §6.3
# task 12). wallet-service's traffic to the internet (the Alchemy Sepolia
# endpoint) is dropped at the host's firewall. The deposit scanner stops
# advancing (its RPC calls time out after 20 seconds) but wallet-service
# stays ready: deposit addresses and the withdrawal pages keep working.
# When the node is back the scanner catches up. About four minutes; the
# block is always lifted.
set -euo pipefail

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'unblock_egress wallet-service >/dev/null 2>&1 || true; cleanup_remote' EXIT

last_scan() { metric wallet-service 9092 wallet_scan_last_success_timestamp_seconds | awk '{printf "%d", $1}'; }
lag() { metric wallet-service 9092 wallet_scan_lag_blocks | awk '{printf "%d", $1}'; }

before=$(last_scan)
[[ -n $before && $before -gt 0 ]] || { echo "FAIL the scanner has not completed a round (is WALLET_XPUB set?)" >&2; exit 1; }
echo "ok   the scanner's last round: $(($(date +%s) - before)) s ago, $(lag) blocks behind"

EMAIL="e2e-fault-chain-$RUN@example.com"
register "$EMAIL" "e2e-fault-chain-$RUN" "e2e fault $RUN"
AUTH=(-H "Authorization: Bearer $(jq -r .access_token <<<"$BODY")")

echo "== the node goes silent"
block_egress wallet-service
blocked_at=$(date +%s)
stalled() { (($(last_scan) < blocked_at)) && (($(date +%s) - blocked_at > 70)); }
eventually 240 "no scan round completes (two intervals and an RPC timeout later)" stalled
call GET "/v1/wallet/deposit-address?asset=ETH&network=ETH-SEPOLIA" "" "${AUTH[@]}"
expect 200 - "a new deposit address is still assigned (no node needed)"
call GET /v1/wallet/deposits "" "${AUTH[@]}"
expect 200 - "deposits are listed"
call GET /v1/wallet/withdrawals "" "${AUTH[@]}"
expect 200 - "withdrawals are listed"
ready=$(compose "exec -T wallet-service wget -qO- http://127.0.0.1:9092/readyz" >/dev/null 2>&1 && echo yes || echo no)
[[ $ready == yes ]] || { echo "FAIL wallet-service is not ready while the node is gone" >&2; exit 1; }
echo "ok   wallet-service stays ready"

echo "== the node is back"
unblock_egress wallet-service
back_at=$(date +%s)
scanning() { (($(last_scan) >= back_at)); }
eventually 240 "a scan round completes again" scanning
caught_up() { (($(lag) < 30)); }
eventually 240 "the scanner catches up (< 30 blocks behind)" caught_up
echo "chain outage survived"
