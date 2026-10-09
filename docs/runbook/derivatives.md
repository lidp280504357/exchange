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
- 并发（审查 C69）：触发循环在事务外读出生效的条件单，用户撤销或强平接管可能就在这之后结束了它。所以条件单的每次结束都在用户锁下、只结束仍是 `ACTIVE` 的（存储层 `UPDATE ... AND status = 'ACTIVE'`，不是就答 `domain.ErrConditionalEnded`，后来的结束不会覆盖先到的）；触发下的单与它的 `TRIGGERED` 在同一个事务里（`Request.Conditional`），下单前先在锁下重读条件单，已结束就不下单。撤销（`CancelConditional`）也拿用户锁。触发时已有以条件单 ID 为 `client_order_id` 的单（审查 C75 ③：改之前的两步法在下单与改状态之间崩溃会留下这种"已下单仍 `ACTIVE`"的条件单，之后每秒触发一次、永不结束），在锁下把条件单置 `TRIGGERED` 并指向那张单，不再下第二张。单测 `conditional_test.go`：迟到的两步法留下的单、用户撤销后迟到的触发不下单、强平接管结束的止损不被迟到的触发改成 `FAILED`、存储层不改已结束的、对冲模式下接管只结束被接管那一侧的条件单。

## 强平与 ADL

实施计划 §7.3 任务 7。监控循环每秒按新鲜标记价检查一次（`Monitor`）：

- **计量**：逐仓仓位单独算，保证金余额 = 仓位保证金 + 未实现盈亏；全仓按用户合并算，权益 = 可用余额 + 全仓仓位保证金 + 全仓订单预留 + 全仓未实现盈亏（要向账本查余额；某个全仓合约没有新鲜标记价时本轮不算这个用户）。维持保证金 = 名义价值 × 该档维持保证金率（强平手续费已并入）。
- **预警**：保证金余额 ≤ 1.2 × 维持保证金时发 `LiquidationWarning`（WebSocket `risk` 频道 `event=WARNING`），同一段只发一次，回到 1.3 倍以上才重置（逐仓记在仓位的 `warned_at`，全仓记在 `derivatives.cross_accounts`）。
- **接管**：保证金余额 ≤ 维持保证金时，撤掉相关挂单（逐仓：该仓位的挂单；全仓：该用户所有全仓挂单；用户自己的与止盈止损触发下的都撤，审查 C65 ②；全仓账户的一张单属于它的每个全仓仓位，只撤一次，审查 C67 ②），该仓位上还没触发的止盈止损一并结束（`CANCELED`、原因 `LIQUIDATION`，审查 C67 ④；之前它们一直 `ACTIVE`，要等触发价穿过才以 `FAILED`/`NO_POSITION` 结束），仓位标为 `liquidating`，发 `LiquidationStarted`（`risk` 频道 `event=STARTED`）。此后用户不能再对该仓位下单、调保证金或杠杆（`DERIV_POSITION_LIQUIDATING`）。
  - 全仓接管时在同一事务里记一条全仓强平（`derivatives.cross_liquidations`，迁移 derivatives 00007，状态 `OPEN`，每个用户每个结算资产最多一条进行中）：触发时账户持有的钱（`balance` = 账本的可用余额 + 该资产全仓挂单的未释放预留 + 被接管仓位的保证金）与触发时权益（`equity`，强平清算费的上限）。此后该账户每笔全仓成交（强平单、ADL，以及接管前已请求撤销、却在引擎收到撤单前成交的挂单）与全仓仓位的资金费都把用户实际的收支（已实现盈亏 + 保险基金补的 − 实收手续费；资金费按账本实际收付）累加到 `flows`；接管时撤单还没到引擎、之后成交开出或加大的全仓仓位当场一并接管（`liquidating`，日志带 `reason=filled while its cross account is liquidated`），否则没人平它、这次强平永远结束不了。
  - 全仓强平进行中（通常几秒；币安的 "user in liquidation mode"），该结算资产的合约账户不再拿出可用余额：不收全仓单（平仓单也不收，仓位都在强平中）、不收任何开仓单（逐仓也不收）、不能追加逐仓保证金、不能从合约账户划出该资产（账本划转前向合约服务查全仓未实现盈亏时被拒），都答 409 `DERIV_POSITION_LIQUIDATING`，`details` 带 `settle_asset`、`liquidation_id`。逐仓仓位的平仓单、减少逐仓保证金、划入照常；这些放回或转入可用余额的钱仍归用户（不进 `balance`/`flows`）。全仓仓位全部平掉后、清算费记账前（至多约 1 秒）也是如此。
