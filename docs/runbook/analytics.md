# 分析读模型（ClickHouse 与 analytics-consumer）

需求 §9，阶段 1 验收标准 8，实施计划 §6.3 任务 12。analytics-consumer（运维端口 9087）以消费组 `analytics-consumer` 按批消费全部业务主题，写入 ClickHouse（库 `exchange`，迁移在 `migrations/clickhouse`，服务启动时执行），写入成功后才提交位点；失败整批重试。所有表都是 ReplacingMergeTree，重复投递的事件在合并时折叠，**查询要加 `FINAL`**（或按 ID 去重）。

## 表

| 表 / 视图 | 内容 | 来源 |
|---|---|---|
| `events` | 全部业务事件，payload 为 protojson（未链接的类型存 `@type` + `@raw` base64），保留 1 年 | 全部主题 |
| `event_ingest_log` | 主题/分区/位点，保留 30 天 | `events` 的物化视图 |
| `audit_logs` | 审计事件，按操作者与时间 | `audit.events` |
| `ledger_entries` | 每条分录行（资产、账户、金额、写后余额） | `ledger.EntryPosted` |
| `trades` | 每笔成交：价格、数量、成交额、主动方、双方订单与用户、手续费（买方付 base、卖方付 quote） | `trade.TradeExecuted` |
| `orders` | 订单受理时的属性（方向、类型、TIF、价格、数量、冻结） | `order.OrderAccepted` |
| `order_updates` | 订单每次变化：NEW / OPEN / PARTIALLY_FILLED / FILLED / CANCELED / REJECTED、累计成交、撤单原因或拒绝码、引擎 sequence | `order.events` 全部六种事件 |
| `orders_current`（视图） | 每个订单的最新状态 + 受理属性（资金检查就被拒的订单没有受理属性） | `order_updates` ⋈ `orders` |
| `wallet_deposits` | 每笔充值的最新快照（状态、确认数、是否未认领、入账 journal） | `wallet.deposit.events`（地址分配事件除外） |
| `wallet_withdrawals` | 每笔提现的最新快照（状态、手续费、交易哈希、风控原因） | `wallet.withdrawal.events` |
| `candles_1m` | 一分钟 K 线（开高低收、成交量、成交额、笔数） | 每批带成交的分钟由 `trades FINAL` 重新计算，最新一次计算生效 |
| `candles(symbol, seconds)`（参数化视图） | 任意周期 K 线，按 epoch（UTC）对齐 | `candles_1m` |
| `read_model_backfills` | 已完成的回填 | analytics-consumer |

- 充值与提现快照的 `version` = 事件毫秒时间 × 16 + 状态进度，同一毫秒的两个事件（提现申请与风控评分在同一事务里）按状态先后取后者。
- 订单、充值、提现的创建时间在 UUIDv7 ID 里：`UUIDv7ToDateTime(order_id)`。
- 金额列为 `Decimal128(18)`；ID 列为 `UUID`，格式不对的事件记入 `analytics_rejected_rows_total` 并告警日志，不写入读模型。
- 钱包事件类型从任务 12 起链接进 analytics-consumer，之后的钱包事件在 `events` 里是可读的 JSON；之前的是 `@raw`，回填按原始字节解码。

## 回填

读模型在事件之后才出现（任务 12），analytics-consumer 启动后在后台把 `events` 里已有的订单、成交、钱包事件按主题、按时间分页（每页 5,000）投影一遍，完成后在 `read_model_backfills` 记 `read-models-v1`，以后不再执行；失败每分钟重试。与实时消费同时写同一行没有问题（ReplacingMergeTree 折叠，K 线以最新计算为准）。需要重算：

```sql
TRUNCATE TABLE trades; TRUNCATE TABLE orders; TRUNCATE TABLE order_updates;
TRUNCATE TABLE wallet_deposits; TRUNCATE TABLE wallet_withdrawals; TRUNCATE TABLE candles_1m;
DELETE FROM read_model_backfills WHERE name = 'read-models-v1';
```

然后重启 analytics-consumer（`ch_query` 或 `clickhouse-client` 执行上面的语句）。新增读模型时换一个回填名。

## 常用查询

```sql
-- 最近 24 小时各交易对成交
SELECT symbol, count() AS trades, sum(quantity) AS volume, sum(quote_quantity) AS quote_volume
FROM trades FINAL WHERE executed_at > now() - INTERVAL 1 DAY GROUP BY symbol;

-- 撤单原因分布
SELECT reason, count() FROM orders_current WHERE status = 'CANCELED' GROUP BY reason ORDER BY count() DESC;

-- BTC-USDT 最近 24 根小时线
SELECT * FROM candles(symbol = 'BTC-USDT', seconds = 3600) ORDER BY open_time DESC LIMIT 24;

-- 尚未完成的提现
SELECT withdrawal_id, status, amount, updated_at FROM wallet_withdrawals FINAL
WHERE status NOT IN ('CONFIRMED', 'REJECTED', 'CANCELED', 'FAILED');
```

管理后台"报表"页（`/admin/v1/reports/{trading,wallet,candles}`，见 [admin.md](admin.md)）用的就是这些表：按交易对与 UTC 日的成交笔数/量/额与受理/被拒订单、按资产与日的入账充值与完成提现、任意交易对的 K 线。

## 核对

- 每小时：各服务 outbox 已发布的事件数与 `events` 比对（`analytics_reconcile_missing`，按主题，`RECONCILE_SCHEMAS` 列出 schema）。
- 端到端 `scripts/e2e/ops.sh`：一分钟前的成交与订单变化各自与事件一一对应；钱包读模型各状态的笔数与 wallet-service 的表一致（确认进度不发事件，比较时把 PostgreSQL 的 CONFIRMING/SIGNING 归到上一个有事件的状态）；一分钟 K 线的笔数之和等于成交数。
