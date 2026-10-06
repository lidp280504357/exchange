# USDT 永续合约运维

需求 §5.8、§11.7，实施计划 §7.3 任务 5；实现见 `internal/derivatives`（derivatives-service：HTTP 8095、gRPC 9195、运维 9095，schema `derivatives`）。契约：`api/openapi/derivatives.yaml`（REST）、`api/proto/exchange/derivatives/v1`（gRPC 与 `derivatives.position.events`）。相关：合约规格见 [instruments.md](instruments.md)，撮合分片 derivatives-engine 见 [matching.md](matching.md#分片现货与合约)，指数价、标记价与资金费率见 [market-data.md](market-data.md#合约指数价标记价与资金费率)，账本动作见 [ledger.md](ledger.md#合约结算)。

## 模型

- 资金都在用户的 `FUTURES` 账户（USDT）里，由账本记账；合约服务只记"这些钱是做什么的"：
  - `frozen` = 所有挂单的预留（初始保证金 + taker 手续费）+ 所有仓位的保证金（逐仓的独立保证金，全仓仓位的初始保证金）。对账时逐用户核对。
  - `available` 是其余部分。未实现盈亏不入账；全仓的未实现亏损会从可开仓额和可转出额里扣掉。
- 设置（每个用户、每个合约）：持仓模式 `ONE_WAY`（一个净仓位，`position_side=BOTH`）或 `HEDGE`（`LONG`、`SHORT` 两个仓位）；保证金模式 `CROSS` 或 `ISOLATED`；杠杆 1 到合约第一档上限。默认单向、全仓、20 倍（不超过合约上限）。持仓模式和保证金模式只能在该合约没有仓位也没有挂单时改（`DERIV_SETTINGS_LOCKED`）。
- 仓位按入场成本记：成本 = Σ 开仓数量 × 成交价，减仓按比例扣减（按 USDT 精度取整，全平时恰好清零）。已实现盈亏 = 平仓数量 × 成交价与扣减成本之差（多头为前减后，空头为后减前），对手是账本科目 `PNL_CLEARING`。所以每个合约恒有 `PNL_CLEARING + Σ多头成本 − Σ空头成本 = 0`（不变量 6）。

## 下单

`POST /v1/derivatives/orders`（经网关，需登录，按下单限流，支持 `Idempotency-Key`；`client_order_id` 重复且内容相同返回原订单）。

- 检查顺序：合约状态为 `TRADING`；有新鲜标记价（10 秒内，否则 `DERIV_MARK_PRICE_UNAVAILABLE`）；价格是 tick 的整数倍、数量是 lot 的整数倍且在范围内；限价离标记价不超过价格带（`ORDER_PRICE_OUT_OF_BAND`）；持仓模式与 `position_side` 对得上；合约处于只减仓时只收平仓单（`DERIV_REDUCE_ONLY_MODE`）；活动单上限。
- **开仓单**（单向模式的普通单、双向模式的 BUY LONG / SELL SHORT）还要：
  - 账户资格 `DERIVATIVES_TRADE`，它同时检查功能开关 `derivatives.trading`；
  - 风险限额：该方向上的持仓 + 同方向活动开仓单 + 本单，按标记价的名义价值不超过杠杆对应的上限（最后一个"最高杠杆 ≥ 本杠杆"的档位的名义价值上限），否则 `DERIV_RISK_LIMIT_EXCEEDED`；
  - 保证金：按手预留 `价格 × lot / 杠杆` 与 `价格 × lot × taker 费率`（各自向上取整；卖单的价格取限价与下单时标记价中较高的，因为卖单只会以不低于限价的价格成交，穿价时成交在标记价附近，审查 A5），合计不超过"可用余额减去全仓未实现亏损"（`DERIV_INSUFFICIENT_MARGIN`）。预留经账本冻结（`ORDER_FREEZE`，键 `order:<订单ID>`）；冻结结果不明时订单停在 `PENDING`，恢复循环每 5 秒用同一个键重试。
- **平仓单**（`reduce_only`，或双向模式的 SELL LONG / BUY SHORT）不预留任何东西、不查资格与最小名义价值，但数量不能超过该仓位还没被其他平仓单占用的部分（`DERIV_REDUCE_ONLY_REJECTED`）。
- 市价单：以"标记价 ± 价格带"（买单向下、卖单向上取到 tick）为价格的 IOC（默认）或 FOK 限价单交给引擎；买单按这个保护价预留，卖单按标记价预留（保护价在标记价下方 5%，按它预留在 125 倍时只有 0.76%，低于 0.8% 的初始保证金）。两个下单表单算"最大可开"时用同样的价格（`reservePrice`）。
- 发给引擎的命令（`derivatives.order.commands`）手续费率为 0：合约手续费由合约服务按 USDT 计。订单受理与拒绝（`OrderAccepted`/`OrderRejected`）和引擎的订单事件一起在 `derivatives.order.events` 上。
- HOUSE 流动性（阶段 4 B4，ADR-0015）：合约的指数交易对跟随参考市场、`market.house_liquidity` 对该合约打开且 `market.internal_matching` 关闭时，命令带 `house_only`，订单只和 HOUSE 的参考簿（币安 U 本位合约的盘口）成交，见 [market-maker.md](market-maker.md)。测试服对全部合约打开（用户决定 2026-10-02：所有交易都与 HOUSE 成交，开多开空都是，用户之间不撮合）。平台币的永续 ASTRA-USDT-PERP 的指数交易对不跟随参考市场，HOUSE 不为它报价，它的订单互相成交（对手方是模拟市场的机器人，见 [market-sim.md](market-sim.md)）；`scripts/ops/house.sh flags` 也不把它列进 `market.house_liquidity`。

撤单：`DELETE /v1/derivatives/orders/{id}`、`DELETE /v1/derivatives/orders?symbol=`，由引擎确认。订单结束（成交完、撤销、拒绝）时，没成交那部分的预留（按手数）解冻，键 `release:<订单ID>`；这与成交先到还是后到无关，因为每笔成交只动用自己那几手的预留。

## 成交结算

消费组 `derivatives-service` 批量读 `derivatives.order.events` 与 `derivatives.trade.events`，逐条按顺序处理，失败整批重试、不跳过。每笔成交先买方后卖方，每一方在持有该用户锁的事务里：读仓位与订单 → 算出结算计划（`domain.PlanFill`）→ 调账本 `SettleFutures`（键 `fill:<成交ID>:<BUY|SELL>`）→ 写仓位、订单、成交记录并发事件。重投时看到这一方已记录就跳过；账本已记、本地没写成功时，重算出同一个计划，账本按键返回第一次的结果。

一方的计划：

| 部分 | 全仓 | 逐仓 |
|---|---|---|
| 手续费 | 数量 × 价格 × 角色费率，向上取整；先从订单这几手的手续费预留里付，预留剩余解冻 | 同左 |
| 平仓部分 | 释放该部分的仓位保证金（按比例，全平时全部）；盈利从 `PNL_CLEARING` 进可用余额，亏损从可用余额付，不够的由保险基金补；预留外的手续费从可用余额付，不够就免收 | 亏损最多付到该部分的保证金为止，剩下的由保险基金补；预留外的手续费从剩余保证金里付；其余保证金解冻（强平时进保险基金）；盈利进可用余额 |
| 开仓部分 | 订单这几手的保证金预留直接转为仓位保证金；成交价高于限价的卖单多出的手续费从这部分保证金里扣 | 同左 |
| 平仓单超出仓位 | 平仓单下单后仓位变小了（被强平等），超出部分开出反向仓位，保证金按"成交额 / 杠杆"从可用余额冻结，能冻多少冻多少 | 同左 |

单向模式一笔成交可以先平后开（反手）；双向模式的平仓单超出部分开在另一侧。

**HOUSE 一侧**：成交带 `house_side` 时，HOUSE 那一方按一张虚拟订单结算（`domain.HouseOrder`：单向持仓、全仓、合约最高杠杆、零手续费，没有预留），仓位与保证金记在 `HOUSE_USER_ID` 的 FUTURES 账户，和普通用户一样收付资金费。强平检查跳过 HOUSE 的仓位（永不被强平）；自动减仓时 HOUSE 与其他盈利仓位一样可以是对手方。HOUSE 的 FUTURES 可用余额不够付保证金或亏损时，这一方的结算按上面的"账本拒绝"挂起，需从 HOUSE 账户的现货划转补足。

**账本拒绝**（保险基金也不够补缺口，或数据不一致）：这一方照样按计划更新仓位和成交（成交 `settled=false`），结算请求存入 `pending_settlements`，恢复循环每 5 秒重试，直到账本记账成功（部分冻结的实际金额届时补进仓位保证金）。指标 `derivatives_settlements_parked_total`，告警 `DerivativesSettlementParked`，对账 `NO_PENDING_SETTLEMENTS` 非零。处理：补足保险基金（`exchangectl ledger insurance-fund`），等待自动重试。

## 保证金、杠杆与转出

- 调杠杆 `PUT /v1/derivatives/settings/{symbol}`：每个持仓按标记价的名义价值必须在新杠杆的风险限额内；全仓仓位的保证金改为"入场成本 / 新杠杆"，多出的冻结（不够则 `DERIV_INSUFFICIENT_MARGIN`）、少了的解冻；逐仓仓位保证金不变，但必须仍够新杠杆下的初始保证金。
- 逐仓追加 / 减少保证金 `POST /v1/derivatives/positions/{symbol}/margin`（`amount` 正为追加、负为减少）：减少后保证金要不低于入场成本 / 杠杆，加上未实现盈亏后也不低于"标记价名义价值 / 杠杆"（`DERIV_MARGIN_REDUCE_TOO_LARGE`）。
- 转出 `FUTURES → SPOT`：ledger-service 先经 gRPC 向合约服务要全仓未实现盈亏（`GetUnrealizedPnL`，某个全仓持仓没有新鲜标记价时返回不可用，转出失败 503），最多转出 `min(可用余额, 可用余额 + 全仓未实现盈亏)`，未实现盈利不能转出，被未实现亏损占用的部分也不能转出。`GET /v1/derivatives/account` 的 `transferable` 即此值。

## 资金费

实施计划 §7.3 任务 6。费率与结算标记价由 market-data-service 算出并在周期结束后结算（见 [market-data.md](market-data.md#合约指数价标记价与资金费率)），合约服务每 3 秒检查一次：

1. **快照**：每个合约的最近一个结算时点（8 小时周期即 00:00、08:00、16:00 UTC）还没有记录时，持成交处理锁记下此刻全部未平仓位（`derivatives.funding_rounds` 与 `funding_payments`），即"结算时刻的持仓"，通常在结算时点后几秒内完成。服务整段停机跨过结算点时，恢复后只给最近一个结算点拍快照（按恢复时的持仓），错过的更早的结算点不收。
2. **结算**：从 market-data-service 取该周期已结算的费率与标记价（`GET /v1/market/{symbol}/funding-rates`）；取不到就下一轮再试，超过 2 小时仍没有（行情服务整个周期没有样本）则该轮记为 SKIPPED、不收。取到后逐仓收付：金额 = |数量| × 标记价 × |费率|，费率为正多付空、为负空付多；付方向上取整、收方向下取整，零头留在 `FUNDING_CLEARING`。**先处理全部付方再处理收方**，清算科目不会为负（账本对账 `FUNDING_BATCHES_BALANCED` 核对每一轮）。
3. 每笔是一次账本 `SettleFutures`（键 `funding:<合约>:<结算时间戳>:<仓位ID>`，`FUNDING_PAY`/`FUNDING_RECEIVE`）：仍在持有的逐仓仓位从自己的保证金里付（最多付到保证金为止）、收到的计入保证金；全仓或结算前已平掉的仓位用可用余额；付不起的部分由保险基金补。随后更新仓位的资金费累计（逐仓还有保证金），发 `FundingPaid`（WebSocket `positions` 频道 `event=FUNDING`）。中途失败的轮次下次从没结的那笔继续。

记录：`GET /v1/derivatives/funding?symbol=&cursor=&limit=`（结算时间、持仓方向与数量、费率、标记价、金额）。

```sql
SELECT symbol, funding_time, status, funding_rate, mark_price, positions FROM derivatives.funding_rounds ORDER BY funding_time DESC LIMIT 6;
SELECT count(*) FILTER (WHERE settled_at IS NULL) AS waiting, sum(amount) AS net FROM derivatives.funding_payments WHERE funding_time = '...';
```

## 止盈止损

实施计划 §7.3 任务 8。`POST /v1/derivatives/conditional-orders` 对一个未平仓位挂止盈（`TAKE_PROFIT`）或止损（`STOP_LOSS`）条件单（`derivatives.conditional_orders`），每个合约最多 20 个活动的：

- 触发价格：标记价（`trigger_by=MARK`，默认，需新鲜）或最新成交价（`LAST`，服务重启后先取最近一笔合约成交）。多头的止盈在价格 ≥ 触发价时触发、止损在 ≤ 时触发；空头相反。下单时价格已经越过触发价会被拒（`DERIV_TRIGGER_IMMEDIATE`）。
- 触发后（每秒检查一次）按条件单下一张只平仓的订单：市价（受保护的 IOC，默认）或给定价格的限价单；数量为条件单的数量，不填则平掉剩余全部，且不超过当时仓位。订单 `kind` 为 `TAKE_PROFIT`/`STOP_LOSS`，`client_order_id` 是条件单 ID，所以崩溃后重试不会重复下单。
- 状态：`ACTIVE` → `TRIGGERED`（带所下订单 ID），或 `FAILED`（下单被拒，`reason` 是错误码，例如仓位正在强平），或 `CANCELED`（用户撤销 `USER`，或触发时仓位已平或已反向 `NO_POSITION`）。

## 强平与 ADL

实施计划 §7.3 任务 7。监控循环每秒按新鲜标记价检查一次（`Monitor`）：

- **计量**：逐仓仓位单独算，保证金余额 = 仓位保证金 + 未实现盈亏；全仓按用户合并算，权益 = 可用余额 + 全仓仓位保证金 + 全仓订单预留 + 全仓未实现盈亏（要向账本查余额；某个全仓合约没有新鲜标记价时本轮不算这个用户）。维持保证金 = 名义价值 × 该档维持保证金率（强平手续费已并入）。
- **预警**：保证金余额 ≤ 1.2 × 维持保证金时发 `LiquidationWarning`（WebSocket `risk` 频道 `event=WARNING`），同一段只发一次，回到 1.3 倍以上才重置（逐仓记在仓位的 `warned_at`，全仓记在 `derivatives.cross_accounts`）。
- **接管**：保证金余额 ≤ 维持保证金时，撤掉相关挂单（逐仓：该仓位的挂单；全仓：该用户所有全仓挂单），仓位标为 `liquidating`，发 `LiquidationStarted`（`risk` 频道 `event=STARTED`）。此后用户不能再对该仓位下单、调保证金或杠杆（`DERIV_POSITION_LIQUIDATING`）。
- **强平单**：每秒检查，没有进行中的强平单就下一张 IOC 限价单（`kind=LIQUIDATION`，平掉剩余数量）：逐仓以破产价、全仓以标记价为基准，向不利方向偏 0.5%（`domain.Slippage`），取到 tick。成交按强平结算：逐仓亏损最多到保证金、剩余保证金进保险基金（`INSURANCE_CONTRIBUTION`）、缺口由保险基金补，分录类型 `LIQUIDATION_SETTLE`；全仓亏损从可用余额付，不够的由保险基金补。每次成交发 `LiquidationFilled`（`event=LIQUIDATED`）。
  - HOUSE 报价的合约上强平单也带 `house_only`，对手方只有 HOUSE。HOUSE 的合约仓位合计到了权益的 `HOUSE_CONTRACT_LEVERAGE` 倍（或该方向到了单合约上限）后只减仓：被强平的用户若与 HOUSE 站在同一侧（例如都多头，强平要卖、HOUSE 不再买入），HOUSE 那一侧没有报价，强平单成交不了，重试 3 次后转 ADL、缺口由保险基金补。这是"HOUSE 永不被强平、用户之间不撮合"（ADR-0015）的必然结果；告警 `HouseContractEquityGone` 与指标 `market_house_contract_exposure_usdt` 提示 HOUSE 快到上限，补 HOUSE 合约保证金（`exchangectl ledger house-margin`）能让它恢复接单。
- **ADL**：强平单下了 3 次（`MaxLiquidationAttempts`）仍没平完，剩余部分对对手方执行自动减仓：同合约反方向、未在强平中的其他用户仓位按"盈利率（未实现盈亏 / 入场成本）× 有效杠杆（名义价值 / 保证金余额）"从高到低排队，按破产价（逐仓）或标记价（全仓）在订单簿之外成交，无手续费，分录类型 `ADL_SETTLE`。被减仓的一方收到 `AdlExecuted`（`event=ADL`）。
- 仓位平完后 `liquidating`、预警标记自动清除。

指标：`derivatives_liquidation_steps_total{step}`（warning、takeover、order、adl）。日志：`position taken over for liquidation`、`position auto-deleveraged`。

```sql
SELECT user_id, symbol, position_side, quantity, margin, liquidating, liquidation_attempts, liquidation_at, warned_at
FROM derivatives.positions WHERE liquidating OR warned_at IS NOT NULL;
SELECT order_id, user_id, side, price, quantity, status, filled_quantity FROM derivatives.orders WHERE kind IN ('LIQUIDATION', 'ADL') ORDER BY order_id DESC LIMIT 20;
```

## 只减仓（降级）

两种情况会把合约置为只减仓（`derivatives.contract_states`），只收平仓单：

- market-data-service 连续 10 秒算不出该合约的标记价（价源不足），发 `risk.events` 的 `SystemDegraded`，合约服务的消费组 `derivatives-service-risk` 收到后置位；
- 合约服务自己发现：有未平仓位的合约，标记价已超过 10 秒没更新（服务启动 30 秒后才判断），原因 `MARK_PRICE_STALE`。

"风控服务不可用"这一条暂不适用：合约服务不依赖 risk-service 做同步检查。标记价恢复后也**不会**自动解除，需人工确认：管理后台「合约」页（权限 `derivatives.write`，标记价未恢复时按钮不可用，动作有审计事件 `admin.derivatives.reduce_only_lifted`），或命令行：

```bash
ssh exchange sudo docker exec exchange-infra-derivatives-service-1 /app/exchangectl derivatives states
ssh exchange sudo docker exec exchange-infra-derivatives-service-1 /app/exchangectl derivatives resume BTC-USDT-PERP
```

故障注入 `scripts/fault/contract-degrade.sh` 演练整个过程：切断 market-data-service 的外网 → 标记价报 `degraded` → 合约只减仓、开仓单被拒 → 恢复外网后仍只减仓（开仓单报 `DERIV_REDUCE_ONLY_MODE`）→ `resume` 后恢复。

部署会重启 market-data-service，标记价可能中断超过 10 秒，合约因此进入只减仓（2026-10-01 有一次 ETH-USDT-PERP 停在只减仓几个小时，直到端到端测试失败才发现）。`deploy/server-update.sh` 最后等 20 秒，解除部署期间开始、原因为 `INDEX_SOURCES` 或 `MARK_PRICE_STALE` 的只减仓，解除人记为 `deploy-<版本>`（`exchangectl` 读 `EXCHANGECTL_ACTOR`）；价源若真断了，10 秒后又会只减仓。部署之外开始的只减仓仍须人工解除。

## 币本位合约（反向合约，币本位设计 2026-10-06 §2，批次 G1）

- 规格（G0）：`margin_type` 为 `COIN`、`settle_asset` 为基础资产（BTC、ETH、ASTRA）、`contract_size` 为面值（美元：BTC 100、其它 10），以 USD 计价。服务读 instrument-service 的全部合约（`margin_type=ALL`），金额精度取结算资产的（BTC、ETH 8 位）。
- 账户：每个结算资产一个 `FUTURES` 账户（USDT、BTC、ETH、ASTRA），冻结、结算、资金费、保证金划转、全仓权益与全仓预警都按（用户, 结算资产）分开；`GET /v1/derivatives/account?asset=BTC` 看该资产的账户（缺省 USDT，不是任何合约结算资产的为 `DERIV_SETTLE_ASSET_MISMATCH`）；全仓预警记在 `cross_accounts`（迁移 derivatives 00006 起主键为（用户, 资产））。划入 FUTURES 用现有划转按资产划。
- 下单：数量是整数张（有小数为 `DERIV_CONTRACTS_NOT_INTEGER`），价格是美元；开仓还要资格 `COIN_M_TRADE`（开关 `derivatives.coin_m`，按用户或地区，关着为 `USER_NOT_ELIGIBLE`）。买单按 min(限价, 标记价) 预留、卖单按限价（价格越低占用的币越多，与 U 本位相反）。
- 公式（`domain/contract.go`、`position.go`）：价值 V(P) = 张数 × 面值 ÷ P；每笔成交的币价值按结算资产精度四舍五入一次，双方同值，成本、已实现盈亏、`PNL_CLEARING` 都用它；未实现盈亏多头 = 成本 − V(P)，空头取反；维持保证金 = V(P) × 维持率 − 抵扣（档位按币计的名义价值，与币安 COIN-M 的 `qtyCap` 同口径）；逐仓强平价多头 Q×S×(1+r) ÷ (M + D + 成本)，空头 Q×S×(1−r) ÷ (成本 − M − D)（分母 ≤ 0 时没有强平价，破产价同理，强平单与 ADL 以标记价兜底）；资金费 = |张数| × 面值 × |费率| ÷ 标记价（付方向上、收方向下）；手续费按未舍入的币价值 × 费率向上。
- 不变量 6 按资产：反向合约的成本方向相反，`PNL_CLEARING` + U 本位 Σ多头成本 − Σ空头成本 − 币本位 Σ多头成本 + Σ空头成本 = 0。
- 结算：账本 `SettleFutures` 按资产；HOUSE 的 BTC/ETH 不能为负，亏损超过余额的部分由同一资产的保险基金补，基金不够时整笔拒绝、不入账（账本集成测试 `TestCoinSettledFutures`）。保险基金与杠杆交易共用 `INSURANCE_FUND` 的同一行（协调会话 20:45 决定 ⑦）。
- 事件与推送：`Position`、`FillSettled` 带 `settle_asset`、`contract_size`，`LiquidationWarning`、`LiquidationFilled`、`AdlExecuted` 带 `settle_asset`（全仓预警的是该账户的资产）；发给引擎的 `PlaceOrder` 带两者，引擎的 `TradeExecuted` 也带，币本位成交的 `quote_quantity` 是张数 × 面值（美元），订单的 `filled_quote` 仍是价格 × 数量之和（平均价由它算）。网关的 `orders`（受理时）、`fills`、`positions`、`risk` 推送带 `settle_asset`，合约成交的 `fee_asset` 是结算资产。ClickHouse 的 `trades`、`derivatives_positions`、`derivatives_fills`、`derivatives_funding` 记 `settle_asset`，币本位成交的 `notional` 是美元价值。
- REST：仓位多了 `settle_asset`、`contracts`（币本位的有符号张数，线性为 null）、`value_coin`（币本位按标记价的币价值）、`value_usd`（币本位张数 × 面值，线性为名义价值）；订单、成交、资金费记录带 `settle_asset`。

## 对账（不变量 6）

每小时（`RECONCILE_INTERVAL`，启动 1 分钟后先跑一次），持有成交处理锁，结果写 `derivatives.reconciliation_runs`，指标 `derivatives_reconcile_mismatches{check}`，告警 `DerivativesReconciliationMismatch`：

| 检查 | 含义 |
|---|---|
| `POSITIONS_BALANCED` | 每个合约多头总量 = 空头总量 |
| `PNL_CLEARING_MATCHES_POSITIONS` | 按结算资产：账本 `PNL_CLEARING` + U 本位 Σ多头成本 − Σ空头成本 − 币本位 Σ多头成本 + Σ空头成本 = 0（反向合约的成本方向相反，见下节） |
| `NO_PENDING_SETTLEMENTS` | 没有等账本记账的结算 |

手工：`exchangectl derivatives reconcile`（直接读两边的库，成交在途时可能有瞬时差异）。账本侧另有 `FUNDING_BATCHES_BALANCED`、`PNL_CLEARING_ONLY_PNL`（见 [ledger.md](ledger.md#对账)）。

## 接口

REST（经网关 `/v1/derivatives/*`，需登录）：

| 路径 | 内容 |
|---|---|
| `GET /v1/derivatives/account` | FUTURES 余额、挂单预留、仓位保证金、未实现盈亏（全部 / 全仓）、保证金余额、可转出 |
| `GET/PUT /v1/derivatives/settings/{symbol}` | 持仓模式、保证金模式、杠杆 |
| `GET /v1/derivatives/positions?symbol=` | 未平仓位：数量（带符号）、开仓均价、标记价、名义价值、未实现盈亏、保证金、维持保证金、预估强平价（逐仓按自己的保证金；全仓按整个全仓账户：可用余额、全仓挂单预留与各全仓仓位按各自标记价计，求本合约标记价到哪里时权益等于维持保证金，`domain.CrossLiquidationPrice`；多仓的权益覆盖得了跌到 0 时为空）、已实现盈亏、资金费 |
| `POST /v1/derivatives/positions/{symbol}/margin` | 逐仓追加 / 减少保证金 |
| `POST/GET/DELETE /v1/derivatives/orders`、`GET/DELETE /v1/derivatives/orders/{id}` | 下单、订单列表、撤单 |
| `GET /v1/derivatives/fills?symbol=` | 成交（角色、平仓数量、手续费、已实现盈亏、是否强平、是否已记账） |
| `GET /v1/derivatives/funding?symbol=` | 资金费收付记录 |
| `POST/GET /v1/derivatives/conditional-orders`、`DELETE /v1/derivatives/conditional-orders/{id}` | 止盈止损条件单 |

内部 REST（只给 admin-service，网关不转发 `/internal`）：

| 路径 | 内容 |
|---|---|
| `GET /internal/derivatives/contracts` | 每个合约的状态、只减仓（原因、开始时间、上次谁解除）、标记价与是否新鲜、持仓量（多头总量）与持仓数 |
| `POST /internal/derivatives/contracts/{symbol}/lift-reduce-only` | `{"actor": ...}` 解除只减仓，返回 `lifted` 表示原来是否只减仓 |
| `GET /internal/derivatives/risk` | 被接管、已预警或保证金率（维持保证金 ÷（保证金 + 未实现盈亏））≥ 0.5 的仓位，风险高的在前；全仓仓位在这里按单个仓位计算 |
| `GET /internal/derivatives/positions` | 全部用户的持仓（后台「仓位」页，2026-10-02 C3）：`symbol`、`user_id`（按用户只读这个用户的仓位）、`watch=true`（只要上一行的风险仓位，另加全仓账户被预警的全仓仓位；不含 HOUSE；全仓账户的预警在所有视图里都算进它的全仓仓位的 `warned_at`，C5.5 ⑱）、`limit`（默认 200，超过 1000 按 1000）；按保证金率、再按开仓名义价值从大到小，HOUSE 的仓位排在最后（HOUSE 的单按最高杠杆记保证金，零盈亏时保证金率就约 50%，不排后会占满"最危险"），`truncated` 表示被 `limit` 截断；每行带 `mark_fresh`，标记价不新鲜时盈亏与保证金率停在上一个标记价（C5.5 ⑨） |
| `POST /internal/derivatives/positions/close` | 后台强制平仓（C2）：先撤该用户在这个合约上的全部挂单，再以 `ADMIN` 类型的市价只减仓单平掉；HOUSE 不平 |
| `GET /internal/derivatives/users/{id}/cross-margin?debit=` | 后台调账预览（C5.5 ⑧）：全仓权益、维持保证金与状态，以及扣减 `debit` 后的权益与状态（`HEALTHY`/`WARNING`/`LIQUIDATE`） |
| `POST /internal/derivatives/contracts/{symbol}/tier-impact` | 新风险阶梯的影响（后台预览，2026-10-02 设计 §2 第 6 条）：`{risk_tiers}`，按保证金监控的规则（逐仓看仓位、全仓看整个账户，HOUSE 不计）算出会新被强平的仓位数、名义价值与账户数，新进入预警、超出杠杆风险限额、没有新鲜标记价的数量，以及最大的 20 个例子；阶梯不合法答 400，不改任何东西；全仓账户逐个向账本查余额，最多量 2000 个、8 秒（后台等 10 秒），剩下的仓位计入「没有新鲜标记价」那一项（`unmeasured`），后台因此不给确认（C5.5 ⑩） |
| `POST /internal/derivatives/contracts/{symbol}/price-impact` | 某个标记价的影响（后台"模拟市场"价格事件的确认框，ASTRA 设计 §6.3）：`{target_price}` → `positions`、`liquidated`（标记价到这里会新被强平的仓位数；全仓账户整个算进去）、`notional`（按目标价）、`accounts`、`insurance_cost`（按目标价平掉时逐仓保证金或全仓权益低于 0 的部分，即保险基金预计承担）、`unmeasured`（现在没有新鲜标记价、量不了的仓位）与最大的 20 个例子（保证金余额按目标价，维持保证金是现在与目标价两个）；与阶梯影响同一套保证金监控算法与上限（2000 个全仓账户、8 秒），HOUSE 不计，不改任何东西；目标价不是正数答 400 |

WebSocket 私有频道：`orders`（合约订单与现货订单同一频道，按 `symbol` 区分）、`fills`（合约成交带 `position_side`、`closed_quantity`、`realized_pnl`，手续费资产 USDT）、`positions`（`event` 为 OPEN、INCREASE、REDUCE、CLOSE、FLIP、MARGIN、FUNDING、LEVERAGE）、`risk`（`event` 为 WARNING、STARTED、LIQUIDATED、ADL）。

## 测试服设置

- 开关 `derivatives.trading` 默认关闭（ADR-0005），测试服打开：`exchangectl flags set derivatives.trading --on --reason "测试环境开放合约"`。
- 合约在 `deploy/instruments/test.json` 里以 `TRADING` 创建（状态只在创建时取文件里的值，之后用 `exchangectl instruments contract-status` 改）。
- 风险限额（用户决定 2026-10-02，两个合约相同，`deploy/instruments/test.json` 的 `risk_tiers`）：名义价值 ≤ 50,000 USDT 最高 125 倍、维持保证金率 0.4%；≤ 250,000 为 100 倍、0.5%；≤ 1,000,000 为 50 倍、1%；≤ 5,000,000 为 20 倍、2.5%；≤ 20,000,000 为 10 倍、5%；≤ 50,000,000 为 5 倍、10%；≤ 100,000,000 为 2 倍、12.5%。杠杆对话框的上限、首页"合约最高杠杆"与规格接口都从这里读，不写死；强平价按所在档位的维持保证金率算（`internal/derivatives/domain/risk_test.go` 的 `TestTheLadderAt125x`）。超出风险限额的错误 `DERIV_RISK_LIMIT_EXCEEDED` 带 `max_notional`、`leverage` 与 `notional`（下单后或调杠杆时该方向的名义价值，按标记价），前端据此说出具体数字；PC 与手机的下单表单分别显示"可开多 / 可开空"（保证金与风险限额两者取小，`packages/core` 的 `riskRoom`、`sideExposure`），提交前就用同样的规则检查（`checkRiskLimit`）。
- 流动性：两个合约都由 HOUSE 按币安合约盘口提供（开关 `market.house_liquidity` 允许全部合约，见 [market-maker.md](market-maker.md)）。`HOUSE_USER_ID` 沿用原做市账户，其 FUTURES 账户由 `scripts/ops/house.sh seed` 注资到 2,000,000 USDT（`exchangectl ledger house-margin`）；测试服 HOUSE 在每个合约上多空各最多接 5,000,000 USDT（`HOUSE_CONTRACT_CAP`），到上限后该方向没有报价。阶段 3 的挂单做市（`MARKET_MAKER_CONTRACTS`、开关 `market.maker`）已随 ADR-0015 退役。
- 保险基金用模拟资金注资：`exchangectl ledger insurance-fund --amount 1000000 --reason "测试环境保险基金" --key insurance-seed-1`（ledger-service 容器里执行，需 `ledger.manual_adjustment`）。
- 端到端：`scripts/e2e/contracts.sh`（规格与 125 倍阶梯、标记价、资金费率、两个合约的盘口）、`scripts/e2e/derivatives.sh`（两个用户在 ETH-USDT-PERP 上各自与 HOUSE 开多、开空，杠杆到 125 倍为止，挂单预留与撤单，只减仓市价平仓，按成交核对盈亏、手续费与余额，转回，最后跑对账）、`scripts/e2e/house.sh` 的合约部分（两个合约各开平一次）、`scripts/e2e/funding.sh`（资金费，见下）、`scripts/e2e/admin.sh` 的合约部分（合约状态、只减仓、状态往返、强平监控、双人审批的保险基金注资）；故障注入 `scripts/fault/contract-degrade.sh`（降级与人工解除）。
- 资金费端到端靠一对**常驻对冲仓位**：`funding.sh` 第一次运行时注册两个用户，各转 100 USDT 到合约账户，在 ETH-USDT-PERP 上用市价单分别与 HOUSE 开多、开空 0.01 张后保持不平（B4 之前开的那一对在 BTC-USDT-PERP 上 0.001 张，状态文件没有合约名时按它处理），邮箱与随机密码记在本机 `~/.cache/exchange-e2e/`（`E2E_STATE_DIR` 可改，不进仓库）；之后每次运行登录这两个用户（超过 7 天未登录时从开发收件箱取登录挑战验证码），逐个检查开仓以来每个资金费时间点：双方都有记录、费率等于 market-data-service 结算的费率、付款方付 仓位数量 × 结算标记价 × |费率| 向上取整、收款方向下取整。仓位没了（被平或被减仓）就重新开一对。强平本身依赖真实价格波动，端到端无法稳定触发，由应用层测试覆盖（`internal/derivatives/application` 的强平、ADL、全仓强平用例，设置 `TEST_POSTGRES_DSN` 时在真实库上跑）。
- 资金费轮次：`exchangectl derivatives funding [--symbol S] [--limit N]` 列出最近的轮次（费率、标记价、仓位数、已结算数、付出、收到、保险基金垫付）；有轮次等费率超过 2 小时 10 分钟仍未跳过、或收到的多于付出加保险基金时以非零退出（`funding.sh` 每次都跑）。

## 查看

```sql
-- schema derivatives
SELECT user_id, symbol, position_side, quantity, entry_cost, margin, margin_mode, leverage, realized_pnl FROM derivatives.positions WHERE quantity <> 0;
SELECT order_id, symbol, side, status, quantity, filled_quantity, consumed_quantity, released FROM derivatives.orders ORDER BY order_id DESC LIMIT 20;
SELECT * FROM derivatives.pending_settlements;
SELECT check_name, mismatches, details FROM derivatives.reconciliation_runs ORDER BY id DESC LIMIT 3;
```

- 指标：`derivatives_fills_total`、`derivatives_settlements_parked_total`、`derivatives_reconcile_mismatches{check}`、`derivatives_reconcile_last_success_timestamp_seconds`、`kafka_consumer_lag{group="derivatives-service"}`、`outbox_pending{schema="derivatives"}`。
- 日志：`contract settlement refused by the ledger; parked`、`contract under reduce-only`、`derivatives invariant broken`、`recovery failed`。

## 管理后台与读模型

- 管理后台「合约」页（[admin.md](admin.md#合约与保险基金)）：合约状态与只减仓（解除需 `derivatives.write`，改状态需 `instruments.write`）、保险基金余额与 `PNL_CLEARING`、发起保险基金注资（双人审批，批准后账本 `FundInsurance` 以幂等键 `approval:<id>` 记 `INSURANCE_CONTRIBUTION`，需开关 `ledger.manual_adjustment`）、强平监控（每 5 秒刷新）、强平记录。报表页有合约日报与当前持仓量。
- **强制平仓**（2026-10-02 设计 C2，用户页「仓位」标签，需 `derivatives.write`）：内部接口 `POST /internal/derivatives/positions/close`（`user_id`、`symbol`、`position_side`、`client_order_id`）。先对该用户在这个合约上的全部挂单请求撤单（C5.5 ⑧ 起包括开仓单：平仓后它成交会把仓位开回去；条件单 `conditional_orders`（止盈止损）不撤，它们触发后只减仓），只要还有没撤完的就答 409 `DERIV_CLOSE_PENDING`（后台每 0.7 秒重试，最多 8 次）；撤完后以市价单平掉整个仓位：订单类型 `ADMIN`（迁移 derivatives 00005），单向持仓为只减仓、双向持仓按方向平。同一个 `client_order_id` 重复调用返回同一笔订单。用户锁只在检查挂单的事务里持有：检查之后、平仓单下单之前用户仍可再开仓（C5.5 ⑯ 评审的低级别项，后台提示里写明，不另锁用户）。正在强平的仓位交给强平引擎（`DERIV_POSITION_LIQUIDATING`），没有仓位答 `DERIV_NO_POSITION`，HOUSE 的仓位不平（`DERIV_HOUSE_NOT_CLOSED`：ADMIN 市价单会和 HOUSE 自己成交）。后台在请求时记审计 `admin.derivatives.position_close_requested`，订单结束后记 `admin.derivatives.position_closed`（成交数量与是否全部成交；盘口薄时可能只成交一部分，剩下的再平一次）；单笔撤合约委托记 `admin.derivatives.order_canceled`。
- ClickHouse 读模型（`migrations/clickhouse/00005_derivatives_read_models.sql`，analytics-consumer 投影，见 [analytics.md](analytics.md)）：`derivatives_positions`（每个仓位的最新快照，按 `version` 取最新）、`derivatives_fills`（已记账的成交，每笔两边各一行，带名义价值）、`derivatives_funding`（每个仓位每次资金费）、`derivatives_liquidations`（WARNING、STARTED、FILLED、ADL 各步骤）。合约的订单与成交并入现货的 `orders`、`order_updates`、`trades`（按 `symbol` 区分），所以交易报表与 K 线也覆盖合约。

```sql
-- ClickHouse
SELECT symbol, sumIf(quantity, quantity > 0) AS long_qty, countIf(quantity != 0) AS positions FROM derivatives_positions FINAL GROUP BY symbol;
SELECT toDate(funding_time) AS day, symbol, sum(amount) FROM derivatives_funding FINAL GROUP BY day, symbol ORDER BY day DESC;
SELECT kind, user_id, symbol, quantity, realized_pnl, insurance_paid, occurred_at FROM derivatives_liquidations FINAL ORDER BY occurred_at DESC LIMIT 20;
```

## 故障与处理

| 情况 | 表现 | 处理 |
|---|---|---|
| 账本不可用 | 下单停在 PENDING、成交批次重试、积压上升 | 恢复后自动继续；冻结与结算都按键幂等 |
| 标记价中断 | 下单报 `DERIV_MARK_PRICE_UNAVAILABLE`；10 秒后合约只减仓 | 查 market-data-service 与参考行情；恢复后在管理后台解除或 `exchangectl derivatives resume` |
| 保险基金不足 | 结算停放、对账两项非零 | 管理后台发起注资（双人审批）或 `exchangectl ledger insurance-fund`，之后自动重试 |
| 资金费轮次卡住 | `exchangectl derivatives funding` 非零退出 | 查 market-data-service 是否结算了该周期的费率（`/v1/market/{symbol}/funding-rates`）；2 小时后自动跳过 |
| 对账不平但无停放 | 说明计划或账本有缺陷 | P1：停开关 `derivatives.trading`，按 `reconciliation_runs.details` 与成交记录排查 |
