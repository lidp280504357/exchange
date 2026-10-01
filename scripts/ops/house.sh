#!/usr/bin/env bash
# HOUSE's virtual liquidity on the test server (ADR-0013, ADR-0015,
# docs/runbook/market-maker.md):
#
#   scripts/ops/house.sh seed   HOUSE's funds: spot inventory of 5,000,000
#                               USDT, 11.74 BTC and 370.4 ETH (MARKET_MAKER;
#                               about half the pair cap each, so HOUSE can
#                               sell and buy about 1,000,000 USDT of them),
#                               and 2,000,000 USDT of contract margin in its
#                               FUTURES account (HOUSE_USER_ID); audited
#                               adjustments, needs ledger.manual_adjustment.
#                               Idempotent.
#   scripts/ops/house.sh flags  public books and charts from Binance everywhere
#                               (market.reference_depth, market.reference_kline)
#                               and HOUSE on every pair and contract of
#                               deploy/instruments/test.json
#                               (market.house_liquidity): every order trades
#                               against HOUSE (user decision 2026-10-02;
#                               market.internal_matching stays off).
#   scripts/ops/house.sh open   the USDT pairs of deploy/instruments/test.json
#                               that follow Binance and are still PREPARE
#                               move to TRADING.
#   scripts/ops/house.sh show   HOUSE's MARKET_MAKER balances.
#
# Internal assets need no inventory: HOUSE may sell them short (ADR-0013).
set -euo pipefail

INFRA=/opt/exchange/infra
DATA="$(dirname "$0")/../../deploy/instruments/test.json"
COMPOSE="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml"

# ctl SERVICE ARGS... runs exchangectl in a service's container.
ctl() {
  local service=$1 args
  shift
  printf -v args ' %q' "$@"
  ssh exchange "cd $INFRA && $COMPOSE exec -T $service /app/exchangectl$args"
}

case "${1:-}" in
seed)
  # Inventory counts as HOUSE's position: an asset held past HOUSE_SYMBOL_CAP
  # stops HOUSE buying it on every pair. Under the design's 100,000 USDT cap
  # a v2 top-up of 1 BTC and 30 ETH did exactly that and was undone (keys
  # *-v2-undo); v3 came with the test server's 2,000,000 (2026-10-02).
  for spec in "USDT 500000 v1" "BTC 0.24 v1" "ETH 7.4 v1" "USDT 4500000 v3" "BTC 11.5 v3" "ETH 363 v3"; do
    read -r asset amount version <<<"$spec"
    ctl ledger-service ledger adjust --house --asset "$asset" --amount "$amount" \
      --reason "HOUSE inventory (ADR-0013)" --key "seed-house-$asset-$version"
  done
  for spec in "100000 v1" "1900000 v2"; do
    read -r amount version <<<"$spec"
    ctl ledger-service ledger house-margin --amount "$amount" \
      --reason "HOUSE contract margin: every contract trades against HOUSE (ADR-0015)" --key "seed-house-margin-$version"
  done
  ;;
flags)
  # Pairs with their own market (the platform coin) have no HOUSE.
  allow="$(jq -r '[(.pairs[] | select(.reference_symbol != null) | .symbol), (.contracts[] | .symbol)] | join(",")' "$DATA")"
  ctl user-service flags set market.reference_depth --on --reason "Binance books on every followed symbol (ADR-0010)"
  ctl user-service flags set market.reference_kline --on --deny-symbols "" --reason "Binance charts on every pair, ETH-BTC included"
  ctl user-service flags set market.house_liquidity --on --allow-symbols "$allow" --reason "every order trades against HOUSE (ADR-0015, 2026-10-02)"
  ;;
open)
  symbols=$(jq -r '.pairs[] | select(.quote_asset == "USDT" and .reference_symbol != null) | .symbol' "$DATA")
  listed=$(ctl instrument-service instruments list)
  for s in $symbols; do
    if grep -E "^$s[[:space:]]" <<<"$listed" | grep -q PREPARE; then
      ctl instrument-service instruments pair-status "$s" --to TRADING --reason "top 50 with HOUSE liquidity (ADR-0015)"
    fi
  done
  ;;
show)
  # shellcheck disable=SC2016 # expanded on the server
  ssh exchange "cd $INFRA && set -a && . ./.env && set +a && sudo docker compose exec -T postgres psql -U \"\$POSTGRES_USER\" -d exchange -At -c \"SELECT asset, available FROM ledger.accounts WHERE account_type = 'MARKET_MAKER' ORDER BY asset\""
  ;;
*)
  sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
