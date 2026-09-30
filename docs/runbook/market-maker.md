# HOUSE 虚拟流动性运维

设计稿 §8（ADR-0013、ADR-0014、ADR-0015）；实现见 `internal/marketmaker`（market-maker 服务，运维端口 9091）、`internal/matching/domain/reference.go`（引擎里的参考簿）与 `internal/ledger`（`HOUSE_TRADE_SETTLE`）。参考行情本身（币安的 ticker、K 线与盘口）见 [market-data.md](market-data.md)。

阶段 2 的"做市机器人"（需求 §11.10，逐档挂真实订单）已随 ADR-0015 退役：market-maker 不再下单，只把参考市场的盘口作为 **参考簿** 发给撮合引擎，由引擎在撮合时让 HOUSE 当对手方。

## 它做什么

- market-maker 从 `market.depth`、`derivatives.market.depth` 的末尾读公共盘口（只认标了 `reference` 的消息，也就是 market-data-service 转发的币安盘口；平台自己的盘口表示该交易对不显示参考市场，HOUSE 在那里不提供流动性）。
- 每 250 毫秒一轮，对每个现货交易对与合约算出 HOUSE 愿意成交的档位与剩余额度，发 `ReferenceBookUpdate` 到 `order.references`（合约 `derivatives.order.references`）；内容没变时 2 秒发一次心跳。
  - 档位：参考盘口最好的 20 档，价格放到本交易对的价格步长上（买价向下取、卖价向上取，HOUSE 永远不比参考市场给得多），落在同一格的合并，每档最多值 20,000 USDT，数量取整到数量步长。
  - 现货额度（ADR-0013）：
    - 可充提资产（`HOUSE_BACKED_ASSETS`，默认 USDT、BTC、ETH）要有库存才能卖，且保留 1,000 USDT 的价值不动（`HOUSE_SAFETY`）；买入花的 USDT 同样保留 1,000；
    - 内部资产（其余 47 个币）没有库存也能卖（HOUSE 在 `MARKET_MAKER` 科目上记负数，ADR-0013）；
    - 单个交易对的净头寸（多或空）最多值 100,000 USDT（`HOUSE_SYMBOL_CAP`），全部现货头寸合计最多 1,000,000 USDT（`HOUSE_TOTAL_CAP`）。
  - 合约额度：HOUSE 在该合约的净仓位多空各最多值 100,000 USDT（`HOUSE_CONTRACT_CAP`）。HOUSE 在合约上是一个普通用户账户（`HOUSE_USER_ID`），仓位与保证金在它的 FUTURES 账户里，**自己永不被强平**（用户穿仓后的自动减仓里，HOUSE 与其他盈利仓位一样可以是对手方，ADR-0015）。
- 发空簿（撤走 HOUSE 的流动性）的情况：开关 `market.house_liquidity` 不允许该交易对、`market.reference_feed` 关闭、参考盘口 3 秒没有消息或漏了增量（等下一个快照）、HOUSE 的余额与仓位 10 秒没读到。引擎自己也会丢弃比订单早 5 秒以上的参考簿（`RefMaxAge`，时间都在命令里，重放结果一致）。
- 库存每秒从账本 gRPC（`MARKET_MAKER` 各资产的可用余额）与 derivatives-service 内网接口（HOUSE 账户的仓位）读一次；交易对与合约规格每 30 秒从 instrument-service 读一次。

## 引擎怎么用参考簿

