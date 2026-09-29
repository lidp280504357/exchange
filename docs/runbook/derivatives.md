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
  - 保证金：按手预留 `价格 × lot / 杠杆` 与 `价格 × lot × taker 费率`（各自向上取整），合计不超过"可用余额减去全仓未实现亏损"（`DERIV_INSUFFICIENT_MARGIN`）。预留经账本冻结（`ORDER_FREEZE`，键 `order:<订单ID>`）；冻结结果不明时订单停在 `PENDING`，恢复循环每 5 秒用同一个键重试。
- **平仓单**（`reduce_only`，或双向模式的 SELL LONG / BUY SHORT）不预留任何东西、不查资格与最小名义价值，但数量不能超过该仓位还没被其他平仓单占用的部分（`DERIV_REDUCE_ONLY_REJECTED`）。
- 市价单：以"标记价 ± 价格带"（买单向下、卖单向上取到 tick）为价格的 IOC（默认）或 FOK 限价单交给引擎，预留也按这个价格算。
- 发给引擎的命令（`derivatives.order.commands`）手续费率为 0：合约手续费由合约服务按 USDT 计。订单受理与拒绝（`OrderAccepted`/`OrderRejected`）和引擎的订单事件一起在 `derivatives.order.events` 上。

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

## 只减仓（降级）

market-data-service 连续 10 秒算不出某合约的标记价时发 `risk.events` 的 `SystemDegraded`，合约服务（消费组 `derivatives-service-risk`）把该合约置为只减仓（`derivatives.contract_states`），只收平仓单。标记价恢复后也**不会**自动解除，需人工确认：

```bash
ssh exchange sudo docker exec exchange-infra-derivatives-service-1 /app/exchangectl derivatives states
ssh exchange sudo docker exec exchange-infra-derivatives-service-1 /app/exchangectl derivatives resume BTC-USDT-PERP
```

## 对账（不变量 6）

每小时（`RECONCILE_INTERVAL`，启动 1 分钟后先跑一次），持有成交处理锁，结果写 `derivatives.reconciliation_runs`，指标 `derivatives_reconcile_mismatches{check}`，告警 `DerivativesReconciliationMismatch`：

| 检查 | 含义 |
|---|---|
| `POSITIONS_BALANCED` | 每个合约多头总量 = 空头总量 |
| `PNL_CLEARING_MATCHES_POSITIONS` | 账本 `PNL_CLEARING`（USDT）+ Σ多头成本 − Σ空头成本 = 0 |
| `NO_PENDING_SETTLEMENTS` | 没有等账本记账的结算 |

手工：`exchangectl derivatives reconcile`（直接读两边的库，成交在途时可能有瞬时差异）。账本侧另有 `FUNDING_BATCHES_BALANCED`、`PNL_CLEARING_ONLY_PNL`（见 [ledger.md](ledger.md#对账)）。

## 接口

REST（经网关 `/v1/derivatives/*`，需登录）：

| 路径 | 内容 |
|---|---|
| `GET /v1/derivatives/account` | FUTURES 余额、挂单预留、仓位保证金、未实现盈亏（全部 / 全仓）、保证金余额、可转出 |
| `GET/PUT /v1/derivatives/settings/{symbol}` | 持仓模式、保证金模式、杠杆 |
| `GET /v1/derivatives/positions?symbol=` | 未平仓位：数量（带符号）、开仓均价、标记价、名义价值、未实现盈亏、保证金、维持保证金、逐仓预估强平价、已实现盈亏、资金费 |
| `POST /v1/derivatives/positions/{symbol}/margin` | 逐仓追加 / 减少保证金 |
| `POST/GET/DELETE /v1/derivatives/orders`、`GET/DELETE /v1/derivatives/orders/{id}` | 下单、订单列表、撤单 |
| `GET /v1/derivatives/fills?symbol=` | 成交（角色、平仓数量、手续费、已实现盈亏、是否强平、是否已记账） |

WebSocket 私有频道：`orders`（合约订单与现货订单同一频道，按 `symbol` 区分）、`fills`（合约成交带 `position_side`、`closed_quantity`、`realized_pnl`，手续费资产 USDT）、`positions`（`event` 为 OPEN、INCREASE、REDUCE、CLOSE、FLIP、MARGIN、FUNDING、LEVERAGE）。

## 测试服设置

- 开关 `derivatives.trading` 默认关闭（ADR-0005），测试服打开：`exchangectl flags set derivatives.trading --on --reason "测试环境开放合约"`。
- 合约在 `deploy/instruments/test.json` 里以 `TRADING` 创建（状态只在创建时取文件里的值，之后用 `exchangectl instruments contract-status` 改）。
- 保险基金用模拟资金注资：`exchangectl ledger insurance-fund --amount 1000000 --reason "测试环境保险基金" --key insurance-seed-1`（ledger-service 容器里执行，需 `ledger.manual_adjustment`）。
- 端到端：`scripts/e2e/contracts.sh`（规格、标记价、资金费率）、`scripts/e2e/derivatives.sh`（两个用户在 ETH-USDT-PERP 上开仓、平仓、转回，最后跑对账）。

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

## 故障与处理

| 情况 | 表现 | 处理 |
|---|---|---|
| 账本不可用 | 下单停在 PENDING、成交批次重试、积压上升 | 恢复后自动继续；冻结与结算都按键幂等 |
| 标记价中断 | 下单报 `DERIV_MARK_PRICE_UNAVAILABLE`；10 秒后合约只减仓 | 查 market-data-service 与参考行情；恢复后 `exchangectl derivatives resume` |
| 保险基金不足 | 结算停放、对账两项非零 | 注资后自动重试 |
| 对账不平但无停放 | 说明计划或账本有缺陷 | P1：停开关 `derivatives.trading`，按 `reconciliation_runs.details` 与成交记录排查 |
