#!/usr/bin/env bash
# Fault injection: the external reference feed goes silent (implementation
# plan §6.3 task 12, ADR-0015). market-data-service's traffic to the
# internet (Binance) is dropped at the host's firewall. Within seconds the
# reference price goes stale, BTC-USDT's reference book is no longer
# usable and HOUSE takes its liquidity away (an empty reference book to
# the engine); the public book falls back to the platform's own orders.
# The streams notice the silence and reconnect with backoff; tickers and
# the candles of pairs without a reference market keep being served. When
# Binance is reachable again the books load a snapshot, the public book is
# Binance's again and HOUSE offers it.
# The contracts lose their mark prices too and go reduce-only; once the
# prices are back the drill lifts the reduce-only states it caused, as the
# operator would (contract-degrade.sh exercises that path itself).
# Needs market.reference_feed, market.reference_depth and
# market.house_liquidity on for BTC-USDT; about four minutes; the block is
# always lifted.
set -euo pipefail

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'unblock_egress market-data-service >/dev/null 2>&1 || true; cleanup_remote' EXIT
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)

age() { metric market-data-service 9090 market_reference_age_seconds 'symbol="BTC-USDT"' | awk '{printf "%d", $1}'; }
book_age() { metric market-data-service 9090 market_reference_book_age_seconds 'symbol="BTC-USDT"' | awk '{printf "%d", $1}'; }
offering() { [[ $(metric market-maker 9091 market_house_active 'symbol="BTC-USDT"') == 1 ]]; }
errors() { metric market-data-service 9090 market_reference_book_stream_failures_total | awk '{printf "%d", $1}'; }
book() { # book [LIMIT]: "BIDS ASKS" levels of BTC-USDT
  call GET "/v1/market/BTC-USDT/depth?limit=${1:-5}" ""
  jq -r '"\(.bids | length) \(.asks | length)"' <<<"$BODY"
}
quoted() { read -r bids asks <<<"$(book)" && ((bids > 0 && asks > 0)); }
# Binance's book has 50 levels a side; the platform's holds the users'
# resting orders, a few at most.
platform() { read -r bids asks <<<"$(book 50)" && ((bids + asks < 20)); }

fresh() { local a b; a=$(age) && b=$(book_age) && ((a >= 0 && a < 5 && b >= 0 && b < 5)); }
eventually 60 "the BTC-USDT reference price and book are fresh" fresh
eventually 60 "HOUSE offers BTC-USDT" offering
eventually 20 "Binance's book is shown" quoted
errors_before=$(errors)

echo "== Binance goes silent"
block_egress market-data-service
stale() { (($(age) > 5)); }
eventually 60 "the reference goes stale" stale
not_offering() { ! offering; }
eventually 60 "HOUSE takes its liquidity away" not_offering
eventually 40 "the public book falls back to the platform's own (users' orders only)" platform
call GET /v1/market/tickers ""
expect 200 - "the platform's tickers are still served"
# BTC-USDT's chart history is Binance's (market.reference_kline) and
# unavailable while it is silent; a pair without a reference market keeps
# its own.
call GET "/v1/market/ETH-BTC/candles?interval=1m&limit=5" ""
expect 200 - "and the platform's own candles (ETH-BTC)"
noticed() { (($(errors) > errors_before)); }
eventually 180 "the silent stream is noticed and retried" noticed

echo "== Binance is back"
unblock_egress market-data-service
eventually 360 "the reference is fresh again" fresh
eventually 60 "HOUSE offers BTC-USDT again" offering
eventually 40 "Binance's book is shown again" quoted
sleep 15 # the mark prices follow the reference
degraded=$(exchangectl derivatives states | awk -v since="$STARTED" 'NR > 1 && $2 == "true" && $4 >= since {print $1}')
for symbol in $degraded; do
  remote "sudo docker compose $COMPOSE_FILES exec -T -e EXCHANGECTL_ACTOR=fault-reference-outage derivatives-service /app/exchangectl derivatives resume $symbol" >/dev/null
  echo "ok   $symbol went reduce-only during the outage; lifted now that its mark price is back"
done
echo "reference feed outage survived"
