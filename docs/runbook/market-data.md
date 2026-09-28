# 平台行情运维

需求 §5.11、§7.3、§11.8；实现见 `internal/marketdata`（market-data-service，HTTP 8090、运维 9090，schema `market`）、撮合引擎的深度导出（`internal/matching/application/depth.go`）与网关的 WebSocket 公共频道（`internal/gateway/wsmarket.go`）。契约 `api/openapi/market.yaml`、`api/proto/exchange/market/v1`。

## 数据流

```text
matching-engine ──market.depth（每 100 ms 变化的订单簿前 200 档，每 10 s 全量）──┬─> market-data-service（REST 深度、ticker 买一卖一）
                                                                                └─> api-gateway（depth: 频道）
trade.events ──> market-data-service ──market.candle.events（CandleUpdated/Closed、TickerUpdated，每 500 ms）──> api-gateway（candles:、ticker: 频道）
trade.events ──> api-gateway（trades: 频道、fills 私有频道）；order.events ──> api-gateway（orders 私有频道）
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

- 公共频道无需 `auth`：`ticker:{symbol}`、`depth:{symbol}`、`trades:{symbol}`、`candles:{symbol}:{interval}`。消息 `{"channel": ..., "type": ..., "data": ...}`；订阅 ticker、K 线时先收到最近一条。
- 深度：订阅后先收 `{"type":"snapshot","seq":n,"data":{"bids":[...],"asks":[...]}}`，之后是 `{"type":"update","seq":n+1,"prev_seq":n,"data":{变化的档位}}`，数量 `"0"` 表示该档消失。`seq` 是本网关实例的计数，`prev_seq` 对不上就重新订阅；每 30 秒重发一次快照。
- 私有频道（需 `auth`，带每用户 `seq`，可用 `last_seq` 补发）新增 `orders`（订单状态变化：NEW、OPEN、PARTIALLY_FILLED、FILLED、CANCELED、REJECTED 及成交累计）与 `fills`（每笔成交的一方：角色、价格、数量、手续费）。

## 查看

```bash
ssh exchange 'curl -s localhost:9090/metrics' | grep -E '^market_|kafka_consumer_lag'   # 或经容器 wget
```

- 指标：`market_updates_published_total`、`market_update_publish_failures_total`、`matching_depth_published_total`、`matching_depth_publish_failures_total`、`kafka_consumer_lag{group="market-data"}`、`ws_pushed_total{channel}`（公共频道按类型计：ticker、depth、trades、candles）。
- 日志：`market state loaded`（启动加载的交易对数）、`market update push failed`、`depth export failed`（下一次会补上）。
- 数据：`SELECT * FROM market.symbols;`（每个交易对已应用到的 sequence 与最新价）、`SELECT interval, count(*) FROM market.candles GROUP BY 1;`。

## 故障与处理

| 情况 | 表现 | 处理 |
|---|---|---|
| 市场服务重启 | 深度最多 10 秒为空；K 线、ticker 从库恢复 | 自动 |
| PostgreSQL 不可用 | 成交批次写库失败，消费者退避重试，积压上升 | 恢复后自动重载并继续；成交量不会重复累加 |
| 引擎重启或切换 | 深度更新暂停，恢复后继续（sequence 不回退） | 自动；客户端 `prev_seq` 不连续时重新订阅 |
| 需要重建 K 线 | — | 停服务，清空 `market.candles`、`market.trades`、`market.symbols`，删除消费组 `market-data` 的位移后重启，会从 `trade.events`（保留 30 天）重算 |

端到端检查：`scripts/e2e/marketdata.sh`（REST 与 WebSocket 围绕一笔成交的全部推送）。