- **强平单**：每秒检查，没有进行中的强平单就下一张 IOC 限价单（`kind=LIQUIDATION`，平掉剩余数量）：逐仓以破产价、全仓以标记价为基准，向不利方向偏 0.5%（`domain.Slippage`），取到 tick。成交按强平结算：逐仓亏损最多到保证金、剩余保证金进保险基金（`INSURANCE_CONTRIBUTION`）、缺口由保险基金补，分录类型 `LIQUIDATION_SETTLE`；全仓亏损从可用余额付，不够的由保险基金补。每次成交发 `LiquidationFilled`（`event=LIQUIDATED`）。
  - HOUSE 报价的合约上强平单也带 `house_only`，对手方只有 HOUSE。HOUSE 的合约仓位合计到了权益的 `HOUSE_CONTRACT_LEVERAGE` 倍（或该方向到了单合约上限）后只减仓：被强平的用户若与 HOUSE 站在同一侧（例如都多头，强平要卖、HOUSE 不再买入），HOUSE 那一侧没有报价，强平单成交不了，重试 3 次后转 ADL、缺口由保险基金补。这是"HOUSE 永不被强平、用户之间不撮合"（ADR-0015）的必然结果；告警 `HouseContractEquityGone` 与指标 `market_house_contract_exposure_usdt` 提示 HOUSE 快到上限，补 HOUSE 合约保证金（`exchangectl ledger house-margin`）能让它恢复接单。
- **ADL**：强平单下了 3 次（`MaxLiquidationAttempts`）仍没平完，剩余部分对对手方执行自动减仓：同合约反方向、未在强平中的其他用户仓位按"盈利率（未实现盈亏 / 入场成本）× 有效杠杆（名义价值 / 保证金余额）"从高到低排队，按破产价（逐仓）或标记价（全仓）在订单簿之外成交，无手续费，分录类型 `ADL_SETTLE`。被减仓的一方收到 `AdlExecuted`（`event=ADL`）。从某个对手仓位里取之前，先撤该对手在这一方向上的全部平仓单（用户自己的与止盈止损触发下的；审查 C65 ②，币安的做法），免得仓位缩小后它们成交开出反向仓。
- 仓位平完后 `liquidating`、预警标记自动清除。
- **强平清算费**（全仓，审查 C68，用户决定 2026-10-09，币安的做法）：逐仓强平时仓位剩下的保证金本来就进保险基金（上面的"强平单"）；全仓强平完成后，账户这次强平剩下的钱也划入保险基金，账户归零。强平循环每秒检查进行中的全仓强平（`SettleCrossLiquidations`）：该资产的合约上没有未平的全仓仓位、没有生效或还占着预留的全仓订单、该用户没有等账本重试的结算（`pending_settlements`）时算作完成。清算费 = min（`balance` + `flows`，触发时权益，此刻的可用余额），按结算资产精度向下取，不为正时为 0；先存进这条记录（`fee`），再以账本 `SettleFutures` 的单个 `INSURANCE` 动作记账（分录 `INSURANCE_CONTRIBUTION`，幂等键 `cross-liquidation:<强平ID>`），然后记为 `DONE` 并发 `CrossLiquidationCompleted`（`derivatives.liquidation.events`，带 `clearance_fee`）。记账应答丢失时下一轮按存下的同一金额、同一个键重记（账本回放第一次的结果，与此时余额无关）；账本拒绝（可用余额少于清算费）时不入账，清空 `fee` 下一轮重算。强平期间转入的钱、逐仓放回的保证金不在 `balance` + `flows` 里，清算费拿不到（上限还有此刻的可用余额）。预估强平价不变（清算费只拿强平后剩下的钱）。
  - 通知：接管时的 `CONTRACT_LIQUIDATING` 写明"强平完成后，剩余保证金将作为强平清算费划入保险基金"（逐仓写"该仓位剩余的保证金将作为强平清算费划入保险基金"）；完成时发 `CONTRACT_LIQUIDATED`（站内信 + 邮件，三语）："全仓仓位已全部平仓，剩余保证金 X 已作为强平清算费划入保险基金"，清算费为 0 时写"账户没有剩余保证金（超出账户的亏损由保险基金承担）"。清算费在用户的资金流水里是一条 `INSURANCE_CONTRIBUTION`（逐仓强平的剩余保证金也是这个分录）；两站的显示名「强平清算费」与强平记录里的清算费由合约数据前端会话做（F24）。网关对 `CrossLiquidationCompleted` 不推 `risk` 频道（通知走 `notifications`）。
  - 等账本重试的结算存在时强平不算完成。保险基金不足停放的全仓成交先按"假定结果"记 `flows`（保险基金补 0、可免的手续费全免）；恢复循环重试记账成功时，在同一事务里把实际结果与假定的差（保险基金补的、账本实际免掉的手续费与假定的差）加进这次强平的 `flows`（审查 C74 ①，`domain.ParkedFlow`，单测 `TestAParkedCrossFillCountsWhatTheFundPaid`：对冲的另一侧之后平仓赚回来的钱因此照样进清算费）。接管之前就已停放的成交不补：记强平时账本本来就落后于仓位（接管时保险基金已经不足），这种情况按事故处理，补足保险基金后核对该用户的清算费。
  - 账户摘要 `GET /v1/derivatives/account` 带 `liquidating`（审查 C74 ④）：该结算资产的全仓强平进行中为 `true`，此时 `transferable` 为 0。
  - 指标 `derivatives_cross_liquidation_open_oldest_seconds`：进行中最久的那条全仓强平已经多少秒（没有为 0，每秒更新）；告警 `DerivativesCrossLiquidationStuck`（超过 300 秒持续 1 分钟，审查 C74 ②）。正常几秒就结束，卡住时按下面"一条全仓强平长时间停在 `OPEN`"查。

