#!/usr/bin/env bash
# Fault injection: the external reference feed goes silent (implementation
# plan §6.3 task 12, ADR-0015). market-data-service's traffic to the
# internet (Binance) is dropped at the host's firewall. Within seconds the
# reference price goes stale, BTC-USDT's reference book is no longer
# usable and HOUSE takes its liquidity away (an empty reference book to
# the engine); the public book falls back to the platform's own orders.
# The streams notice the silence and reconnect with backoff; tickers and
# candles keep being served. When Binance is reachable again the books
# load a snapshot, the public book is Binance's again and HOUSE offers it.
# Needs market.reference_feed, market.reference_depth and
# market.house_liquidity on for BTC-USDT; about four minutes; the block is
# always lifted.
set -euo pipefail

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'unblock_egress market-data-service >/dev/null 2>&1 || true; cleanup_remote' EXIT

age() { metric market-data-service 9090 market_reference_age_seconds 'symbol="BTC-USDT"' | awk '{printf "%d", $1}'; }
book_age() { metric market-data-service 9090 market_reference_book_age_seconds 'symbol="BTC-USDT"' | awk '{printf "%d", $1}'; }
offering() { [[ $(metric market-maker 9091 market_house_active 'symbol="BTC-USDT"') == 1 ]]; }
errors() { metric market-data-service 9090 market_reference_book_stream_failures_total | awk '{printf "%d", $1}'; }
book() { # book: "BIDS ASKS" levels of BTC-USDT
  call GET "/v1/market/BTC-USDT/depth?limit=5" ""
  jq -r '"\(.bids | length) \(.asks | length)"' <<<"$BODY"
}
quoted() { read -r bids asks <<<"$(book)" && ((bids > 0 && asks > 0)); }
empty() { [[ $(book) == "0 0" ]]; }

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
eventually 40 "the public book is the platform's own (empty)" empty
call GET /v1/market/tickers ""
expect 200 - "the platform's tickers are still served"
call GET "/v1/market/BTC-USDT/candles?interval=1m&limit=5" ""
expect 200 - "and its candles"
noticed() { (($(errors) > errors_before)); }
eventually 180 "the silent stream is noticed and retried" noticed

echo "== Binance is back"
unblock_egress market-data-service
eventually 360 "the reference is fresh again" fresh
eventually 60 "HOUSE offers BTC-USDT again" offering
eventually 40 "Binance's book is shown again" quoted
echo "reference feed outage survived"
