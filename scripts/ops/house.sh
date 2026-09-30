#!/usr/bin/env bash
# HOUSE's virtual liquidity on the test server (ADR-0013, ADR-0015,
# docs/runbook/market-maker.md):
#
#   scripts/ops/house.sh seed   HOUSE's spot inventory: 500,000 USDT and
#                               about 20,000 USDT's worth of BTC and ETH
#                               (MARKET_MAKER, audited adjustments; needs
#                               ledger.manual_adjustment). Idempotent.
#   scripts/ops/house.sh flags  public books from Binance everywhere
#                               (market.reference_depth) and HOUSE on the
#                               USDT pairs of deploy/instruments/test.json
#                               and BTC-USDT-PERP (market.house_liquidity);
#                               ETH-USDT-PERP stays users against users.
#   scripts/ops/house.sh open   the USDT pairs of deploy/instruments/test.json
#                               still PREPARE move to TRADING.
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
  for spec in "USDT 500000" "BTC 0.24" "ETH 7.4"; do
    read -r asset amount <<<"$spec"
    ctl ledger-service ledger adjust --house --asset "$asset" --amount "$amount" \
      --reason "HOUSE inventory (ADR-0013)" --key "seed-house-$asset-v1"
  done
  ;;
flags)
  allow="$(jq -r '[.pairs[] | select(.quote_asset == "USDT") | .symbol] | join(",")' "$DATA"),BTC-USDT-PERP"
  ctl user-service flags set market.reference_depth --on --reason "Binance books on every followed symbol (ADR-0010)"
  ctl user-service flags set market.house_liquidity --on --allow-symbols "$allow" --reason "HOUSE liquidity on the top 50 and BTC-USDT-PERP (ADR-0015)"
  ;;
open)
  symbols=$(jq -r '.pairs[] | select(.quote_asset == "USDT") | .symbol' "$DATA")
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
  sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
