# 撮合引擎运维

需求 §5.7、§11.1、§11.2，ADR-0002；实现见 `internal/matching`（matching-engine，运维端口 9089，schema `matching`），契约 `api/proto/exchange/order/v1`（命令与订单事件）、`api/proto/exchange/trade/v1`（成交）。

## 输入与输出

- 输入：
  - `order.commands`（按交易对分区）：交易服务在冻结成功后发 PlaceOrder，撤单时发 CancelOrder。每条命令自带撮合所需的参数（费率、资产精度、lot、市价保护价、是否只与 HOUSE 成交），引擎不查任何服务。
  - `order.references`（阶段 4 B4，ADR-0015）：market-maker 发的 HOUSE 参考簿 `ReferenceBookUpdate`，分区数与 `order.commands` 相同，同一交易对落在同一分区号，引擎把两个主题的同号分区当作一个输入按到达顺序处理。见下文「HOUSE 的参考簿」。
- 输出经本 schema 的 outbox 发布：
  - `order.events`：OrderOpened、OrderPartiallyFilled、OrderFilled、OrderCanceled（原因 USER、IOC、FOK、SELF_TRADE、NO_LIQUIDITY）、OrderRejected（`ORDER_WOULD_TAKE`、`ORDER_NO_LIQUIDITY`、`ORDER_SELF_TRADE`）。
  - `trade.events`：TradeExecuted。成交 ID 由交易对与 sequence 派生，重放得到同样的 ID；`trade_number` 是交易对内从 1 开始的成交编号（订单簿状态的一部分，随快照保存），下游据此发现漏掉的成交（账本对账 `TRADES_NUMBERED`，任务 5 的公开成交 ID）。编号字段上线前发出的成交为 0。
