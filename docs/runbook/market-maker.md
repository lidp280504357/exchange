# 参考行情与做市运维

需求 §5.11、§11.9、§11.10；实现见 `internal/marketdata`（参考行情，`adapters/binance`、`application/reference.go`）与 `internal/marketmaker`（market-maker，运维端口 9091）。

## 参考行情

- 来源：币安公开行情（`data-api.binance.vision` 的 REST 与 `data-stream.binance.vision` 的 WebSocket）。**币安条款禁止未经授权把它用于交易服务**：只能在测试环境用，上线前按 §11.9 换成有授权的数据源并记 ADR；代码里来源是接口（`ports.ReferenceSource`），可以加源。
- market-data-service 跟随交易对表里设了 `reference_symbol` 的交易对（测试服 BTC-USDT、ETH-USDT，见 [market-data.md](market-data.md#参考行情跟随哪些交易对adr-0010)），功能开关 `market.reference_feed` 打开时运行：每次连接先订阅 `kline_1m` 与 `ticker` 流，同时用 REST 补齐最新一根以来的 1m K 线（最多一天，请求间隔 200 毫秒，429 时按 `Retry-After` 暂停）；参考价取最新的 ticker 或 K 线收盘价；断线按 1 秒起、最长 1 分钟退避重连；K 线按（来源, 交易对, 开盘时间）去重写入 `market.reference_candles`，保留 7 天。开关关掉时连接断开，内存里的参考价作废。
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
- 指标：`mm_quoting{symbol}`（1 在报价）、`mm_orders_placed_total{symbol,side}`、`mm_orders_canceled_total`、`mm_errors_total`、`mm_inventory{asset}`、`mm_position{symbol}`（合约净仓位）。日志：`market maker quoting`、`market maker pulled its quotes`（带原因）。

## 合约做市

需求 §11.10 最后一条："合约交易对的流动性由同一机器人以标记价为中心报价"。同一个 market-maker 进程、同一个做市账户：

- 按 `MARKET_MAKER_CONTRACTS`（测试服为 BTC-USDT-PERP）逐个合约报价，价格中心是 market-data-service 的标记价（`/v1/market/{symbol}/mark-price`，未降级且 5 秒内更新才算新鲜），档位与点差规则同现货；下单走 derivatives-service 的 `/v1/derivatives/orders`（`X-User-Id` 为做市账户，GTC 限价单），derivatives-service 按 `MARKET_MAKER_USER_IDS` 免手续费。
- 库存按做市账户在该合约的净仓位算：多头到上限不挂买单、空头到上限不挂卖单；合约默认上限 0.5（基础资产，`domain.ContractDefaults`），其余参数同 §11.10 默认，可以用 `MARKET_MAKER_PARAMS_FILE` 覆盖。做市不对冲，仓位靠用户成交自然变化；保证金用做市账户合约账户里的 USDT（默认 20 倍全仓），保证金不够、超出风险限额或价格偏出价格带的档位本轮跳过、下轮再试。
- 撤掉该合约全部报价的情况：开关 `market.maker` 不允许该合约、合约不是 TRADING、标记价不新鲜、合约只减仓或 `derivatives.trading` 关闭（下单被拒 `DERIV_REDUCE_ONLY_MODE`、`DERIV_MARK_PRICE_UNAVAILABLE`、`DERIV_DISABLED` 等，之后每轮试一次，恢复后重新报价）、进程退出。
- "无足够流动性的合约不得开放开仓"由运维保证：只在做市报价的合约上开放交易（合约状态与开关）；代码不强制，端到端脚本 `derivatives.sh` 就在没有做市的 ETH-USDT-PERP 上由两个用户对敲。

测试服一次性设置（在上面现货设置之后）：

```bash
# 1. 从做市账户的现货转 20000 USDT 到合约账户（在做市容器里以做市账户调用账本内网接口）
ssh exchange 'cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T market-maker sh -c '"'"'wget -q -O - --header "X-User-Id: $MARKET_MAKER_USER_ID" --header "Content-Type: application/json" --header "Idempotency-Key: mm-futures-1" --post-data "{\"asset\":\"USDT\",\"amount\":\"20000\",\"from_account_type\":\"SPOT\",\"to_account_type\":\"FUTURES\"}" http://ledger-service:8085/v1/account/transfers'"'"''
# 2. 开关允许合约（保留原来的 BTC-USDT）
exchangectl flags set market.maker --on --allow-symbols BTC-USDT,BTC-USDT-PERP --reason "quote BTC-USDT and BTC-USDT-PERP"
```

之后 `mm_quoting{symbol="BTC-USDT-PERP"}` 为 1，H5 合约交易页的盘口有双边报价。

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
