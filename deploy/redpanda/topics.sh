#!/usr/bin/env bash
# 幂等创建 Redpanda topic（测试环境，单 broker，副本数 1）。
# 用法（在测试服上）：bash /opt/exchange/infra/redpanda/topics.sh
# KAFKA_NAMESPACE=dev. 时建带前缀的一套（本机开发栈 scripts/dev.sh 用，见 docs/runbook/local-dev.md）。
# 已存在的 topic 只核对，不改分区；保留期按需求文档 §8.2：业务 topic 7 天，order.commands / trade.events / *.dlq 30 天。
set -euo pipefail

INFRA_DIR="${INFRA_DIR:-/opt/exchange/infra}"
NS="${KAFKA_NAMESPACE:-}"
DAY_MS=86400000

rpk() {
  sudo docker compose -f "$INFRA_DIR/docker-compose.yml" --env-file "$INFRA_DIR/.env" exec -T redpanda rpk "$@"
}

# 每个分区的活动段按这个步长预分配磁盘，默认 32 MiB（近 100 个 topic 时约 3 GB）；测试环境流量小，降到 4 MiB，对新段生效
step=$((4 * 1024 * 1024))
if [ "$(rpk cluster config get segment_fallocation_step 2>/dev/null)" != "$step" ]; then
  rpk cluster config set segment_fallocation_step "$step" >/dev/null && echo "segment_fallocation_step = $step"
fi

existing="$(rpk topic list 2>/dev/null | awk 'NR>1 {print $1}')"

ensure() { # name partitions retention_days
  local name="$1" parts="$2" days="$3"
  if grep -qx "$name" <<<"$existing"; then
    echo "exists : $name"
  else
    rpk topic create "$name" -p "$parts" -r 1 -c "retention.ms=$((days * DAY_MS))" >/dev/null
    echo "created: $name (partitions=$parts, retention=${days}d)"
  fi
}

# 业务 topic：名称 分区数
BUSINESS=(
  "auth.events 1"
  "user.events 1"
  "instrument.events 1"
  "account.events 1"
  "order.events 3"
  "trade.events 3"
  "ledger.events 3"
  "wallet.deposit.events 1"
  "wallet.withdrawal.events 1"
  "derivatives.position.events 3"
  "derivatives.liquidation.events 1"
  "market.candle.events 3"
  "risk.events 1"
  "audit.events 1"
  "notification.events 1"
)

for entry in "${BUSINESS[@]}"; do
  read -r name parts <<<"$entry"
  ensure "$NS$name" "$parts" 7
  ensure "$NS$name.retry" 1 7
  ensure "$NS$name.dlq" 1 30
done

# 撮合引擎输入命令，需要可重放，保留 30 天
ensure "${NS}order.commands" 3 30

# 需要重放的 topic 保留 30 天（对已存在的 topic 也生效）
for t in "${NS}trade.events" "${NS}order.commands"; do
  rpk topic alter-config "$t" --set "retention.ms=$((30 * DAY_MS))" >/dev/null && echo "retention: $t = 30d"
done

echo
rpk topic list
