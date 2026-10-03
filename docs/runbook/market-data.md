# 平台行情运维

需求 §5.11、§7.3、§11.8；实现见 `internal/marketdata`（market-data-service，HTTP 8090、运维 9090，schema `market`）、撮合引擎的深度导出（`internal/matching/application/depth.go`）与网关的 WebSocket 公共频道（`internal/gateway/wsmarket.go`）。契约 `api/openapi/market.yaml`、`api/proto/exchange/market/v1`。

## 数据流

```text
matching-engine ──market.depth.internal（每 100 ms 变化的订单簿前 200 档，每 10 s 全量）──> market-data-service
币安盘口与成交（depth@100ms、aggTrade）──────────────────────────────────────────────> market-data-service
market-data-service ──market.depth（公共盘口：快照 + 增量，1 秒心跳）──┬─> api-gateway（depth: 频道）
                    ──market.trades（公共成交）─────────────────────┤  └─> market-maker（HOUSE 的参考簿，见 market-maker.md）
                                                                   └─> api-gateway（trades: 频道）
trade.events ──> market-data-service ──market.candle.events（CandleUpdated/Closed、TickerUpdated，每 500 ms）──> api-gateway（candles:、ticker: 频道）
trade.events ──> api-gateway（fills 私有频道）；order.events ──> api-gateway（orders 私有频道）
derivatives-engine ──derivatives.market.depth.internal / derivatives.trade.events──> 同上（合约的公共盘口在 derivatives.market.depth；合约的 fills 由 derivatives-service 推）
参考行情（币安）──> market-data-service ──每秒：MarkPriceUpdated、IndexPriceUpdated、FundingRateUpdated──> api-gateway（mark-price:、funding: 频道）
                                       └─标记价 10 秒算不出──> risk.events（SystemDegraded）
```

- 公共盘口与成交由 market-data-service 统一发布（ADR-0015）：显示参考市场的交易对发币安的盘口与成交（见下文「参考盘口与成交」），其余交易对转发引擎自己的深度（`*.depth.internal`）与平台成交。
- 深度与成交主题都是派生状态：从内存直接发，不走 outbox，丢一份由下一份补上；只保留 1 小时，没有 retry/dlq，不进 ClickHouse。市场服务、网关与 market-maker 都从主题末尾读（`kafka.Tail`，不提交位移），启动后最多 10 秒拿到全部订单簿。
- `market.candle.events` 同样由市场服务直接发布，也不进 ClickHouse。平盘分钟（见下文「规则」）另经 outbox 发到业务主题 `market.candle.flats`（保留 7 天，有 retry/dlq），analytics 把它写进 `candles_1m`。
- 市场服务的消费组 `market-data` 读 `trade.events`：按交易对的 sequence 幂等（已应用的跳过），一批成交在一个事务里写 K 线、最近成交和进度；写库失败则内存状态作废，重新从库加载后重投。库里的数据都能从 `trade.events` 重建。

## 规则

