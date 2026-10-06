#!/usr/bin/env bash
# HOUSE's virtual liquidity on the test server (ADR-0013, ADR-0015,
# docs/runbook/market-maker.md):
#
#   scripts/ops/house.sh seed   HOUSE's funds: spot inventory of 5,000,000
#                               USDT, 11.74 BTC and 370.4 ETH (MARKET_MAKER;
#                               about half the pair cap each, so HOUSE can
#                               sell and buy about 1,000,000 USDT of them),
#                               and 2,000,000 USDT of contract margin in its
#                               FUTURES account (HOUSE_USER_ID), 6 BTC and 200
#                               ETH in its coin-margined ones, and the
#                               insurance fund's BTC, ETH and ASTRA rows;
#                               500,000 USD of margin more for every other
#                               contract listed with a reference market
#                               (and 100,000 USD of insurance fund for its
#                               coin when coin-margined);
#                               audited adjustments, needs
#                               ledger.manual_adjustment. Idempotent.
#   scripts/ops/house.sh flags  public books and charts from Binance everywhere
#                               (market.reference_depth, market.reference_kline)
#                               and HOUSE on every pair listed now that follows
#                               a reference market (deploy/instruments and the
#                               console's listings alike) and the contracts on
#                               them (market.house_liquidity): every order
#                               trades against HOUSE (user decision 2026-10-02;
#                               market.internal_matching stays off).
#   scripts/ops/house.sh open   the USDT pairs of deploy/instruments/test.json
#                               that follow Binance and are still PREPARE
#                               move to TRADING.
#   scripts/ops/house.sh open-contracts [N]
#                               the contracts listed now that follow Binance
#                               (gen-contracts.go, coin-margined design
#                               2026-10-06 §3.4) and are still PREPARE move
#                               to TRADING, N of them (all without N); seed
#                               and flags first, so HOUSE quotes them.
#   scripts/ops/house.sh show   HOUSE's MARKET_MAKER balances.
#
# Internal assets need no inventory: HOUSE may sell them short (ADR-0013).
set -euo pipefail

INFRA=/opt/exchange/infra
DATA="$(dirname "$0")/../../deploy/instruments/test.json"
API="${API:-https://astras.vip}"
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
  # The coin-margined contracts settle in their coin (coin-margined design
  # 2026-10-06 §2.3): HOUSE's BTC and ETH FUTURES accounts, each about
  # 500,000 USD (ten times that is HOUSE_CONTRACT_CAP), and the insurance
  # fund's rows of the three coins (the coordinator's 20:45 decision 7:
  # BTC +2 and ETH +40 on what margin trading put there, ASTRA 10% of a
  # coin-margined contract's 1,000,000 USD).
  for spec in "BTC 6 v1" "ETH 200 v1"; do
    read -r asset amount version <<<"$spec"
    ctl ledger-service ledger house-margin --asset "$asset" --amount "$amount" \
      --reason "HOUSE coin-margined contract margin (ADR-0015)" --key "seed-house-margin-$asset-$version"
  done
  for spec in "BTC 2 v1" "ETH 40 v1" "ASTRA 100000 v1"; do
    read -r asset amount version <<<"$spec"
    ctl ledger-service ledger insurance-fund --asset "$asset" --amount "$amount" \
      --reason "insurance fund of the coin-margined contracts" --key "seed-insurance-coinm-$asset-$version"
  done
  # Every further contract HOUSE quotes (one with a reference market,
  # coin-margined design §3.4, batch G1c) brings 10% of its
  # HOUSE_CONTRACT_CAP (5,000,000 USD, the cap at 10x) of margin once,
  # keyed by the contract: USDT for a USDT-margined one; its coin at the
  # mark price for a coin-margined one, whose coin's insurance fund gets
  # 100,000 USD of it once too. The four above are seeded already. A coin
  # amount follows the mark price: run again later it differs, and its key
  # answers COMMON_IDEMPOTENCY_CONFLICT, which says the seed was done.
  once() {
    local out
    out=$(ctl "$@" </dev/null 2>&1) && { echo "$out"; return 0; }
    if grep -q COMMON_IDEMPOTENCY_CONFLICT <<<"$out"; then
      echo "already seeded (its key was used): ${*: -1}"
      return 0
    fi
    echo "$out" >&2
    return 1
  }
  contracts="$(curl -fsS "$API/v1/market/contracts?margin_type=ALL")"
  while IFS=$'\t' read -r symbol margin settle; do
    case "$symbol" in BTC-USDT-PERP | ETH-USDT-PERP | BTC-USD-PERP | ETH-USD-PERP) continue ;; esac
    if [[ $margin != COIN ]]; then
      once ledger-service ledger house-margin --amount 500000 \
        --reason "HOUSE contract margin: 10% of the cap of $symbol (G1c)" --key "seed-house-margin-$symbol-v1"
      continue
    fi
    mark="$(curl -fsS "$API/v1/market/$symbol/mark-price" | jq -r '.mark_price // empty')"
    [[ -n $mark ]] || { echo "$symbol has no mark price: skipped, seed again later" >&2; continue; }
    once ledger-service ledger house-margin --asset "$settle" --amount "$(awk -v m="$mark" 'BEGIN { printf "%.4f", 500000 / m }')" \
      --reason "HOUSE coin-margined contract margin: 10% of the cap of $symbol (G1c)" --key "seed-house-margin-$symbol-v1"
    once ledger-service ledger insurance-fund --asset "$settle" --amount "$(awk -v m="$mark" 'BEGIN { printf "%.4f", 100000 / m }')" \
      --reason "insurance fund of the coin-margined contracts (G1c)" --key "seed-insurance-coinm-$settle-v1"
  done < <(jq -r '.contracts[] | select((.reference_symbol // "") != "") | [.symbol, .margin_type, .settle_asset] | @tsv' <<<"$contracts")
  ;;