详见 [matching.md](matching.md#house-的参考簿adr-0015)。要点：

- 交易服务下单时决定订单是否 `house_only`：交易对跟随参考市场、`market.house_liquidity` 打开、`market.internal_matching` 关闭时为真，此时订单**只和 HOUSE 成交**（用户之间不互相成交），用户挂的限价单在参考价穿过时按其限价成交（HOUSE 当吃单方）。
- `market.internal_matching` 打开时用户订单先与其他用户的挂单成交（同价时用户挂单优先），再与 HOUSE 成交。测试服保持关闭。
- 现货成交里 HOUSE 一侧记在系统科目 `MARKET_MAKER`（分录 `HOUSE_TRADE_SETTLE`，HOUSE 不付手续费），用户一侧照常 `TRADE_SETTLE`/`TRADE_FEE`；合约成交里 HOUSE 一侧是 `HOUSE_USER_ID` 的普通合约结算。

## 指标与告警

| 指标 | 含义 |
|---|---|
| `market_house_active{symbol}` | 1 表示 HOUSE 在该交易对/合约上提供流动性，0 表示发了空簿 |
| `market_house_inventory{asset,backed}` | `MARKET_MAKER` 各资产可用余额（内部资产卖出后为负） |
| `market_house_exposure_usdt{symbol}` | 该交易对基础资产或合约仓位的 USDT 价值（空头为负） |
| `market_house_room{symbol,side}` | 最近一次发出的可买、可卖数量（基础资产） |
| `market_house_updates_total{kind}` | 发出的参考簿（`levels`/`empty`） |
| `market_house_publish_failures_total` | 发布失败的轮次（下一轮重发） |

告警（`deploy/observability/alerts.yml`）：`HouseInventoryNegative`（可充提资产库存为负，critical）、`HouseRoomExhausted`（某方向额度 10 分钟为 0：补库存或调上限）、`HousePublishFailing`、`ReferenceBookStale`（参考盘口不同步或 30 秒没变，见 [market-data.md](market-data.md)）。

日志：`house liquidity not published`、`house liquidity: HOUSE's holdings not read`、`house liquidity idle: set HOUSE_USER_ID`（没配 HOUSE 账户时服务空转）。

## 配置

`deploy/compose/docker-compose.apps.yml` 的 market-maker 段：`LEDGER_GRPC_ADDR`、`INSTRUMENT_SERVICE_URL`、`DERIVATIVES_SERVICE_URL`；上限用 `HOUSE_LEVEL_CAP`、`HOUSE_SYMBOL_CAP`、`HOUSE_TOTAL_CAP`、`HOUSE_CONTRACT_CAP`、`HOUSE_SAFETY`（USDT，默认 20000、100000、1000000、100000、1000）与 `HOUSE_BACKED_ASSETS` 覆盖。`HOUSE_USER_ID` 在服务器 `apps.env`（market-maker 与 derivatives-service 都读；测试服沿用原做市账户的用户 ID）。

## 测试服设置（一次性）

```bash
# 1. apps.env 加 HOUSE_USER_ID（沿用原做市账户），部署后生效
# 2. HOUSE 的现货库存：500,000 USDT 与约 20,000 USDT 的 BTC、ETH（需要 ledger.manual_adjustment，幂等）
scripts/ops/house.sh seed
# 3. 开关：公共盘口显示币安、HOUSE 提供流动性（50 个 USDT 交易对与 BTC-USDT-PERP；ETH-USDT-PERP 留给端到端脚本里的用户对敲）
exchangectl flags set market.reference_depth --on --reason "Binance books on the top 50 (ADR-0010)"
exchangectl flags set market.house_liquidity --on --allow-symbols <50 个 USDT 交易对>,BTC-USDT-PERP --reason "HOUSE liquidity (ADR-0015)"
# 4. 开放交易：把 test.json 里仍是 PREPARE 的 USDT 交易对改为 TRADING
scripts/ops/house.sh open
```

`scripts/ops/house.sh show` 看 HOUSE 的 `MARKET_MAKER` 余额。HOUSE 合约账户用原做市账户 FUTURES 里的约 2 万 USDT；亏损超过它时账本会拒绝结算并挂起（`DerivativesSettlementParked` 告警），要从该账户的现货划转补足。

## 验证与故障

- `scripts/e2e/house.sh`：50 个 USDT 交易对在交易、盘口是币安的（1000SHIB 按 1000 个计价）、新用户市价买卖 SOL-USDT 立即按盘口价与 HOUSE 成交、低于盘口的限价单挂着直到撤单、BTC-USDT-PERP 市价开仓与只减仓平仓、之后账本与合约对账通过。
- `scripts/fault/reference-outage.sh`：切断 market-data-service 的外网，几秒内 HOUSE 撤走流动性、公共盘口退回平台自己的（空）；恢复后重新显示币安盘口、HOUSE 重新提供流动性。
- 要临时停掉 HOUSE：`exchangectl flags set market.house_liquidity --off --reason "..."`（或把某个交易对从 allow 列表去掉），下一轮就发空簿；用户挂着的单子留在簿上，等开关恢复后按参考价成交。