指标：`derivatives_liquidation_steps_total{step}`（warning、takeover、order、adl、cleared：全仓强平完成、清算费已记）。日志：`position taken over for liquidation`、`position auto-deleveraged`、`cross account liquidation started`（带 `balance`、`equity`）、`cross account liquidation over`（带 `left`、`clearance_fee`）、`cross liquidation fee refused; worked out again`，强平循环的 `cross liquidation fees not booked`（记账失败，下一秒重试）。

```sql
SELECT user_id, symbol, position_side, quantity, margin, liquidating, liquidation_attempts, liquidation_at, warned_at
FROM derivatives.positions WHERE liquidating OR warned_at IS NOT NULL;
SELECT order_id, user_id, side, price, quantity, status, filled_quantity FROM derivatives.orders WHERE kind IN ('LIQUIDATION', 'ADL') ORDER BY order_id DESC LIMIT 20;
-- 全仓强平：进行中的与最近完成的（left = balance + flows）
SELECT liquidation_id, user_id, asset, started_at, equity, balance, flows, balance + flows AS left, status, fee, done_at
FROM derivatives.cross_liquidations ORDER BY started_at DESC LIMIT 20;
```

一条全仓强平长时间停在 `OPEN`：看该用户该资产还有哪些全仓仓位没平（应当都在 `liquidating`，强平单与 ADL 的进度见上面两条查询）、是否还有全仓订单在等引擎确认撤单或释放预留（`derivatives.orders` 里该用户 `margin_mode = 'CROSS'` 的生效或未释放订单）、`pending_settlements` 是否有该用户的停放结算（保险基金不足，见下文"账本拒绝"）；日志 `cross liquidation fees not booked` 给出记账失败的原因。进行中的强平让该账户不能开仓、不能划出，处理前先在后台用户页确认情况。

## 只减仓（降级）

两种情况会把合约置为只减仓（`derivatives.contract_states`），只收平仓单：

- market-data-service 连续 10 秒算不出该合约的标记价（价源不足），发 `risk.events` 的 `SystemDegraded`，合约服务的消费组 `derivatives-service-risk` 收到后置位；
- 合约服务自己发现：有未平仓位的合约，标记价已超过 10 秒没更新（服务启动 30 秒后才判断），原因 `MARK_PRICE_STALE`。

"风控服务不可用"这一条暂不适用：合约服务不依赖 risk-service 做同步检查。标记价恢复后也**不会**自动解除（指标 `derivatives_contract_reduce_only_mark_fresh{symbol}` 在只减仓且标记价新鲜时为 1，恢复循环每 5 秒更新；告警 `DerivativesReduceOnlyWithFreshMark` 在持续 10 分钟时提醒去解除，审查 C70：2026-10-07 现货关闭约 30 秒让平台币永续只减仓了两天才被发现），需人工确认：管理后台「合约」页（权限 `derivatives.write`，标记价未恢复时按钮不可用，动作有审计事件 `admin.derivatives.reduce_only_lifted`），或命令行：

```bash
ssh exchange sudo docker exec exchange-infra-derivatives-service-1 /app/exchangectl derivatives states
ssh exchange sudo docker exec exchange-infra-derivatives-service-1 /app/exchangectl derivatives resume BTC-USDT-PERP
```

故障注入 `scripts/fault/contract-degrade.sh` 演练整个过程：切断 market-data-service 的外网 → 标记价报 `degraded` → 合约只减仓、开仓单被拒 → 恢复外网后仍只减仓（开仓单报 `DERIV_REDUCE_ONLY_MODE`）→ `resume` 后恢复。`scripts/fault/coinm-degrade.sh` 在币本位的 BTC-USD-PERP 上演练同一过程（BTC 保证金、整张开仓，合约不在 TRADING 时跳过）。

部署会重启 market-data-service，标记价可能中断超过 10 秒，合约因此进入只减仓（2026-10-01 有一次 ETH-USDT-PERP 停在只减仓几个小时，直到端到端测试失败才发现）。`deploy/server-update.sh` 最后等 20 秒，解除部署期间开始、原因为 `INDEX_SOURCES` 或 `MARK_PRICE_STALE` 的只减仓，解除人记为 `deploy-<版本>`（`exchangectl` 读 `EXCHANGECTL_ACTOR`）；价源若真断了，10 秒后又会只减仓。端到端脚本自己造成的同样自己解除：`scripts/e2e/trading.sh` 关闭现货片刻会让平台币永续只减仓，结束时用同一口径解除关闭以后开始的（`lift_reduce_only`，解除人 `e2e-trading.sh`，B165）。其余部署之外开始的只减仓仍须人工解除（后台重开产品线时列出关闭期间开始的并可一键解除是 A92）。