flags)
  # The pairs and contracts as listed now, not test.json: one listed from
  # the console (LINK-BTC) keeps HOUSE when it follows a reference market.
  # Pairs with their own market (the platform coin), and contracts without
  # a Binance perpetual (its two), have no HOUSE; the coin-margined
  # contracts are listed on request (margin_type=ALL).
  pairs="$(curl -fsS "$API/v1/market/pairs")"
  contracts="$(curl -fsS "$API/v1/market/contracts?margin_type=ALL")"
  allow="$(jq -rn --argjson p "$pairs" --argjson c "$contracts" '[$p.pairs[] | select((.reference_symbol // "") != "") | .symbol] as $followed |
    [$followed[], ($c.contracts[] | select((.reference_symbol // "") != "") | .symbol)] | join(",")')"
  [ -n "$allow" ] || { echo "no followed pair listed: nothing changed" >&2; exit 1; }
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
open-contracts)
  limit=${2:-0}
  [[ $limit =~ ^[0-9]+$ ]] || { echo "open-contracts N: a count" >&2; exit 2; }
  contracts="$(curl -fsS "$API/v1/market/contracts?margin_type=ALL")"
  opened=0
  for s in $(jq -r '.contracts[] | select(.status == "PREPARE" and (.reference_symbol // "") != "") | .symbol' <<<"$contracts"); do
    ctl instrument-service instruments contract-status "$s" --to TRADING --reason "Binance's perpetuals open in batches (G1c)" </dev/null
    opened=$((opened + 1))
    if ((limit > 0 && opened >= limit)); then break; fi
  done
  echo "$opened contracts opened, $(jq '[.contracts[] | select(.status == "PREPARE" and (.reference_symbol // "") != "")] | length' <<<"$contracts") were PREPARE"
  ;;
show)
  # shellcheck disable=SC2016 # expanded on the server
  ssh exchange "cd $INFRA && set -a && . ./.env && set +a && sudo docker compose exec -T postgres psql -U \"\$POSTGRES_USER\" -d exchange -At -c \"SELECT asset, available FROM ledger.accounts WHERE account_type = 'MARKET_MAKER' ORDER BY asset\""
  ;;
*)
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
