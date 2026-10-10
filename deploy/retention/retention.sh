#!/usr/bin/env bash
# 历史数据保留（M1，ADR-0022；测试服，每日一次）：删除 15 天前的历史（exchangectl retention run），
# 在运维锁下（与部署、端到端、演练轮流），之后跑一次账本对账；输出追加到 /opt/exchange/logs/retention.log。
# 用法：bash /opt/exchange/infra/retention/retention.sh [--dry-run] [--days 15] ...
# （systemd 的 exchange-retention.timer 每日 04:10 UTC 触发，03:30 的备份之后；部署只装不启用）
set -euo pipefail
INFRA_DIR="${INFRA_DIR:-/opt/exchange/infra}"
LOG="${LOG:-/opt/exchange/logs/retention.log}"
mkdir -p "$(dirname "$LOG")"
COMPOSE=(sudo docker compose -f "$INFRA_DIR/docker-compose.yml" -f "$INFRA_DIR/docker-compose.apps.yml")
{
  echo "== $(date -u +%Y-%m-%dT%H:%M:%SZ) retention $*"
  # The same lock scripts/ops/lock.sh takes from a laptop.
  exec 9>"$INFRA_DIR/ops.lock"
  if ! flock -w 3600 9; then
    echo "the ops lock stayed taken an hour ($(cat "$INFRA_DIR/ops.lock.owner" 2>/dev/null)): skipped"
    exit 1
  fi
  printf 'cron retention.sh (server) since %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$INFRA_DIR/ops.lock.owner"
  trap ': >"$INFRA_DIR/ops.lock.owner"' EXIT
  "${COMPOSE[@]}" exec -T -e EXCHANGECTL_ACTOR=cron-retention user-service /app/exchangectl retention run "$@"
  "${COMPOSE[@]}" exec -T ledger-service /app/exchangectl ledger reconcile
} >>"$LOG" 2>&1