## 产品线开关（产品线开关设计 2026-10-07，批次 K1b）

两条合约产品线各一个全局开关：U 本位 `product.usdt_m`、币本位 `product.coin_m`（迁移 config 00002 写入为开；没存的按开算，见 [feature-flags.md](feature-flags.md)）。合约属于哪条线按规格判：币本位（`contract_size` 为正）属 `coin_m`，其余属 `usdt_m`。服务每 5 秒重读开关（`flags.RefreshInterval`）。与按合约的只减仓、合约状态和资格开关是叠加关系。

关闭后（`application/products.go`）：

- 只收平仓单与撤单：平仓单是只减仓单，以及对冲持仓模式下平所在方向仓位的单（卖出平多、买入平空）；强平、ADL、止盈止损触发的单与后台平仓都属此类。其余（开仓单）答 403 `PRODUCT_CLOSED`（`details.product` 为 `usdt_m` 或 `coin_m`）；新建止盈止损也答 `PRODUCT_CLOSED`（关闭时它们都被撤了，已有仓位直接下只减仓单平）。单向持仓模式下平仓要带 `reduce_only`，不带的单按开仓单拒。单测 `TestAClosedProductLineTakesOnlyCloses`、`TestAClosedLineTakesEveryKindOfClose`（对冲平多、止盈触发、后台平仓，审查 C60）。
- 仓位、资金费、强平、ADL 与对账照常；HOUSE 照常报价（所有成交都对 HOUSE，平仓单与强平单只能和它成交；开仓单进不来，HOUSE 的报价只会被平仓与强平用到；设计稿 §1 #3，协调会话 16:48 更正）。
- 做市账户（`MARKET_MAKER_USER_IDS`，模拟市场的机器人）不受开关限制：平台币永续没有 HOUSE 报价，机器人的挂单是平仓的对手方。
- 后台关闭时调 `POST /internal/products/{usdt_m|coin_m}/cancel-open`（`{actor, reason}`）：先重读开关（读不到答 503，还开着答 409 `COMMON_CONFLICT`），再按用户逐个加锁、撤销该线合约上的全部用户挂单（强平、ADL、后台平仓单不撤，做市账户的不撤）与生效中的止盈止损（状态 `CANCELED`、原因 `PRODUCT_CLOSED`），每单在同一事务里发审计 `admin.orders.canceled`（`Target` 为 `user:<id>`、`Actor` 为调用方给的 `actor`、`Details` 含 `order_id`、`symbol`、`product`、`type`）；答 202 `{canceled, orders: [{order_id, user_id, symbol, type: ORDER|CONDITIONAL}]}`，挂单由引擎确认撤销。某个用户的撤单失败（如拿不到锁）不挡其他用户，全部走完后答 503 `COMMON_UNAVAILABLE`，`details` 带已撤的 `canceled` 与失败的 `failed_users`（审查 C60，单测 `TestClosingGoesOnPastAUserItCannotLock`）；重复调用只撤此刻还开着的，所以重试即可。生效订单按交易对读 `orders_active` 部分索引（首列是用户，按交易对查要整段读，只含生效订单），止盈止损按交易对读 `conditional_orders_active`；撤单接口与兜底只在关闭的线上读，计数接口在后台「产品线」卡打开时每 15 秒读一次（审查 C63 ②）。内部接口带调用方 `X-User-Id`（经网关来的）的请求一律 404，与 spot-trading-service 一致（审查 C63 ①）。
- 兜底：恢复循环每 5 秒检查关闭的线，撤掉关闭前 30 秒起新建的开仓挂单与止盈止损（关闭那一刻正在下的单，读开关时还没变；开关的时间是库的时钟、订单的是本服务的，30 秒里除了开关最多 5 秒的滞后都是给两者时钟差的余量，审查 C60），审计的 `actor` 为 `system:derivatives-service`；关闭之后下的平仓单不动，更早挂着的由上面的接口撤。兜底可能先于后台的调用撤掉这 30 秒内新建的单（2026-10-07 端到端里出现过），这些不在 cancel-open 的回答与后台的撤单数里，审计里有。
- 仓位缩小后多出来的平仓单（审查 C60 ④、C62）：平仓单下单时按仓位限量，但挂着时仓位可能被 ADL、强平或一笔反方向成交缩小；每笔成交记账后，服务在同一事务里按成交后的仓位检查该用户这个合约上的平仓单（用户的、止盈止损触发的；强平、ADL、后台平仓单不算），按下单先后保留放得下的，撤掉放不下的（`domain.BeyondPosition`，单测 `TestClosingOrdersBeyondTheirPosition`、`TestADLCancelsTheClosingOrdersItLeavesNoRoomFor`、`TestAFillTheOtherWayTrimsTheClosingOrders`）；强平接管与 ADL 还在动仓位之前先撤平仓单（见「强平与 ADL」，审查 C65 ②，单测 `TestATakeOverCancelsATakeProfitsOrder`、`TestADLCancelsTheCounterpartysClosingOrdersFirst`）。撤单到达引擎之前已成交的仍会开出反向仓：引擎不知道仓位，成交的两边（对 HOUSE 也是）都已按成交量记账，没法在记账时封顶，`domain.PlanFill` 把超出部分按可用余额开仓（数量不超过下单时的仓位）。
- `GET /internal/products/{usdt_m|coin_m}` → `{product, closed, open_orders, open_positions}`：关闭会撤的挂单数（含止盈止损）与会保留的持仓数，HOUSE 与做市账户不计（后台「产品线」卡用）。
- 重新打开即恢复下单；关闭期间撤掉的挂单不回放。

