#!/usr/bin/env bash
# End-to-end check of the public market reference data (instrument-service)
# against a deployed environment; the test data comes from
# deploy/instruments/test.json, applied at every deploy.
#
#   scripts/e2e/market.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

echo "== assets"
call GET /v1/market/assets ""
expect 200 - "assets"
check '[.assets[].asset_code] | contains(["BTC","ETH","USDT"])' "seeded assets present"
check '[.assets[] | select(.asset_code == "ETH") | .networks[].network] | sort == ["ETH", "ETH-SEPOLIA"]' "ETH on Ethereum (custody) and Sepolia"
check '[.assets[] | select(.asset_code == "USDT") | .networks[].network] | sort == ["BSC", "ETH", "TRON"]' "USDT on BEP20, ERC20 and TRC20"
check '(.assets[] | select(.asset_code == "USDT") | .decimals) == 6' "USDT has 6 decimals"

echo "== pairs"
call GET /v1/market/pairs ""
expect 200 - "pairs"
check '[.pairs[].symbol] | contains(["BTC-USDT","ETH-USDT","ETH-BTC"])' "seeded pairs listed"
call GET /v1/market/pairs/btc-usdt ""
expect 200 - "one pair (symbol is case-insensitive)"
check '.tick_size == "0.01" and .lot_size == "0.0001" and .maker_fee_rate == "0.001" and (.min_notional | type) == "string"' "decimals are strings"
call GET /v1/market/pairs/NOPE-USDT ""
expect 404 COMMON_NOT_FOUND "unknown pair"
cache=$(curl -s -D - -o /dev/null "$BASE/v1/market/pairs" | tr -d '\r' | awk -F': ' 'tolower($1) == "cache-control" {print $2}')
[[ "$cache" == *"max-age=10"* ]] || { echo "FAIL cache-control: $cache" >&2; exit 1; }
echo "ok   reference data is cacheable"

echo "all market checks passed"
