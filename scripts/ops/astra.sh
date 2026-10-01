#!/usr/bin/env bash
# The platform coin ASTRA on the test server (docs/设计-平台币ASTRA与模拟做市-2026-10-02.md):
#
#   scripts/ops/astra.sh profile  its display name, introductions, site and
#                                 default logo (deploy/instruments/astra.svg)
#                                 through exchangectl; operators change them
#                                 later in the admin console. Each run is a
#                                 new profile version.
#   scripts/ops/astra.sh open     ASTRA-USDT moves from PREPARE to TRADING.
#                                 It follows no reference market, so users
#                                 trade with each other (ADR-0015 rule 6);
#                                 the bots come with batch A2.
set -euo pipefail

INFRA=/opt/exchange/infra
ROOT="$(dirname "$0")/../.."
COMPOSE="sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml"

# ctl SERVICE ARGS... runs exchangectl in a service's container, stdin passed on.
ctl() {
  local service=$1 args
  shift
  printf -v args ' %q' "$@"
  ssh exchange "cd $INFRA && $COMPOSE exec -T $service /app/exchangectl$args"
}

case "${1:-}" in
profile)
  ctl instrument-service instruments profile ASTRA --display-name Astra \
    --zh "ASTRA 是 Astras 的平台币，只在本站交易，不能充值或提现；它的行情来自平台自己的市场（学习项目的模拟市场）。" \
    --en "ASTRA is the Astras platform coin. It trades only here and cannot be deposited or withdrawn; its market is the platform's own (the simulated market of a learning project)." \
    --website https://astras.vip --logo - --logo-type image/svg+xml \
    --reason "ASTRA's default profile (design 2026-10-02 §5.3)" <"$ROOT/deploy/instruments/astra.svg"
  ;;
open)
  ctl instrument-service instruments pair-status ASTRA-USDT --to TRADING --reason "ASTRA-USDT opens (design 2026-10-02 batch A1)" </dev/null
  ;;
*)
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