端到端：`scripts/e2e/derivatives.sh` 与 `coinm.sh` 各有一步把线关上片刻（`scripts/e2e/lib/products.sh`：关开关、调 cancel-open；脚本结束时无论成败都重新打开），核对挂单与止盈止损被撤、开仓与新止盈止损被拒、持仓用户的只减仓单对 HOUSE 成交、重开后开仓单被收。测试服上这会撤掉其他用户在该线上的挂单（做市账户的除外）。

## 币本位合约（反向合约，币本位设计 2026-10-06 §2，批次 G1）

- 规格（G0）：`margin_type` 为 `COIN`、`settle_asset` 为基础资产（BTC、ETH、ASTRA）、`contract_size` 为面值（美元：BTC 100、其它 10），以 USD 计价。服务读 instrument-service 的全部合约（`margin_type=ALL`），金额精度取结算资产的（BTC、ETH 8 位）。
- 账户：每个结算资产一个 `FUTURES` 账户（USDT、BTC、ETH、ASTRA），冻结、结算、资金费、保证金划转、全仓权益与全仓预警都按（用户, 结算资产）分开；`GET /v1/derivatives/account?asset=BTC` 看该资产的账户（缺省 USDT，不是任何合约结算资产的为 `DERIV_SETTLE_ASSET_MISMATCH`）；全仓预警记在 `cross_accounts`（迁移 derivatives 00006 起主键为（用户, 资产））。划入 FUTURES 用现有划转按资产划。
- 下单：数量是整数张（有小数为 `DERIV_CONTRACTS_NOT_INTEGER`），价格是美元；开仓还要资格 `COIN_M_TRADE`（开关 `derivatives.coin_m`，按用户或地区，关着为 `USER_NOT_ELIGIBLE`）。买单按 min(限价, 标记价) 预留、卖单按限价（价格越低占用的币越多，与 U 本位相反）。
- 公式（`domain/contract.go`、`position.go`）：价值 V(P) = 张数 × 面值 ÷ P；每笔成交的币价值按结算资产精度四舍五入一次，双方同值，成本、已实现盈亏、`PNL_CLEARING` 都用它；未实现盈亏多头 = 成本 − V(P)，空头取反；维持保证金 = V(P) × 维持率 − 抵扣（档位按币计的名义价值，与币安 COIN-M 的 `qtyCap` 同口径）；逐仓强平价多头 Q×S×(1+r) ÷ (M + D + 成本)，空头 Q×S×(1−r) ÷ (成本 − M − D)（分母 ≤ 0 时没有强平价，破产价同理，强平单与 ADL 以标记价兜底）；资金费 = |张数| × 面值 × |费率| ÷ 标记价（付方向上、收方向下）；手续费按未舍入的币价值 × 费率向上。
- 不变量 6 按资产：反向合约的成本方向相反，`PNL_CLEARING` + U 本位 Σ多头成本 − Σ空头成本 − 币本位 Σ多头成本 + Σ空头成本 = 0。
- 结算：账本 `SettleFutures` 按资产；HOUSE 的 BTC/ETH 不能为负，亏损超过余额的部分由同一资产的保险基金补，基金不够时整笔拒绝、不入账（账本集成测试 `TestCoinSettledFutures`）。保险基金与杠杆交易共用 `INSURANCE_FUND` 的同一行（协调会话 20:45 决定 ⑦）。
- 事件与推送：`Position`、`FillSettled` 带 `settle_asset`、`contract_size`，`LiquidationWarning`、`LiquidationFilled`、`AdlExecuted` 带 `settle_asset`（全仓预警的是该账户的资产），`AdlExecuted` 还带 `contract_size`（币本位为面值，线性为空，与 `FillSettled` 同）；三者都带 `direction`（`LONG`/`SHORT`，审查 FS C49：单向持仓模式下 `position_side` 恒为 `BOTH`，看不出方向）——预警按仓位数量的正负（全仓预警为空），强平成交与 ADL 按所平仓位（卖出平的是多头）；发给引擎的 `PlaceOrder` 带两者，引擎的 `TradeExecuted` 也带，币本位成交的 `quote_quantity` 是张数 × 面值（美元），订单的 `filled_quote` 仍是价格 × 数量之和（平均价由它算）。网关的 `orders`（受理时）、`fills`、`positions`、`risk` 推送带 `settle_asset`，合约成交的 `fee_asset` 是结算资产。ClickHouse 的 `trades`、`derivatives_positions`、`derivatives_fills`、`derivatives_funding` 记 `settle_asset`，币本位成交的 `notional` 是美元价值。
- REST：仓位多了 `settle_asset`、`contracts`（币本位的有符号张数，线性为 null）、`value_coin`（币本位按标记价的币价值）、`value_usd`（币本位张数 × 面值，线性为名义价值）；订单、成交、资金费记录带 `settle_asset`。
- 测试服（C39，2026-10-06）：BTC-USD-PERP、ETH-USD-PERP 在 `house.sh seed`（HOUSE 的 BTC 6、ETH 200 合约保证金，保险基金 BTC +2、ETH +40、ASTRA 100,000）与 `house.sh flags` 之后转 TRADING；两站在 G4 之前不列币本位合约（合约列表缺省只给 U 本位），只有端到端账户在交易。ASTRA-USD-PERP 没有币安参考、仍是 PREPARE。
- 端到端 `scripts/e2e/coinm.sh`（新用户买 BTC、转 0.002 BTC 到合约账户，`?asset=BTC` 的账户与两个错误码，市价开 3 张多单、只减仓平掉，核对仓位字段、按 BTC 的盈亏与手续费、余额与转回，最后对账）；故障注入 `scripts/fault/coinm-degrade.sh`（见上节）。两者在合约不在 TRADING 时跳过。强平与 ADL 的端到端（审查 EX，上线前条件）是演练 `scripts/fault/coinm-liquidation.sh`：币安价格驱动的 BTC/ETH 合约上无法稳定触发，放在机器人做市、能用运营调价的 ASTRA-USD-PERP 上（压低 4% 让 25 倍逐仓多头被强平给机器人，核对保险基金按成交记的赔付变动；机器人离开后再压低 4%，15 倍多头三次强平单无人接、按破产价 ADL；一个 25 倍全仓多头被强平后核对强平清算费、资金流水与账户归零（审查 C68）；结束时价格、开关、仓位都复原，见 [testing.md](testing.md)）。

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
| `GET /internal/derivatives/risk` | 被接管、已预警或保证金率（维持保证金 ÷（保证金 + 未实现盈亏））≥ 0.5 的仓位，风险高的在前；全仓仓位在这里按单个仓位计算。`user_ids` 或 `exclude_user_ids`（审查 L3，后台默认只看真人：只要这些用户的、或除这些用户以外的；逗号分隔或重复给出，最多 1,000 个 UUID，两者只能给一个，否则答 400；`user_ids=` 给空即谁的都不要） |
| `GET /internal/derivatives/positions` | 全部用户的持仓（后台「仓位」页，2026-10-02 C3）：`symbol`、`user_id`（按用户只读这个用户的仓位）、`user_ids` 或 `exclude_user_ids`（同上一行，审查 L3，在库里按 `= ANY` / `<> ALL` 筛）、`watch=true`（只要上一行的风险仓位，另加全仓账户被预警的全仓仓位；不含 HOUSE；全仓账户的预警在所有视图里都算进它的全仓仓位的 `warned_at`，C5.5 ⑱）、`limit`（默认 200，超过 1000 按 1000）；按保证金率、再按开仓名义价值从大到小，HOUSE 的仓位排在最后（HOUSE 的单按最高杠杆记保证金，零盈亏时保证金率就约 50%，不排后会占满"最危险"），`truncated` 表示被 `limit` 截断；每行带 `mark_fresh`，标记价不新鲜时盈亏与保证金率停在上一个标记价（C5.5 ⑨） |
| `POST /internal/derivatives/positions/close` | 后台强制平仓（C2）：先撤该用户在这个合约上的全部挂单，再以 `ADMIN` 类型的市价只减仓单平掉；HOUSE 不平 |
| `GET /internal/derivatives/users/{id}/cross-margin?debit=&asset=` | 后台调账预览（C5.5 ⑧）：该结算资产（缺省 USDT）全仓账户的权益、维持保证金与状态，以及扣减 `debit` 后的权益与状态（`HEALTHY`/`WARNING`/`LIQUIDATE`） |
| `POST /internal/derivatives/contracts/{symbol}/tier-impact` | 新风险阶梯的影响（后台预览，2026-10-02 设计 §2 第 6 条）：`{risk_tiers}`，按保证金监控的规则（逐仓看仓位、全仓看整个账户，HOUSE 不计）算出会新被强平的仓位数、名义价值与账户数，新进入预警、超出杠杆风险限额、没有新鲜标记价的数量，以及最大的 20 个例子；阶梯不合法答 400，不改任何东西；全仓账户逐个向账本查余额，最多量 2000 个、8 秒（后台等 10 秒），剩下的仓位计入「没有新鲜标记价」那一项（`unmeasured`），后台因此不给确认（C5.5 ⑩） |
| `GET /internal/products/{usdt_m\|coin_m}` | 产品线的开关状态与关闭会撤的挂单数、会保留的持仓数（见「产品线开关」） |
| `POST /internal/products/{usdt_m\|coin_m}/cancel-open` | 后台关闭产品线时撤销其全部用户挂单与止盈止损，逐单审计（见「产品线开关」） |
| `POST /internal/derivatives/contracts/{symbol}/price-impact` | 某个标记价的影响（后台"模拟市场"价格事件的确认框，ASTRA 设计 §6.3）：`{target_price}` → `positions`、`liquidated`（标记价到这里会新被强平的仓位数；全仓账户整个算进去）、`notional`（按目标价）、`accounts`、`insurance_cost`（按目标价平掉时逐仓保证金或全仓权益低于 0 的部分，即保险基金预计承担）、`unmeasured`（现在没有新鲜标记价、量不了的仓位）与最大的 20 个例子（保证金余额按目标价，维持保证金是现在与目标价两个）；与阶梯影响同一套保证金监控算法与上限（2000 个全仓账户、8 秒），HOUSE 不计，不改任何东西；目标价不是正数答 400 |

