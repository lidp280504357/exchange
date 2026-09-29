# 平台行情运维

需求 §5.11、§7.3、§11.8；实现见 `internal/marketdata`（market-data-service，HTTP 8090、运维 9090，schema `market`）、撮合引擎的深度导出（`internal/matching/application/depth.go`）与网关的 WebSocket 公共频道（`internal/gateway/wsmarket.go`）。契约 `api/openapi/market.yaml`、`api/proto/exchange/market/v1`。

## 数据流

```text
matching-engine ──market.depth（每 100 ms 变化的订单簿前 200 档，每 10 s 全量）──┬─> market-data-service（REST 深度、ticker 买一卖一）
                                                                                └─> api-gateway（depth: 频道）
trade.events ──> market-data-service ──market.candle.events（CandleUpdated/Closed、TickerUpdated，每 500 ms）──> api-gateway（candles:、ticker: 频道）
trade.events ──> api-gateway（trades: 频道、fills 私有频道）；order.events ──> api-gateway（orders 私有频道）
derivatives-engine ──derivatives.market.depth / derivatives.trade.events──> 同上（合约的深度、K 线、ticker、成交；合约的 fills 由 derivatives-service 推）
参考行情（币安）──> market-data-service ──每秒：MarkPriceUpdated、IndexPriceUpdated、FundingRateUpdated──> api-gateway（mark-price:、funding: 频道）
                                       └─标记价 10 秒算不出──> risk.events（SystemDegraded）
```

- `market.depth` 是派生状态：引擎从内存直接发，不走 outbox，丢一份由下一份补上；只保留 1 小时，没有 retry/dlq，不进 ClickHouse。市场服务和网关都从主题末尾读（`kafka.Tail`，不提交位移），启动后最多 10 秒拿到全部订单簿。
- `market.candle.events` 同样由市场服务直接发布，也不进 ClickHouse。
- 市场服务的消费组 `market-data` 读 `trade.events`：按交易对的 sequence 幂等（已应用的跳过），一批成交在一个事务里写 K 线、最近成交和进度；写库失败则内存状态作废，重新从库加载后重投。库里的数据都能从 `trade.events` 重建。

## 规则

- K 线周期 `1m 3m 5m 15m 30m 1h 2h 4h 6h 12h 1d 1w 1M`，UTC 对齐，周从周一开始。只存有成交的区间；查询时无成交区间用上一根收盘价补平（成交量 0），第一笔成交之前的区间不返回。
- 当前 K 线变化时每 500 ms 推 `CandleUpdated`；区间结束时推一次 `CandleClosed`，新区间在有成交前推一根平盘 K 线。
- 24 小时 ticker 按分钟计算：窗口是当前分钟加前 1439 分钟；`open` 是窗口前最后一笔成交价（之前没有成交时取窗口内第一笔），`change = (last − open) / open`（小数，8 位）；窗口内没有成交时 `last` 沿用上一笔、成交量 0；从未成交的交易对价格为 null。
- 最新成交价同时是下单价格带与市价保护价的锚点：交易服务从自己的 `fills` 取（缓存 1 秒），没有成交时用参考价。参考行情（币安公开数据，仅测试环境）与做市见 [market-maker.md](market-maker.md)；端到端脚本在没有做市的 ETH-BTC 上成交。

## 接口

REST（经网关，无需登录，`Cache-Control: public, max-age=1`）：

| 路径 | 内容 |
|---|---|
| `GET /v1/market/tickers` | 所有上架交易对的 ticker |
| `GET /v1/market/{symbol}/ticker` | 一个交易对的 ticker；未上架或已下线 404 |
| `GET /v1/market/{symbol}/depth?limit=` | 深度 `[价格, 数量]`，最多 200 档，带引擎 sequence |
| `GET /v1/market/{symbol}/trades?limit=` | 最近成交（最多 100 条，新的在前），带交易对内编号 `trade_number` |
| `GET /v1/market/{symbol}/candles?interval=&from=&to=&limit=` | K 线（最多 1000 根，时间 RFC 3339） |

WebSocket `wss://astras.vip/v1/ws`：

- 公共频道无需 `auth`：`ticker:{symbol}`、`depth:{symbol}`、`trades:{symbol}`、`candles:{symbol}:{interval}`（交易对与合约），合约另有 `mark-price:{symbol}`、`funding:{symbol}`（见下节）。消息 `{"channel": ..., "type": ..., "data": ...}`；订阅 ticker、K 线时先收到最近一条。
- 深度：订阅后先收 `{"type":"snapshot","seq":n,"data":{"bids":[...],"asks":[...]}}`，之后是 `{"type":"update","seq":n+1,"prev_seq":n,"data":{变化的档位}}`，数量 `"0"` 表示该档消失。`seq` 是本网关实例的计数，`prev_seq` 对不上就重新订阅；每 30 秒重发一次快照。
- 私有频道（需 `auth`，带每用户 `seq`，可用 `last_seq` 补发）新增 `orders`（订单状态变化：NEW、OPEN、PARTIALLY_FILLED、FILLED、CANCELED、REJECTED 及成交累计）与 `fills`（每笔成交的一方：角色、价格、数量、手续费）。

