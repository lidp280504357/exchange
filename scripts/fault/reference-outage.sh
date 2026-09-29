#!/usr/bin/env bash
# Fault injection: the external reference feed goes silent (implementation
# plan §6.3 task 12, requirements §11.10). market-data-service's traffic to
# the internet (Binance) is dropped at the host's firewall. The reference
# price goes stale within seconds and the market maker pulls its BTC-USDT
# quotes; the stream notices the silence after 30 seconds and reconnects
# with backoff; the platform's own market data keeps being served. When
# Binance is reachable again the feed backfills, the reference is fresh and
# the maker quotes again. Needs market.reference_feed and market.maker on;
# about four minutes; the block is always lifted.
set -euo pipefail

# shellcheck source=../e2e/lib/common.sh
source "$(dirname "$0")/../e2e/lib/common.sh"
# shellcheck source=../e2e/lib/remote.sh
source "$(dirname "$0")/../e2e/lib/remote.sh"
trap 'unblock_egress market-data-service >/dev/null 2>&1 || true; cleanup_remote' EXIT

age() { metric market-data-service 9090 market_reference_age_seconds 'symbol="BTC-USDT"' | awk '{printf "%d", $1}'; }
quoting() { [[ $(metric market-maker 9091 mm_quoting 'symbol="BTC-USDT"') == 1 ]]; }
errors() { metric market-data-service 9090 market_reference_errors_total | awk '{printf "%d", $1}'; }
book() { # book: "BIDS ASKS" levels of BTC-USDT
  call GET "/v1/market/BTC-USDT/depth?limit=5" ""
  jq -r '"\(.bids | length) \(.asks | length)"' <<<"$BODY"
}
quoted() { read -r bids asks <<<"$(book)" && ((bids > 0 && asks > 0)); }
empty() { [[ $(book) == "0 0" ]]; }

fresh() { local a; a=$(age) && ((a >= 0 && a < 5)); }
eventually 60 "the BTC-USDT reference is fresh" fresh
eventually 60 "the market maker quotes BTC-USDT" quoting
eventually 20 "its quotes are on the book" quoted
errors_before=$(errors)

echo "== Binance goes silent"
block_egress market-data-service
stale() { (($(age) > 5)); }
eventually 60 "the reference goes stale" stale
not_quoting() { ! quoting; }
eventually 60 "the market maker pulls its quotes" not_quoting
eventually 40 "the BTC-USDT book is empty" empty
call GET /v1/market/tickers ""
expect 200 - "the platform's tickers are still served"
call GET "/v1/market/BTC-USDT/candles?interval=1m&limit=5" ""
expect 200 - "and its candles"
noticed() { (($(errors) > errors_before)); }
eventually 180 "the silent stream is noticed and retried" noticed

echo "== Binance is back"
unblock_egress market-data-service
eventually 360 "the reference is fresh again" fresh
eventually 60 "the market maker quotes again" quoting
eventually 40 "its quotes are back on the book" quoted
echo "reference feed outage survived"
