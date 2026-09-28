# 参考行情与做市运维

需求 §5.11、§11.9、§11.10；实现见 `internal/marketdata`（参考行情，`adapters/binance`、`application/reference.go`）与 `internal/marketmaker`（market-maker，运维端口 9091）。

## 参考行情

- 来源：币安公开行情（`data-api.binance.vision` 的 REST 与 `data-stream.binance.vision` 的 WebSocket）。**币安条款禁止未经授权把它用于交易服务**：只能在测试环境用，上线前按 §11.9 换成有授权的数据源并记 ADR；代码里来源是接口（`ports.ReferenceSource`），可以加源。
- market-data-service 跟踪 `REFERENCE_SYMBOLS`（测试服为 BTC-USDT）的 1m K 线，功能开关 `market.reference_feed` 打开时运行：每次连接先用 REST 补齐最新一根以来的 K 线（最多一天，请求间隔 200 毫秒），再订阅 `kline_1m` 流；断线按 1 秒起、最长 1 分钟退避重连；K 线按（来源, 交易对, 开盘时间）去重写入 `market.reference_candles`，保留 7 天。开关关掉时连接断开，内存里的参考价作废。
- 参考价只在内网：`GET /internal/market/{symbol}/reference`（`{symbol, source, price, updated_at, fresh}`，5 秒内的算新鲜）。网关不转发 `/internal`，客户端看不到来源与参考价（§11.9：来源的商标与文案要法务确认后才能出现在客户端）。
- 用途：交易服务在交易对 5 分钟内没有成交时用它作价格带与市价保护价的锚点（这样长时间无成交后的旧成交价不会把做市报价挡在价格带外）；做市机器人围绕它报价。
- 指标：`market_reference_age_seconds{symbol}`（没有参考价时 -1）、`market_reference_updates_total`、`market_reference_errors_total`；告警 `MarketReferenceStale`（超过 30 秒没更新）。

## 做市机器人

- 做市账户是一个普通用户，走和其他用户一样的下单接口与账本（§11.10），只是交易服务把 `MARKET_MAKER_USER_IDS` 里的账户手续费设为 0。它直接调各服务的内网 REST（`X-User-Id` 为做市账户，与网关转发时一样）。
- 按 `MARKET_MAKER_SYMBOLS`（测试服为 BTC-USDT）逐对报价，每 500 毫秒一轮，参数默认按 §11.10：
  - 点差 0.2%（买一、卖一各离参考价 0.1%），每侧 5 档，档距 0.1%，每档 0.002 BTC；
  - 参考价变动不到 0.05% 不改价，只补被成交掉的档；
  - 库存上限 5 BTC（到了就不挂买单），单边偏斜上限 80%（BTC 市值占比超过 80% 不挂买单，低于 20% 不挂卖单）；
  - 可用余额不够的档先不挂，撤掉的单子解冻后下一轮补上。
  - 可以用 `MARKET_MAKER_PARAMS_FILE`（`domain.Params` 的 JSON 数组）按交易对覆盖。
- 撤掉全部报价的情况：开关 `market.maker` 对该交易对关闭、交易对不是 TRADING、参考价超过 5 秒没更新、进程退出。
- 指标：`mm_quoting{symbol}`（1 在报价）、`mm_orders_placed_total{symbol,side}`、`mm_orders_canceled_total`、`mm_errors_total`、`mm_inventory{asset}`。日志：`market maker quoting`、`market maker pulled its quotes`（带原因）。

## 测试服设置（一次性）

```bash
# 1. 注册做市账户（与端到端脚本同样的注册流程），记下 user_id
# 2. 注入模拟资金（需要 ledger.manual_adjustment）
exchangectl ledger adjust --user <mm_user_id> --asset BTC --amount 1 --reason "market maker inventory"
exchangectl ledger adjust --user <mm_user_id> --asset USDT --amount 100000 --reason "market maker inventory"
# 3. 服务器 /opt/exchange/infra/apps.env 加上（不是密钥，但随环境而定）
#    MARKET_MAKER_USER_ID=<mm_user_id>
#    MARKET_MAKER_USER_IDS=<mm_user_id>
# 4. 打开开关
exchangectl flags set market.reference_feed --on --reason "test environment reference prices"
exchangectl flags set market.maker --on --allow-symbols BTC-USDT --reason "quote BTC-USDT"
```

端到端脚本改在没有做市的 ETH-BTC 上成交（BTC-USDT 的做市报价会吃掉固定价格的单子），ETH-BTC 在测试数据里的价格带为 100%；ETH-USDT 保持 PREPARE，用作"未开放交易对"的检查。
