#!/usr/bin/env bash
# 幂等创建 Redpanda topic（测试环境，单 broker，副本数 1）。
# 用法（在测试服上）：bash /opt/exchange/infra/redpanda/topics.sh
# KAFKA_NAMESPACE=dev. 时建带前缀的一套（本机开发栈 scripts/dev.sh 用，见 docs/runbook/local-dev.md）。
# 已存在的 topic 只核对，不改分区；保留期按需求文档 §8.2：业务 topic 7 天，order.commands / trade.events / *.dlq 30 天。
set -euo pipefail

INFRA_DIR="${INFRA_DIR:-/opt/exchange/infra}"
NS="${KAFKA_NAMESPACE:-}"
DAY_MS=86400000
HOUR_MS=3600000
GIB=$((1024 * 1024 * 1024))

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
  "derivatives.order.events 3"
  "derivatives.trade.events 3"
  "derivatives.position.events 3"
  "derivatives.liquidation.events 1"
  "margin.events 3"
  "market.candle.events 3"
  "market.candle.flats 3"
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

# 币安合约的强平单（LiquidationOccurred，设计 2026-10-06 §3.3，G3b 发布）：网关的 liquidations 频道实时推送，
# analytics 写进 ClickHouse futures_liquidations（保留 7 天），topic 本身只保留 1 天
ensure "${NS}market.liquidations" 3 1
ensure "${NS}market.liquidations.retry" 1 7
ensure "${NS}market.liquidations.dlq" 1 30

# 撮合引擎输入命令，需要可重放，保留 30 天（现货与合约分片各一个）
ensure "${NS}order.commands" 3 30
ensure "${NS}derivatives.order.commands" 3 30

# 派生状态只保留 6 小时（每分区至多 6 GiB），没有 retry/dlq，丢了由下一份补上（ADR-0015）：
#   - 引擎的第二个输入：虚拟流动性的参考簿（分区数必须与 order.commands 相同，同一交易对落在同一分区号）；
#   - 公共深度（market-data-service 发布）与引擎自己的深度（*.internal）；
#   - 公共成交（market.trades，现货与合约共用）；
#   - 杠杆账户的当前状态（margin.accounts，margin 频道的 ACCOUNT 推送）。
DERIVED=("${NS}order.references" "${NS}derivatives.order.references" "${NS}market.depth" "${NS}derivatives.market.depth"
  "${NS}market.depth.internal" "${NS}derivatives.market.depth.internal" "${NS}market.trades" "${NS}margin.accounts")
for t in "${DERIVED[@]}"; do
  if grep -qx "$t" <<<"$existing"; then
    echo "exists : $t"
  else
    rpk topic create "$t" -p 3 -r 1 -c "retention.ms=$((6 * HOUR_MS))" -c "retention.bytes=$((6 * GIB))" >/dev/null
    echo "created: $t (partitions=3, retention=6h)"
  fi
done

# 需要重放的 topic 保留 30 天（对已存在的 topic 也生效）
for t in "${NS}trade.events" "${NS}order.commands" "${NS}derivatives.trade.events" "${NS}derivatives.order.commands"; do
  rpk topic alter-config "$t" --set "retention.ms=$((30 * DAY_MS))" >/dev/null && echo "retention: $t = 30d"
done

# 保留期与大小上限（用户 2026-10-07 21:17 批准的方案，审查 C56 ③），对已存在的 topic 也生效；时间与每分区字节数
# 哪个先到就删最旧的段：
#   - 业务与可重放的 topic（含 .retry/.dlq、order.commands）：时间照上面不变，每分区再加 20 GiB 作灾难保险——
#     眼下每个都不到 1.5 GB，正常碰不到，只防写满磁盘（2026-10-01 磁盘写满时 Docker 丢了运行中容器的网络端点）；
#   - 派生状态：6 小时、每分区 6 GiB。它们只被实时跟读（tail），没人回放，6 小时够当场排查；实测每个约
#     0.6–0.7 GB/小时，四个盘口类 topic 留一天要约 63 GB，不值得（2026-10-02 曾因留 7 天把磁盘写到 85%）；
#   - 行情 K 线与 ticker（market.candle.events）：同样只被跟读（衍生品服务的标记价、网关推送），按用户要求
#     （2026-10-07 21:09，测试服磁盘扩到 150 GB 后）留最近 3 天便于排查，约 8 GB/天，每分区 12 GiB。
# 预计 Redpanda 共约 45 GB。这里是唯一的真相：每次部署都对已存在的 topic 重设一遍，线上手工改的会被改回。
business=("${NS}market.liquidations" "${NS}market.liquidations.retry" "${NS}market.liquidations.dlq"
  "${NS}order.commands" "${NS}derivatives.order.commands")
for entry in "${BUSINESS[@]}"; do
  read -r name _ <<<"$entry"
  business+=("$NS$name" "$NS$name.retry" "$NS$name.dlq")
done
rpk topic alter-config "${business[@]}" --set "retention.bytes=$((20 * GIB))" >/dev/null &&
  echo "retention: ${#business[@]} business topics, at most 20 GiB per partition"
rpk topic alter-config "${DERIVED[@]}" --set "retention.ms=$((6 * HOUR_MS))" --set "retention.bytes=$((6 * GIB))" >/dev/null &&
  echo "retention: ${#DERIVED[@]} derived topics, 6h and at most 6 GiB per partition"
rpk topic alter-config "${NS}market.candle.events" --set "retention.ms=$((3 * DAY_MS))" --set "retention.bytes=$((12 * GIB))" >/dev/null &&
  echo "retention: ${NS}market.candle.events = 3d, at most 12 GiB per partition"

echo
rpk topic list