## 查看

```bash
ssh exchange 'curl -s localhost:9090/metrics' | grep -E '^market_|kafka_consumer_lag'   # 或经容器 wget
```

- 指标：`market_updates_published_total`、`market_update_publish_failures_total`、`matching_depth_published_total`、`matching_depth_publish_failures_total`、`kafka_consumer_lag{group="market-data"}`、`ws_pushed_total{channel}`（公共频道按类型计：ticker、depth、trades、candles）。
- 日志：`market state loaded`（启动加载的交易对数）、`market update push failed`、`depth export failed`（下一次会补上）。
- 数据：`SELECT * FROM market.symbols;`（每个交易对已应用到的 sequence 与最新价）、`SELECT interval, count(*) FROM market.candles GROUP BY 1;`。

## 合约：指数价、标记价与资金费率

需求 §11.7，实施计划 §7.3 任务 3；实现见 `internal/marketdata/domain/perpetual.go`（公式）与 `application/marks.go`（每秒一轮）。

- 合约（如 `BTC-USDT-PERP`）的 K 线、ticker、最近成交与深度和交易对一样，来自合约分片 derivatives-engine 的 `derivatives.trade.events` 与 `derivatives.market.depth`（见 [matching.md](matching.md#分片现货与合约)），同在上面的接口里，`/v1/market/tickers` 也包含合约。
- 每秒对每个未下线的合约：
  - **指数价**：`index_symbol`（如 `BTC-USDT`）各价源的最新现货价（参考行情，5 秒内的才算）按权重取中位数（两边权重正好各半时取两价平均），剔除偏离中位数超过 3% 的源后再取一次，8 位小数。可用源少于 `INDEX_MIN_SOURCES`（默认 2）时本轮没有指数价。平台自己的现货成交不算独立价源。价源权重 `INDEX_SOURCE_WEIGHTS`（如 `binance=1`，未列出为 1，0 表示停用）。**测试服只有币安一个源，配置为 1**；上线前按 §11.9 接入至少 3 个有授权的源。
  - **标记价**：`index × (1 + basis)`，`basis` 是合约盘口中间价相对指数的偏离 `(mid − index) / index` 的 30 秒 EMA（每秒一个样本，α = 2/31，保留 12 位小数），盘口缺一边时样本为 0；`basis` 限制在 ±1% 以内，8 位小数。服务启动时 EMA 从 0 开始（标记价等于指数价）。
  - **溢价指数样本**：按合约的冲击名义金额（`impact_notional`，测试服 10000 USDT）在盘口两边算平均成交价（冲击买价、冲击卖价），`premium = (max(0, 冲击买价 − index) − max(0, index − 冲击卖价)) / index`；深度不够冲击名义金额的一边记 0。
  - **预估资金费率**：本周期样本平均值 `P`，`rate = clamp(P + clamp(interest − P, ±0.05%), ±funding_cap)`，8 位小数；正值多头付空头。
- 资金费周期按合约的 `funding_interval_hours`（8 小时即 00:00、08:00、16:00 UTC）。样本累计在 `market.funding_periods` 里（每分钟保存一次，重启最多丢一分钟的样本）；周期结束后的第一次有标记价的计算把该周期**结算**：写入费率、平均溢价、利率、当时的标记价与指数价（`settled_at`），并发 `FundingRateUpdated{final=true}`。服务停机跨过结算点时，恢复后的第一轮补结算，用恢复时的标记价；整个周期都不在线则该周期没有记录，不收资金费。derivatives-service 按已结算的记录收付资金费（任务 6）。
- **降级**：一个合约连续 10 秒算不出标记价（价源不足），发 `risk.events` 的 `SystemDegraded{reason=INDEX_SOURCES}`（键为合约代码），合约交易进入只减仓，需人工解除（任务 7）；标记价恢复后发 `SystemRecovered`（仅通知）。不足 10 秒的断档（参考源重连）不降级，期间标记价停在最后一次的值。关掉开关 `market.reference_feed` 会让全部合约降级。
- 只能跑一个实例：周期样本在内存里累计，两个实例会重复采样。

接口与推送：

| 路径 / 频道 | 内容 |
|---|---|
| `GET /v1/market/{symbol}/mark-price` | 标记价、指数价（最后一次计算的值，从未算出时为 null）、预估资金费率、利率、下次结算时间、`degraded`、`updated_at` |
| `GET /v1/market/{symbol}/funding-rates?from=&to=&limit=` | 已结算的资金费率（新的在前，最多 1000 条）：结算时间、费率、标记价、指数价、平均溢价、利率、样本数 |
| `GET /internal/market/{symbol}/mark`（不经网关） | 以上加 `basis`、本周期平均溢价与样本数、各价源的价格、权重与是否采用（审计用；价源名称不对客户端公开，§11.9） |
| `mark-price:{symbol}` | 每秒一次：`mark_price`、`index_price`、`funding_rate`（预估）、`next_funding_time` |
| `funding:{symbol}` | `type=estimate`：预估费率变化时推送（不变时每 10 秒重推一次），新订阅者先收到最近一条；`type=settled`：周期结算时推一次，带结算标记价 |

Kafka：`market.candle.events` 上的 `MarkPriceUpdated`、`FundingRateUpdated`（键为合约）与 `IndexPriceUpdated`（键为指数的现货代码，带各价源）；都不进 ClickHouse，结算记录以 `market.funding_periods` 为准。

```bash
curl -s https://astras.vip/v1/market/BTC-USDT-PERP/mark-price | jq
ssh exchange 'sudo docker exec exchange-market-data-service-1 wget -qO- localhost:8090/internal/market/BTC-USDT-PERP/mark' | jq
```

```sql
-- 进行中的周期与最近的结算（PostgreSQL，schema market）
SELECT symbol, funding_time, samples, premium_sum / nullif(samples, 0) AS avg_premium FROM market.funding_periods WHERE settled_at IS NULL;
SELECT symbol, funding_time, funding_rate, mark_price, samples FROM market.funding_periods WHERE settled_at IS NOT NULL ORDER BY funding_time DESC LIMIT 10;
```

指标：`market_mark_age_seconds{symbol}`（-1 表示从未算出）、`market_index_sources{symbol}`（最近一次指数价用到的源数）、`market_contract_degraded{symbol}`、`market_funding_settled_total`；告警 `ContractDegraded`、`ContractIndexSourceMissing`。

## 参考 K 线（`market.reference_kline`，测试环境）

用户决定（2026-09-30）：测试环境成交太少，平台自己的 K 线几乎不动，图表一律显示币安的 K 线。开关 `market.reference_kline` 按交易对生效（还需要 `market.reference_feed` 开着）：

- 哪些交易对：参考行情跟踪的交易对（`REFERENCE_SYMBOLS`，测试服 BTC-USDT、ETH-USDT）用自己的参考数据，合约用它的指数交易对（BTC-USDT-PERP → BTC-USDT）。没有参考数据的交易对（ETH-BTC）照常显示平台 K 线。
- 历史：`GET /v1/market/{symbol}/candles` 改为向币安取同周期的 K 线（`/api/v3/klines`，周期名与对齐方式和平台一致），同样的请求 5 秒内走缓存，已结束的历史页缓存 1 分钟；取不到时返回 `COMMON_UNAVAILABLE`。
- 实时：参考行情收到的每条 1 分钟推送，在服务里累加成各周期的当前 K 线（开高低收、成交量、笔数），随每 500 毫秒一次的推送发到 `market.candle.events`，前端的 `candles:{symbol}:{interval}` 频道和平台 K 线一样收到；服务启动后第一次遇到进行到一半的周期，先向币安取这一根的当前值再累加。这些交易对不再推送平台自己的 K 线；ticker、成交记录、深度仍是平台的数据。
- 测试服设置：`exchangectl flags set market.reference_kline --on --deny-symbols ETH-BTC --reason "..."`。ETH-BTC 要保留平台 K 线，因为端到端 `marketdata.sh` 在它上面成交后检查平台 K 线。
- 数据授权：参考数据给客户端看同样受 §11.9 限制，只在测试环境用；上线前关掉开关，或换成有授权的数据源。

## 故障与处理

| 情况 | 表现 | 处理 |
|---|---|---|
| 合约降级（`ContractDegraded`） | `mark-price` 的 `degraded` 为 true，`updated_at` 停住；`market_index_sources` 为 0 | 查参考行情（`market_reference_age_seconds`、开关 `market.reference_feed`、`reference feed failed` 日志，见 [market-maker.md](market-maker.md)）；恢复后确认标记价正常，再按合约服务手册人工解除只减仓 |
| 市场服务重启 | 深度最多 10 秒为空；K 线、ticker 从库恢复 | 自动 |
| PostgreSQL 不可用 | 成交批次写库失败，消费者退避重试，积压上升 | 恢复后自动重载并继续；成交量不会重复累加 |
| 引擎重启或切换 | 深度更新暂停，恢复后继续（sequence 不回退） | 自动；客户端 `prev_seq` 不连续时重新订阅 |
| 需要重建 K 线 | — | 停服务，清空 `market.candles`、`market.trades`、`market.symbols`，删除消费组 `market-data` 的位移后重启，会从 `trade.events`（保留 30 天）重算 |

端到端检查：`scripts/e2e/marketdata.sh`（REST 与 WebSocket 围绕一笔成交的全部推送）。