WebSocket 私有频道：`orders`（合约订单与现货订单同一频道，按 `symbol` 区分）、`fills`（合约成交带 `position_side`、`closed_quantity`、`realized_pnl`，手续费资产 USDT）、`positions`（`event` 为 OPEN、INCREASE、REDUCE、CLOSE、FLIP、MARGIN、FUNDING、LEVERAGE）、`risk`（`event` 为 WARNING、STARTED、LIQUIDATED、ADL）。

## 测试服设置

- 开关 `derivatives.trading` 默认关闭（ADR-0005），测试服打开：`exchangectl flags set derivatives.trading --on --reason "测试环境开放合约"`。
- 合约在 `deploy/instruments/test.json` 里以 `TRADING` 创建（状态只在创建时取文件里的值，之后用 `exchangectl instruments contract-status` 改）。
- 风险限额按币安的真实分档（用户决定 2026-10-09「全部参考币安」，B168；`deploy/instruments/test.json` 的 `risk_tiers`，由 `gen-contracts.go` 从币安网站公开的档位生成，每个跟随币安的合约用它自己的那份）：每档名义价值上限、最高杠杆、维持保证金率，累计抵扣额按档位连续推出（`maintenanceAmount`，与币安的 `cumFastMaintenanceAmount` 同一算法）；最高杠杆也按币安，就是该合约第一档的（用户决定 2026-10-10「全部按照币安来」，B171，取代 10-02 的「最高 125 倍」）：BTC-USDT-PERP、ETH-USDT-PERP 为 150 倍，其余按各自币安第一档；instrument-service 与 derivatives-service 的档位校验只留 150 作防呆（`LeverageCap`），调杠杆超过合约第一档答 `DERIV_LEVERAGE_EXCEEDED`（details `max_leverage`）。BTC-USDT-PERP：≤ 300,000 USDT 150 倍、0.4%；≤ 800,000 为 100 倍、0.5%；≤ 3,000,000 为 75 倍、0.65%；≤ 12,000,000 为 50 倍、1%；≤ 70,000,000 为 25 倍、2%；≤ 100,000,000 为 20 倍、2.5%；再往上 10、5、4、3、2、1 倍，到 1,800,000,000 为 1 倍、50%，共 12 档；ETH-USDT-PERP 同样 12 档，第 5 档起上限较低（到 1,200,000,000）。以前（2026-10-02）这两个合约是手写的 7 档缩小版（125 倍只到 50,000 USDT，10 万 USDT 保证金 100 倍只能开约 3 BTC），换成币安的以后在测试服实际持仓的名义价值上维持保证金都不高于原来（换档前用 `tier-impact` 核对：BTC 4 个、ETH 3 个持仓，新强平 0）。ASTRA 两个永续没有币安参考，沿用原来的 7 档。杠杆对话框的上限、首页"合约最高杠杆"与规格接口都从这里读，不写死；强平价按所在档位的维持保证金率算（`internal/derivatives/domain/risk_test.go` 的 `TestTheLadderAt125x`）。超出风险限额的错误 `DERIV_RISK_LIMIT_EXCEEDED` 带 `max_notional`、`leverage` 与 `notional`（下单后或调杠杆时该方向的名义价值，按标记价），前端据此说出具体数字；PC 与手机的下单表单分别显示"可开多 / 可开空"（保证金与风险限额两者取小，`packages/core` 的 `riskRoom`、`sideExposure`），提交前就用同样的规则检查（`checkRiskLimit`）。
- 流动性：两个合约都由 HOUSE 按币安合约盘口提供（开关 `market.house_liquidity` 允许全部合约，见 [market-maker.md](market-maker.md)）。`HOUSE_USER_ID` 沿用原做市账户，其 FUTURES 账户由 `scripts/ops/house.sh seed` 注资到 2,000,000 USDT（`exchangectl ledger house-margin`）；测试服 HOUSE 在每个合约上多空各最多接 5,000,000 USDT（`HOUSE_CONTRACT_CAP`），到上限后该方向没有报价。阶段 3 的挂单做市（`MARKET_MAKER_CONTRACTS`、开关 `market.maker`）已随 ADR-0015 退役。
- 保险基金用模拟资金注资：`exchangectl ledger insurance-fund --amount 1000000 --reason "测试环境保险基金" --key insurance-seed-1`（ledger-service 容器里执行，需 `ledger.manual_adjustment`）。
- 端到端：`scripts/e2e/contracts.sh`（规格与阶梯：BTC-USDT-PERP 的档位与 `test.json` 一致、币安的 12 档 150 倍到 300,000 USDT，BTC、ETH 最高 150 倍；标记价、资金费率、两个合约的盘口）、`scripts/e2e/derivatives.sh`（两个用户在 ETH-USDT-PERP 上各自与 HOUSE 开多、开空，杠杆到合约第一档为止（从规格读，ETH 150 倍，超一倍答 `DERIV_LEVERAGE_EXCEEDED`），挂单预留与撤单，只减仓市价平仓，按成交核对盈亏、手续费与余额，转回，最后跑对账）、`scripts/e2e/house.sh` 的合约部分（两个合约各开平一次）、`scripts/e2e/funding.sh`（资金费，见下）、`scripts/e2e/admin.sh` 的合约部分（合约状态、只减仓、状态往返、强平监控、双人审批的保险基金注资）；故障注入 `scripts/fault/contract-degrade.sh`（降级与人工解除）。
- 资金费端到端靠一对**常驻对冲仓位**：`funding.sh` 第一次运行时注册两个用户，各转 100 USDT 到合约账户，在 ETH-USDT-PERP 上用市价单分别与 HOUSE 开多、开空 0.01 张后保持不平（B4 之前开的那一对在 BTC-USDT-PERP 上 0.001 张，状态文件没有合约名时按它处理），邮箱与随机密码记在本机 `~/.cache/exchange-e2e/`（`E2E_STATE_DIR` 可改，不进仓库）；之后每次运行登录这两个用户（超过 7 天未登录时从开发收件箱取登录挑战验证码），逐个检查开仓以来每个资金费时间点：双方都有记录、费率等于 market-data-service 结算的费率、付款方付 仓位数量 × 结算标记价 × |费率| 向上取整、收款方向下取整。仓位没了（被平或被减仓）就重新开一对。强平本身依赖真实价格波动，端到端无法稳定触发，由应用层测试覆盖（`internal/derivatives/application` 的强平、ADL、全仓强平用例，设置 `TEST_POSTGRES_DSN` 时在真实库上跑；强平清算费见 `crossliquidation_test.go`：账户归零而强平期间转入与逐仓放回的钱留给用户、什么都没剩时不收、记账应答丢失按同一金额重记、被拒重算、强平期间不收开仓单与追加保证金、接管后才成交的挂单一并接管、资金费计入）。例外是 `scripts/e2e/price-event.sh`：带风控的价格事件把 BTC-USDT-PERP 的标记价抬高（目标 16%，HOUSE 亏损上限会把它压小，2026-10-10 那次是 1.45%），脚本事先开一个 ETH-USDT-PERP 的 20 倍逐仓多单，再用约 1 USDT 开一个 100 倍全仓空单，事件期间每半秒请求把 1000 USDT 划出合约账户（多于账户余额，什么都不会划走）；之后核对空单被强平、强平记录 `DONE`、清算费 = min（剩下的，触发时权益）且与通知和资金流水的 `INSURANCE_CONTRIBUTION` 一致、可用余额是清算费之外剩下的、冻结的正是逐仓多单的保证金且多单还在、`liquidating` 已为 `false`，强平期间的划出答 409 `DERIV_POSITION_LIQUIDATING`（强平只有一两秒、两次请求都没落在其中时只记一句说明；有请求落在其中却没被拒则失败）（审查 C68、C74 ③）；事件幅度到不了强平价时把空单平掉、跳过这些；`derivatives.sh`、`coinm.sh` 核对普通平仓不产生清算费。
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