- K 线周期 `1m 3m 5m 15m 30m 1h 2h 4h 6h 12h 1d 1w 1M`，UTC 对齐，周从周一开始。只存有成交的区间；查询时无成交区间用上一根收盘价补平（成交量 0），第一笔成交之前的区间不返回。
- **平盘分钟**（协调会话 2026-10-04 代用户决定：ASTRA 的 1 分钟 K 线断续）：图表用平台自己成交的品种（没有显示参考市场 K 线的交易对与合约，测试服即 ASTRA-USDT 与 ASTRA-USDT-PERP；`market.reference_kline` 对某个跟随币安的交易对关掉时它也算），一分钟结束 10 秒（`FlatGrace`，等迟到的成交）后仍没有成交，就存一根平盘的 1m K 线：开高低收都是上一根收盘价，成交量与笔数 0（"flat minute runner"，每秒一次，指标 `market_flat_minutes_total`）。更长的周期把它当作一笔量为 0、价格为上一收盘价的成交：以平盘分钟开头的 5m/15m… 开盘价就是上一收盘价，高低价算上它，与 ClickHouse `candles()` 由 1m 汇总的结果一致。推送照常：这根平盘分钟像有成交的一样推一次 `CandleClosed`；同一事务里经 outbox 发 `CandleClosed` 到 `market.candle.flats`，analytics 把它写进 `candles_1m`（`updated_at` 取分钟开始，那一分钟真有成交时由成交算出的行覆盖它）。只向前生效：重启后最多补最近 1 小时（`FlatCatchUp`），之前的历史不回填（查询照旧补平）；从没成交过的品种没有。迟于 `FlatGrace` 才到的成交在这里计入下一分钟（K 线不重开），在 ClickHouse 计入它自己的分钟。
- 当前 K 线变化时每 500 ms 推 `CandleUpdated`；区间结束时推一次 `CandleClosed`，新区间在有成交前推一根平盘 K 线。ticker 变化时推 `TickerUpdated`，没变化也每 15 秒重推一次（`TickerHeartbeat`）：网关从 `market.candle.events` 的末尾读，没有参考市场替它推送的冷清交易对（如 ASTRA-USDT）否则要等下一笔成交才出现在 `tickers` 频道里。
- 24 小时 ticker 按分钟计算：窗口是当前分钟加前 1439 分钟；`open` 是窗口前最后一笔成交价（之前没有成交时取窗口内第一笔），`change = (last − open) / open`（小数，8 位）；窗口内没有成交时 `last` 沿用上一笔、成交量 0；从未成交的交易对价格为 null。
- 最新成交价同时是下单价格带与市价保护价的锚点：交易服务从自己的 `fills` 取（缓存 1 秒），5 分钟内没有成交时用内网参考价 `GET /internal/market/{symbol}/reference`（顺序见 [trading.md](trading.md)）。不跟随参考市场的交易对（平台币 ASTRA-USDT）的"参考价"是平台自己的市场（`source: platform`）：与它的永续指数同一算法的价格（见下文"指数价"），没有时盘口中价（买一、卖一各值 100 以上），再没有时 market-sim 30 秒内上报的目标价（`source: simulation`）；market-sim 每 5 秒经 `PUT /internal/market/{symbol}/simulated-price`（`{"price": "…"}`，204；只接受已上市、不跟随参考市场的交易对，否则 409 `MARKET_NOT_SIMULATED`）上报，这也是它的心跳：开关 `sim.halt_on_loss` 打开时，1 分钟没有心跳的交易对与它的永续由本服务置 `HALT`，记在 `market.sim_halts`，心跳恢复 30 秒后放开（`SimGuard`，见 [market-sim.md](market-sim.md#心跳与停牌设计-9a5)）。这样交易对很久没有成交、盘口又空时，价格带不会锁在一笔旧成交上（ASTRA 设计 §4，见 [market-sim.md](market-sim.md#价格带不锁死市场设计-4)）。参考行情（币安公开数据，仅测试环境）见下文，HOUSE 虚拟流动性见 [market-maker.md](market-maker.md)；端到端脚本 `marketdata.sh` 在不跟随参考市场的 ETH-BTC 上成交。

## 接口

REST（经网关，无需登录，`Cache-Control: public, max-age=1`）：

| 路径 | 内容 |
|---|---|
| `GET /v1/market/tickers` | 所有上架交易对的 ticker（带 `rank`，基础资产的市值排名） |
| `GET /v1/market/summary?limit=` | 首页概览：TRADING 状态、以 USDT 计价、有价格的交易对的涨幅榜、跌幅榜、成交额榜（各 `limit` 条，默认 5，最多 20） |
| `GET /v1/market/sparklines?symbols=A,B&range=7d\|24h` | 行情列表的走势，一次最多 60 个交易对（阶段 4 B7）：取图表同源（参考市场或平台自己）的最近 168 根 1 小时 K 线收盘价，`7d` 均匀抽成 56 个点、`24h` 取最后 24 个；每个交易对的收盘价缓存 5 分钟，取不到的交易对不出现在结果里。两个用户站 10 ms 内要的行合成一个请求 |
| `GET /v1/market/{symbol}/ticker` | 一个交易对的 ticker；未上架或已下线 404 |
| `GET /v1/market/{symbol}/depth?limit=` | 深度 `[价格, 数量]`，最多 200 档，带公共盘口的 sequence；显示参考市场时是币安的盘口 |
| `GET /v1/market/{symbol}/trades?limit=` | 最近成交（最多 100 条，新的在前），带交易对内编号 `trade_number`；显示参考市场时是币安的成交 |
| `GET /v1/market/{symbol}/candles?interval=&from=&to=&limit=` | K 线（最多 1000 根，时间 RFC 3339）；返回 `to` 之前开盘的最近 `limit` 根。向前翻页：把已拿到的最早一根的 `open_time` 作为 `to`，正好再拿 `limit` 根、不重叠 |

WebSocket `wss://astras.vip/v1/ws`：

- 公共频道无需 `auth`：`ticker:{symbol}`、`depth:{symbol}`、`trades:{symbol}`、`candles:{symbol}:{interval}`（交易对与合约），合约另有 `mark-price:{symbol}`、`funding:{symbol}`（见下节）。消息 `{"channel": ..., "type": ..., "data": ...}`；订阅 ticker、K 线时先收到最近一条。
- `tickers`：一个订阅拿到全部交易对与合约的 ticker（行情列表、首页、跑马灯用，只占 50 个订阅名额中的 1 个）。订阅后先收 `{"channel":"tickers","type":"snapshot","data":[...全部 ticker，按代码排序]}`，之后网关每秒把这一秒内变化过的交易对合成一条 `{"type":"update","data":[...]}` 发出（每个交易对只带最新一条）；没有变化就不发。
- 深度：订阅后先收 `{"type":"snapshot","seq":n,"data":{"bids":[...],"asks":[...]}}`，之后是 `{"type":"update","seq":n+1,"prev_seq":n,"data":{变化的档位}}`，数量 `"0"` 表示该档消失。`seq` 是本网关实例的计数，`prev_seq` 对不上就重新订阅；每 30 秒重发一次快照。网关按 `market.depth` 消息的 `prev_sequence` 应用增量，接不上的增量丢掉、等下一个快照（最多 10 秒）。
- 私有频道（需 `auth`，带每用户 `seq`，可用 `last_seq` 补发）新增 `orders`（订单状态变化：NEW、OPEN、PARTIALLY_FILLED、FILLED、CANCELED、REJECTED 及成交累计）与 `fills`（每笔成交的一方：角色、价格、数量、手续费）。

## 查看

```bash
ssh exchange 'curl -s localhost:9090/metrics' | grep -E '^market_|kafka_consumer_lag'   # 或经容器 wget
```

- 指标：`market_updates_published_total`、`market_update_publish_failures_total`、`market_public_messages_total{kind,source}`（公共盘口与成交消息，`source` 为 `reference` 或 `platform`）、`matching_depth_published_total`、`matching_depth_publish_failures_total`、`kafka_consumer_lag{group="market-data"}`、`ws_pushed_total{channel}`（公共频道按类型计：ticker、depth、trades、candles）；参考盘口的指标见下文。
- 日志：`market state loaded`（启动加载的交易对数）、`market update push failed`、`depth export failed`（下一次会补上）。
- 数据：`SELECT * FROM market.symbols;`（每个交易对已应用到的 sequence 与最新价）、`SELECT interval, count(*) FROM market.candles GROUP BY 1;`。

## 合约：指数价、标记价与资金费率

需求 §11.7，实施计划 §7.3 任务 3；实现见 `internal/marketdata/domain/perpetual.go`（公式）与 `application/marks.go`（每秒一轮）。

- 合约（如 `BTC-USDT-PERP`）的 K 线、ticker、最近成交与深度和交易对一样，来自合约分片 derivatives-engine 的 `derivatives.trade.events` 与 `derivatives.market.depth`（见 [matching.md](matching.md#分片现货与合约)），同在上面的接口里，`/v1/market/tickers` 也包含合约。
- 每秒对每个未下线的合约：
  - **指数价**：`index_symbol`（如 `BTC-USDT`）各价源的最新现货价（参考行情，5 秒内的才算）按权重取中位数（两边权重正好各半时取两价平均），剔除偏离中位数超过 3% 的源后再取一次，8 位小数。可用源少于 `INDEX_MIN_SOURCES`（默认 2）时本轮没有指数价。平台自己的现货成交不算独立价源。价源权重 `INDEX_SOURCE_WEIGHTS`（如 `binance=1`，未列出为 1，0 表示停用）。**测试服只有币安一个源，配置为 1**；上线前按 §11.9 接入至少 3 个有授权的源。指数交易对不跟随参考市场时（平台币 ASTRA-USDT，ASTRA 设计 §5.2 与 A4 审查后的澄清；须是已读到的交易对列表里没有 `reference_symbol` 的交易对，列表还没读到时一律当作跟随，绝不改用平台盘口）唯一的价源是平台自己的市场（`platform`，一个源就够，不受 `INDEX_MIN_SOURCES` 限制），不用裸中价：取最近 60 秒的时间加权成交价（每个成交价按它作为最新价的时长加权，窗口开头沿用窗口前最后一笔），盘口中价只在买一、卖一各值 100（计价币）以上且距最近成交不超过 1% 时才与它平均；60 秒内没有成交时，合格的中价单独顶上（最近成交须在 5 分钟内），都没有时本轮没有指数价，10 秒后合约降级为只减仓；跟随参考市场的指数交易对从不改用平台盘口（那是 HOUSE 对参考盘口的复制）。
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

## 参考行情：跟随哪些交易对（ADR-0010）

- 交易对表的 `reference_symbol`（币安符号，如 `BTCUSDT`）决定是否跟随：有它的交易对都跟随，没有的（测试服只有平台币 ASTRA-USDT；ETH-BTC 自 B4 起跟随 ETHBTC）始终显示平台数据，与 `market.reference_*` 开关怎么设无关：ticker 与 24 小时统计、K 线（REST 与频道）、盘口、成交、走势图、`tickers` 频道与 `/v1/market/summary` 都来自平台自己的 `trade.events` 与引擎盘口（2026-10-02 核对，单元测试覆盖）。`reference_multiplier` 是价格倍数（1000 倍计价的币，如 `1000PEPE-USDT` ↔ `PEPEUSDT`，倍数 1000）：适配器把币安的价格乘以倍数、数量除以倍数，成交额不变，之后一切都按平台的代码与单位处理。两个字段在 `deploy/instruments/test.json` 里维护，部署时幂等同步（见 [instruments.md](instruments.md)）。
- 行情服务每分钟重读一次映射，跟随的交易对变了就重连。每次连接先建流（每个交易对 `kline_1m` 与 `ticker` 两条，一个组合连接），同时用 REST 取一次全部 24h ticker、补齐 1 分钟 K 线（从库里最新一根到建流那一分钟，最多一天）；REST 请求间隔 200 毫秒，币安回 429/418 时按 `Retry-After` 暂停全部请求。
- 旧的环境变量 `REFERENCE_SYMBOLS` 已去掉。

## 参考 ticker（`market.reference_ticker`）

开关按交易对生效（还需要 `market.reference_feed` 开着）：打开时 `GET /v1/market/tickers`、`/{symbol}/ticker`、`/summary` 与 `ticker:`、`tickers` 频道的最新价、24h 开高低、成交量、成交额、笔数、买一卖一都来自币安的 24h ticker（`<symbol>@ticker`，每秒一次），`updated_at` 是币安计算它的时间；合约显示它的指数交易对的 ticker。币安流中断时继续显示最后一条（`updated_at` 停住），客户端据此在 30 秒后显示"行情连接中断"；从未收到过参考 ticker 时显示平台自己的。开关关掉后下一次推送（500 毫秒内）恢复平台 ticker。

## 行情中断保护（`market.halt_on_feed_loss`）

- 状态（`GET /internal/market/feed`，不经网关，后台概览用）：`OFF`（`market.reference_feed` 关着或没有跟随的交易对）、`OK`（30 秒内收到过数据）、`DELAYED`（30 秒到 5 分钟没有数据）、`DOWN`（5 分钟以上）。服务启动或开关刚打开时从那一刻起算。
- `DOWN` 且开关打开：把跟随币安、处于 TRADING 的交易对置为 HALT（操作人 `market-data-service`，原因 `reference feed lost`），先记入 `market.feed_halts` 再改状态。数据恢复并持续 30 秒后，只把 `feed_halts` 里的交易对从 HALT 改回 TRADING；期间被运营改成别的状态的交易对只删记录、不动它。两个开关任一关掉时也会恢复。合约不受影响（它们有自己的只减仓降级）。
- 指标：`market_reference_feed_state{state}`（当前状态为 1）、`market_feed_halted_pairs`；告警 `MarketFeedHalted`。
- 参考符号核对（管理后台 C3，不经网关）：`GET /internal/market/reference-symbols/{symbol}` 返回 `{symbol, spot, futures}`，向币安的 `/api/v3/ticker/price` 与 `/fapi/v1/ticker/price` 各问一次（与其它请求共用 200 毫秒的间隔）。后台在交易对用上新参考符号前调用：币安不认识的符号会让按批读取的全部交易对的 ticker 失败。

## 参考 K 线（`market.reference_kline`，测试环境）

用户决定（2026-09-30）：测试环境成交太少，平台自己的 K 线几乎不动，图表一律显示币安的 K 线。开关 `market.reference_kline` 按交易对生效（还需要 `market.reference_feed` 开着）：

- 哪些交易对：跟随币安的交易对（有 `reference_symbol` 的，测试服是全部交易对：USDT 交易对与跟随 ETHBTC 的 ETH-BTC）用自己的参考数据，合约用它的指数交易对（BTC-USDT-PERP → BTC-USDT）。没有参考数据的交易对照常显示平台 K 线。
- 历史：`GET /v1/market/{symbol}/candles` 改为向币安取同周期的 K 线（`/api/v3/klines`，周期名与对齐方式和平台一致），同样的请求 5 秒内走缓存，已结束的历史页缓存 1 分钟；取不到时返回 `COMMON_UNAVAILABLE`。
- 实时：参考行情收到的每条 1 分钟推送，在服务里累加成各周期的当前 K 线（开高低收、成交量、笔数），随每 500 毫秒一次的推送发到 `market.candle.events`，前端的 `candles:{symbol}:{interval}` 频道和平台 K 线一样收到；服务启动后第一次遇到进行到一半的周期，先向币安取这一根的当前值再累加。这些交易对不再推送平台自己的 K 线；ticker 见上一节，盘口与成交见下一节。
- 测试服设置：`exchangectl flags set market.reference_kline --on --deny-symbols ETH-BTC --reason "..."`，`market.reference_ticker`、`market.halt_on_feed_loss` 同样打开。ETH-BTC 没有 `reference_symbol`，本来就显示平台数据；端到端 `marketdata.sh` 在它上面成交后检查平台 K 线与 ticker。
- 数据授权：参考数据给客户端看同样受 §11.9 限制，只在测试环境用；上线前关掉开关，或换成有授权的数据源。

## 参考盘口与成交（`market.reference_depth`，阶段 4 B4）

开关按交易对生效（还需要 `market.reference_feed` 开着；测试服对全部跟随的交易对打开，见 `scripts/ops/house.sh flags`）。打开时该交易对的公共盘口（REST 深度、`depth:` 频道）与公共成交（REST trades、`trades:` 频道）都是币安的；HOUSE 按同一份盘口提供流动性（[market-maker.md](market-maker.md)），所以用户看到的就是能成交的价格。

- 本地盘口（`internal/marketdata/domain/localbook.go`，币安"如何正确在本地维护一个订单簿"的做法）：每个组合连接最多 25 个交易对（`<symbol>@depth@100ms` 与 `@aggTrade`），先缓存增量，再逐个用 REST 取快照（现货 `/api/v3/depth?limit=1000`，合约 `/fapi/v1/depth`），丢掉快照之前的增量；现货按 `U`/`u`、合约按 `pu` 检查连续性，断了就重新取快照（`market_reference_book_resyncs_total`）。1000 倍计价的币价格乘、数量除以倍数。
- 可用的条件：已同步，且它的连接 5 秒内收到过消息（按连接算，冷门币盘口不变也不会被当成断流）。不可用时该交易对退回平台自己的盘口与成交（转发引擎的 `market.depth.internal`），恢复后重新发快照。
- 发布：每 100 毫秒一轮，变化的交易对发 `DepthUpdate`（与上次发出的前 200 档比较的差异，带 `prev_sequence`），每 10 秒与刚开始显示时发 `DepthSnapshot`，没有变化时每秒一条空的 `DepthUpdate` 作心跳（`taken_at` 是连接最后收到消息的时间）。公共 sequence 按交易对递增，起点是服务启动时刻（微秒），重启后不会回退；不显示参考市场的交易对每次转发引擎快照也占一个 sequence。成交按批发 `TradesPrinted`（`market.trades`）；平台自己的成交只转发这一批里新应用的（按 sequence 判断），`trade.events` 重投时不会重复出现在成交列表里。REST 的最近成交在启动时先从币安取一次。
- 合约的盘口与成交用币安 U 本位合约的同名符号（`fapi`/`fstream`），标记价的盘口中间价也用它。
- 指标：`market_reference_book_age_seconds{symbol}`（距上次变化的秒数，未同步为 -1）、`market_reference_book_resyncs_total`、`market_reference_book_stream_failures_total`；告警 `ReferenceBookStale`（不同步或 30 秒没变，持续 2 分钟）。
- 日志：`reference book stream failed`（带交易对数，按 1 秒起、最长 1 分钟退避重连）、`reference book snapshot not loaded`、`reference books: followed symbols changed`。

## 故障与处理

| 情况 | 表现 | 处理 |
|---|---|---|
| 币安行情中断（`MarketFeedHalted`） | `/internal/market/feed` 为 `DOWN`，跟随的交易对被置 HALT；ticker 的 `updated_at` 停住 | 查 `reference feed failed` 日志与出网；恢复后 30 秒自动放开；要提前放开就关掉 `market.halt_on_feed_loss` |
| 币安盘口断流（`ReferenceBookStale`） | 5 秒后该交易对的公共盘口退回平台自己的，HOUSE 发空簿、不再成交（`market_house_active` 为 0） | 自动重连并重新取快照；`scripts/fault/reference-outage.sh` 演练 |
| 合约降级（`ContractDegraded`） | `mark-price` 的 `degraded` 为 true，`updated_at` 停住；`market_index_sources` 为 0 | 查参考行情（`market_reference_age_seconds`、开关 `market.reference_feed`、`reference feed failed` 日志，见 [market-maker.md](market-maker.md)）；恢复后确认标记价正常，再按合约服务手册人工解除只减仓 |
| 市场服务重启 | 深度最多 10 秒为空；K 线、ticker 从库恢复 | 自动 |
| PostgreSQL 不可用 | 成交批次写库失败，消费者退避重试，积压上升 | 恢复后自动重载并继续；成交量不会重复累加 |
| 引擎重启或切换 | 深度更新暂停，恢复后继续（sequence 不回退） | 自动；客户端 `prev_seq` 不连续时重新订阅 |
| 需要重建 K 线 | — | 停服务，清空 `market.candles`、`market.trades`、`market.symbols`，删除消费组 `market-data` 的位移后重启，会从 `trade.events`（保留 30 天）重算 |

端到端检查：`scripts/e2e/marketdata.sh`（参考行情的映射、ticker、概览、K 线翻页与 `tickers` 频道；REST 与 WebSocket 围绕一笔成交的全部推送）。