- 每个事件带交易对内递增的 `sequence`，排序只看它，不看墙上时钟。
- 引擎不持有余额（ADR-0002）。账本消费成交做结算（见 [ledger.md](ledger.md#成交结算)）；交易服务消费订单事件更新订单，在终态解冻剩余（见 [trading.md](trading.md)）。

## 分片：现货与合约

同一个二进制按 `MATCHING_SHARD` 跑两个独立实例（实施计划 §7.3 任务 2），订单簿、WAL、租约、消费组互不相干：

| 分片 | 容器 | 运维端口 | schema | 租约 | 消费组 | 输入 | 输出 |
|---|---|---|---|---|---|---|---|
| `spot`（默认） | matching-engine | 9089 | `matching` | `matching-engine` | `matching-engine` | `order.commands`、`order.references` | `order.events`、`trade.events`、`market.depth.internal` |
| `derivatives` | derivatives-engine | 9096 | `deriv_matching` | `derivatives-engine` | `derivatives-engine` | `derivatives.order.commands`、`derivatives.order.references` | `derivatives.order.events`、`derivatives.trade.events`、`derivatives.market.depth.internal` |

引擎的深度只发到 `*.internal`，公共盘口由 market-data-service 发布（见 [market-data.md](market-data.md)）。

- 合约分片的撮合规则与现货完全相同，只是"交易对"换成合约代码（如 `BTC-USDT-PERP`），命令由 derivatives-service 发出。引擎的费率字段合约下发 0：合约手续费以 USDT 计，由 derivatives-service 按成交算（引擎仍然不碰余额与持仓）。
- 两个分片的指标名相同，按抓取任务（`job="derivatives-engine"`）或 `outbox_pending{schema="deriv_matching"}` 区分。
- 现货的查询与处理方法对合约分片同样适用，把 schema 换成 `deriv_matching`。

## 撮合规则

- 价格优先、时间优先，按挂单方（maker）价格成交。
- TIF：
  - GTC 未成交部分挂单。
  - IOC 未成交部分撤销。
  - FOK 先预演，不能全部成交就整单撤销、不产生成交。
  - POST_ONLY 若会立即成交就拒绝。
- 市价单：
  - 买单按 `quote_amount` 逐档买入整 lot；剩余金额连一个 lot 都买不起时，订单算 FILLED，剩余金额由交易服务解冻。
  - 卖单按数量逐档卖出。
  - 都不越过保护价（有锚点时由交易服务算好随命令下发）。
  - 对手盘为空或第一档就超出保护价：拒单 `ORDER_NO_LIQUIDITY`；成交一部分后盘面用完：撤销剩余，原因 NO_LIQUIDITY。
- 自成交保护（新单的模式）：
  - CANCEL_NEWEST（默认）：新单停止撮合。尚无成交则拒单 `ORDER_SELF_TRADE`，有成交则撤销剩余。
  - CANCEL_OLDEST：撤掉自己的挂单，继续撮合。
  - CANCEL_BOTH：两者都撤。
- 手续费按角色费率（taker 用自己订单的 taker 费率，maker 用 maker 费率）。买方以基础资产付，卖方以计价资产付，向上取整到资产精度（向平台有利，需求 §10.3）。
- 交易对配置保证 tick × lot 能被计价资产精度整除（instrument-service 校验）。所以成交额、冻结额和剩余解冻额都是精确值，只有手续费需要取整。

## HOUSE 的参考簿（ADR-0015）

- 每个订单簿可以带一份参考簿：HOUSE 在各价位愿意成交的数量（参考市场盘口前 20 档，已放到本交易对的价格步长上、按档封顶）与它还能买、还能卖的数量（`buy_room`、`sell_room`）。新的参考簿整份替换旧的；空簿表示 HOUSE 不提供流动性。成交会消耗档位与额度，直到下一份参考簿到来。
- 合并撮合：新订单按价格优先在"其他用户的挂单"与"HOUSE 的档位"之间取更好的价格，同价时用户挂单优先；和 HOUSE 成交时 HOUSE 是挂单方（maker），价格是参考簿的档位价。订单带 `house_only`（交易服务在 `market.house_liquidity` 打开、`market.internal_matching` 关闭时设置）时只和 HOUSE 成交。
- 触发：参考簿更新时，簿上与新参考价交叉的用户挂单按**其限价**与 HOUSE 成交（HOUSE 为吃单方），按价格优先、时间优先，受额度限制。
- 新鲜度：参考簿比订单的时间早 5 秒以上（`RefMaxAge`，两边的时间都在消息里，重放结果一致）时不用于成交；触发只在参考簿自己更新时发生。
- HOUSE 一侧的成交带 `house_side` 与 HOUSE 的用户 ID，下游据此结算（现货记到 `MARKET_MAKER`，见 [ledger.md](ledger.md#house-的现货成交adr-0013)；合约是 HOUSE 账户的普通结算，见 [derivatives.md](derivatives.md)）。FOK 预演同样按合并后的盘口。
- 成交事件、订单事件与普通成交相同；HOUSE 没有订单，没有 HOUSE 一侧的订单事件。

## 持久化与恢复

- 引擎按批处理命令与参考簿（最多 500 条或 20 毫秒）。同一事务里写入：
  - 命令与参考簿进 WAL（`matching.wal`，按分区内的应用顺序编号 `seq`，并记来源主题 `source` 与偏移）；
  - 产生的事件进 outbox；
  - 需要时写快照。每个分区最多 `MATCHING_SNAPSHOT_EVERY`（默认 1000）条后存一次该分区全部订单簿与参考簿（`matching.snapshots`，带覆盖到的 `seq` 与两个主题各自应用到的偏移）。
- WAL 已有的（来源, 偏移）不再处理，所以 Kafka 重投没有影响。格式错误的命令记错误日志后跳过，不写 WAL，也不会卡住分区。
- 启动时，以及写库失败后下一批处理前：从各分区最新快照恢复订单簿，再按 `seq` 重放快照之后的 WAL，不重复发事件。这样内存状态不会领先于已落库的状态。
- 快照已覆盖、且早于 `MATCHING_WAL_RETENTION` 的 WAL 每小时清理。默认 24 小时；参考簿每个交易对每秒最多 4 份（2026-10-01 实测 50 个交易对约每秒 106 条、每条约 1 KB，每小时约 400 MB），测试服两个引擎都设为 2 小时（`docker-compose.apps.yml`），`matching.wal` 稳定在 1.5 GB 以内。
- 主备：进程启动时先取数据库租约（会话级咨询锁 `lease:matching-engine`）才开始消费。第二个实例每秒试一次锁，停在"waiting for the engine lease"。持有者每 5 秒 ping 一次租约连接，失败或超过 5 秒即视为丢失并退出，由容器重启后重新竞争。测试环境只跑一个实例。
- 首次部署时引擎从头消费 `order.commands`，任务 2 以来的全部下单与撤单都会重放一遍（当时还没撤掉的订单会互相成交）。那时的命令不带资产与 lot：资产按交易对代码 `BASE-QUOTE` 拆出，lot 退回基础资产的最小单位。

## 查看

```sql
-- 各分区的快照位置与 WAL 长度（PostgreSQL，schema matching）
SELECT partition, seq, "offset", ref_offset, taken_at, jsonb_array_length(books) AS books FROM matching.snapshots;
SELECT partition, source, count(*), max(seq) FROM matching.wal GROUP BY partition, source ORDER BY 1, 2;
SELECT pg_size_pretty(pg_total_relation_size('matching.wal'));
```

- 指标：`matching_commands_total{type}`（PlaceOrder、CancelOrder、ReferenceBookUpdate、invalid）、`matching_trades_total`、`kafka_consumer_lag{group="matching-engine"}`、`outbox_pending{schema="matching"}`。
- 日志：`matching engine recovered`（恢复时的快照数、重放条数、订单簿数）、`invalid command skipped`。
- ClickHouse：`SELECT occurred_at, payload FROM events WHERE topic = 'trade.events' ORDER BY occurred_at DESC LIMIT 20`。

## 故障与处理

| 情况 | 表现 | 处理 |
|---|---|---|
| 引擎重启 | 恢复期间命令在 Kafka 排队 | 自动：快照 + WAL 重建后继续消费 |
| PostgreSQL 不可用 | 批写入失败，消费者退避重试，积压上升 | 恢复后自动重建并继续；事件只发一次 |
| Redpanda 不可用 | 收不到命令；outbox 待发上升 | 恢复后自动继续 |
| 快照损坏或需要从头重放 | — | 停引擎，删除 `matching.snapshots` 行，重启会从 WAL 头重放（WAL 需完整覆盖该分区；清理过的部分无法重放） |
