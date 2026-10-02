# 事件链路运维（outbox、消费者、重试与死信）

需求 §8；实现见 `internal/platform/{event,outbox,inbox,kafka}`，topic 由 `deploy/redpanda/topics.sh` 幂等创建（每个业务 topic 另有 `.retry` 与 `.dlq`）。

## 链路

1. 业务事务里写 `outbox` 表（与业务数据同一事务，`outbox.Add`）。
2. 每个服务的 outbox relay 按插入顺序批量发布到 Redpanda（`acks=all`、幂等生产者），发布失败保留重试；指标 `outbox_pending`、`outbox_published_total`、`outbox_publish_failures_total`。
3. 消费者组手动提交位点（至少一次）；处理函数在同一事务里写 `inbox`（`inbox.Process`/`ProcessID`）或用业务幂等键（账本分录），重复投递不会重复生效。
4. 处理失败：记录转到 `<topic>.retry`，按 1s/5s/30s/5m 重试 4 次，仍失败转到 `<topic>.dlq`；无法解码的记录直接进 `.dlq`。记录头带 `x-origin-topic`、`x-group`（放弃它的消费组）、`x-attempt`、`x-not-before`、`x-error`。
5. 指标：`kafka_consumer_records_total{group,topic,result=ok|retry|dlq|skipped}`、`kafka_consumer_handle_seconds`、`kafka_consumer_lag`（每 30 秒）。

派生状态的主题不走 outbox、没有 `.retry`/`.dlq`，只保留 1 小时（`topics.sh` 每次部署都对已存在的这些 topic 重设一遍；`market.candle.events` 在测试服保留 1 天，它也只被实时跟读），丢一条由下一条补上：`order.references`、`derivatives.order.references`（HOUSE 的参考簿，market-maker 直接发；分区数必须与 `order.commands` 相同）、`market.depth`、`derivatives.market.depth`、`market.trades`（公共盘口与成交，market-data-service 发）、`market.depth.internal`、`derivatives.market.depth.internal`（引擎自己的深度）。见 ADR-0015 与 [market-data.md](market-data.md)。

Redpanda 停机时 API 照常工作，事件留在各服务的 outbox；恢复后自动重连并补发，消费组从已提交位点继续（`scripts/fault/redpanda-outage.sh` 验证）。

## 死信查看与重放

```bash
# 测试服（在任一应用容器里运行 exchangectl）
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl dlq list auth.events
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl dlq replay auth.events --all --group notification-service
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl dlq replay auth.events --offset 0:12
```

- `list` 列出死信：`分区:位点`、进入时间、消费组、尝试次数、事件类型与 ID、错误信息。
- `replay` 把选中的死信重新写入 `<topic>.retry`，作为**原消费组**的第 1 次尝试（`x-attempt=0`，带 `x-replayed-from`）：只有放弃它的那个组会再处理，其他组不受影响；再失败会重新走完 4 次重试后回到死信。
- 处理函数都是幂等的，同一条死信重放两次无害；无法解码的记录只能查看，不能重放。
- 先修复导致失败的原因（依赖不可用、数据问题、代码缺陷并部署），再重放。
- analytics-consumer（ClickHouse 批量写入）只会把无法解码的记录放进死信；ClickHouse 不可用时它整批退避重试，不产生死信（`scripts/fault/clickhouse-outage.sh` 验证）。

## 核对

- analytics-consumer 每小时按 topic 核对最近一段时间 outbox 已发布条数与 ClickHouse 条数，指标 `analytics_reconcile_missing{topic}`。这段时间最长 24 小时，并且落在 outbox 的保留期以内（保留期减 40 分钟），免得已清理的行被当成缺失。
- outbox 里已发布的行保留 `OUTBOX_RETENTION`（默认 7 天，各服务的 janitor 每小时清一次）；测试服磁盘小，compose 设为 6 小时（一天的 HOUSE 与模拟市场成交，光账本的 outbox 就约 0.4 GB）。
- 账本每小时核对三项不变量，见 [ledger.md](ledger.md)。
