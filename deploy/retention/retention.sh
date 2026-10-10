#!/usr/bin/env bash
# 历史数据保留（M1，ADR-0022；测试服，每日一次）：删除 15 天前的历史（exchangectl retention run），
# 在运维锁下（与部署、端到端、演练轮流），之后跑一次账本对账。输出同时进 journald（systemd 单元的标准输出）
# 与 /opt/exchange/logs/retention.log（超过 10 MB 时轮转一份）；成功后把完成时间写进 node exporter 的 textfile
# 指标 exchange_retention_last_success_timestamp_seconds（告警 RetentionRunStale：两天没成功）。
# 用法：bash /opt/exchange/infra/retention/retention.sh [--dry-run] [--days 15] ...
# （systemd 的 exchange-retention.timer 每日 04:10 UTC 触发，03:30 的备份之后；部署只装不启用）
set -euo pipefail
INFRA_DIR="${INFRA_DIR:-/opt/exchange/infra}"
LOG="${LOG:-/opt/exchange/logs/retention.log}"
METRICS_DIR="${METRICS_DIR:-$INFRA_DIR/metrics}"
mkdir -p "$(dirname "$LOG")" "$METRICS_DIR"
if [[ -f $LOG && $(stat -c %s "$LOG") -gt 10485760 ]]; then
  mv "$LOG" "$LOG.1"
fi
exec > >(tee -a "$LOG") 2>&1
COMPOSE=(sudo docker compose -f "$INFRA_DIR/docker-compose.yml" -f "$INFRA_DIR/docker-compose.apps.yml")

echo "== $(date -u +%Y-%m-%dT%H:%M:%SZ) retention $*"
# The same lock scripts/ops/lock.sh takes from a laptop.
exec 9>"$INFRA_DIR/ops.lock"
if ! flock -w 3600 9; then
  echo "the ops lock stayed taken an hour ($(cat "$INFRA_DIR/ops.lock.owner" 2>/dev/null)): skipped"
  exit 1
fi
printf 'cron retention.sh (server) since %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$INFRA_DIR/ops.lock.owner"
trap ': >"$INFRA_DIR/ops.lock.owner"' EXIT
# A container of its own (the services' image and settings), not one inside
# user-service's: stopping the unit stops the run, and the run does not
# share the service's memory (B199).
"${COMPOSE[@]}" run --rm --no-deps -T -e EXCHANGECTL_ACTOR=cron-retention user-service /app/exchangectl retention run "$@"
"${COMPOSE[@]}" exec -T ledger-service /app/exchangectl ledger reconcile
printf '# HELP exchange_retention_last_success_timestamp_seconds When the history retention last finished without a failure.\n# TYPE exchange_retention_last_success_timestamp_seconds gauge\nexchange_retention_last_success_timestamp_seconds %s\n' \
  "$(date +%s)" >"$METRICS_DIR/exchange_retention.prom.tmp"
mv "$METRICS_DIR/exchange_retention.prom.tmp" "$METRICS_DIR/exchange_retention.prom"
echo "== $(date -u +%Y-%m-%dT%H:%M:%SZ) done"
