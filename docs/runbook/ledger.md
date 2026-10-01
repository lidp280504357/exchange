# 账本运维

需求 §5.9、§11.4；ADR-0001（余额只能由分录改变）、ADR-0008（金额十进制）。实现见 `internal/ledger`，契约 `api/proto/exchange/ledger/v1`（gRPC 与 `ledger.events`）、`api/proto/exchange/account/v1`（`account.events`）、`api/openapi/account.yaml`。

## 模型

- 账户 `ledger.accounts`：（所有者类型, 所有者, 账户类型, 资产）唯一。用户只有 `SPOT`、`FUTURES`；系统账户 `FEE_REVENUE`、`INSURANCE_FUND`、`DEPOSIT_PENDING`、`WITHDRAWAL_PENDING`、`UNCLAIMED_DEPOSIT`、`FUNDING_CLEARING`、`MARKET_MAKER`、`GAS_SUPPLY`、`ADJUSTMENT`、`PNL_CLEARING`（合约已实现盈亏的对手方，阶段 3）各资产一个。首次记账时自动建户。
- 分录 `journals` + `journal_lines`：**只追加**（触发器禁止 UPDATE/DELETE/TRUNCATE）；一条 journal 内同一资产的 line 之和为 0（提交时由约束触发器检查）；每条 line 带写入后的 `available_after`/`frozen_after` 与账户版本号。
- 余额非负（不变量 3）由表约束兜底：只有 `DEPOSIT_PENDING`、`ADJUSTMENT`、`PNL_CLEARING` 与 `MARKET_MAKER`（HOUSE 卖出内部资产，ADR-0013，阶段 4 B4）可为负。
- 幂等：每条 journal 有唯一 `idem_key` 与内容摘要；同键同内容返回第一次的结果（`replayed`），同键不同内容返回 409 `COMMON_IDEMPOTENCY_CONFLICT`。gRPC 调用方的键按用户隔离（`u:<user>:<key>`）。
- 并发：一次记账按固定顺序锁住涉及的账户（`SELECT ... FOR UPDATE`），先在内存算完全部余额再写库，余额不足时什么都不写（`LEDGER_INSUFFICIENT_BALANCE`，422）。超精度金额直接拒绝（`LEDGER_AMOUNT_PRECISION`，400），精度来自 instrument-service。
- 每条 journal 发 `ledger.EntryPosted`（含全部 line，ClickHouse `ledger_entries` 由它投影），每个受影响的用户账户发 `ledger.BalanceChanged`（WebSocket `balances` 频道用）。

## 接口

- gRPC `LedgerService`（`ledger-service:9185`）：`Freeze`、`Unfreeze`（`ORDER_*`/`WITHDRAW_*`）、`Transfer`、`GetBalances`，写操作都要幂等键。
- REST（经网关，需登录）：`GET /v1/account/balances`、`POST /v1/account/transfers`（必须带 `Idempotency-Key`）、`GET /v1/account/transfers`、`GET /v1/account/ledger`。
- 划转：现货 ↔ 合约在一个事务里完成（§13 验收 11）。需要功能开关 `account.transfer` 且账户资格允许（user-service `CheckEligibility(TRANSFER)`）；余额不足的划转记为 `FAILED` 并保留，同键重试得到同样的错误；成功/失败分别发 `account.AccountTransferCompleted`/`AccountTransferFailed`。
- 管理后台（2026-10-02 设计 C2）：
  - `Adjust` 可调现货或合约账户（`account_type`，默认 `SPOT`）。
  - **风控冻结**：`PlaceHold`、`ReleaseHold`、`ListHolds`。冻结把用户现货可用余额的一部分转入冻结（分录 `ADMIN_FREEZE`，键 `hold:<冻结单ID>`），解冻原样转回（`ADMIN_UNFREEZE`，键 `hold-release:<ID>`，备注为解冻理由），每张冻结单只能解冻一次（`LEDGER_HOLD_RELEASED`）。冻结单记在 `ledger.holds`（迁移 ledger 00005），两步都在同一事务里写审计 `ledger.hold_placed`/`ledger.hold_released`（操作者为管理员邮箱）。只做现货：合约账户的冻结是保证金，由 derivatives-service 对账。用户的资金流水能看到这两类分录（站内显示为"风控冻结/风控解冻"）。

```sql
SELECT id, user_id, asset, amount, reason, actor, created_at, released_at, released_by FROM ledger.holds WHERE released_at IS NULL ORDER BY created_at DESC;
```

## 模拟资金（阶段 1）

阶段 1 没有链上充值，测试资金来自 `ADJUSTMENT` 对手科目（`MANUAL_ADJUSTMENT` 分录，§11.4）：

- **欢迎资金**：`ledger.welcome_credit` 打开时，ledger-service 消费 `auth.UserRegistered`，给新用户 SPOT 账户记 `WELCOME_FUNDS`（默认 `USDT:10000,BTC:0.1,ETH:2`），键 `welcome:<user_id>` 保证一人一次。只在测试环境打开。
- **人工调账**：`ledger.manual_adjustment` 打开时可用 CLI，同事务写 `audit.AdminActionPerformed`；阶段 2 改为管理后台双人审批。

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger adjust --user <user_id> --asset USDT --amount 500 --reason "测试补款" --key <可重试的键>
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger balances <user_id>
```

测试服已打开的开关：`ledger.welcome_credit`、`account.transfer`、`ledger.manual_adjustment`（`exchangectl flags list` 查看）。

## 成交结算

需求 §11.1 第 7 步。ledger-service 以消费组 `ledger-settlement` 消费 `trade.events`，一批（最多 500 条或 20 毫秒）一个事务。每笔成交记三条 journal：

| journal | 键 | 内容 |
|---|---|---|
| `TRADE_SETTLE` | `trade:<成交ID>` | 买方冻结的 quote → 卖方可用；卖方冻结的 base → 买方可用。只含成交本金，所以它的合计就是撮合的成交合计（不变量 5） |
| `TRADE_FEE` | `trade-fee:<成交ID>` | 买方从收到的 base 付手续费、卖方从收到的 quote 付，都进 `FEE_REVENUE`（§11.3）；双方都为 0 时不记 |
| `ORDER_UNFREEZE` | `trade-release:<成交ID>` | 限价买单成交价低于限价时，(限价 − 成交价) × 数量当场解冻；订单按限价冻结，交易服务终态解冻时按"限价 × 成交量"扣除，两边正好对上 |

- 金额以成交事件为准，不重算：只校验成交额 = 价格 × 数量、手续费小于各自收到的数、限价不低于成交价、双方不是同一人。
- 成交记在 `ledger.trades`（成交 ID 为主键），重投、outbox 重复发布都只记一次。
- 事务里先按固定顺序锁住整批涉及的账户，再逐笔在内存里模拟全部 line：一笔成交要么三条都记，要么都不记。
- 账本拒绝的成交（事件不合法、超精度、冻结不足）记为 `FAILED` 并写明原因，这笔什么都不记，同批其他成交照常结算。这说明上游有 bug，是 P1：
  - 表现：指标 `ledger_trades_failed_total`（告警 `TradeSettlementRefused`）、错误日志 `trade settlement refused`、对账 `TRADES_SETTLED` 非零。
  - 处理：`exchangectl ledger trades --failed` 看原因，修复后 `exchangectl ledger retry-trades` 重新结算。
- 数据库或 instrument-service 出错时整批重试，不跳过。
- 其他指标：`ledger_trades_settled_total`、`ledger_settlement_batch_seconds`、`kafka_consumer_lag{group="ledger-settlement"}`。

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger trades --limit 10
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger trades --failed
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger retry-trades
```

首次部署时消费组从头读 `trade.events`，任务 3 以来留在冻结里的成交资金一次结清。

## HOUSE 的现货成交（ADR-0013）

阶段 4 B4：用户与 HOUSE 的虚拟流动性成交（见 [market-maker.md](market-maker.md)）时，成交事件带 `house_side`（HOUSE 买或卖）。

- 结算分录是 `HOUSE_TRADE_SETTLE`（键同样是 `trade:<成交ID>`）：用户一方与 `TRADE_SETTLE` 完全相同（从冻结付出、收进可用），HOUSE 一方记在系统科目 `MARKET_MAKER` 各资产的**可用**余额上，没有订单、冻结与手续费。用户的手续费与限价差额照常记 `TRADE_FEE`、`ORDER_UNFREEZE`。
- `MARKET_MAKER` 可以为负：内部资产（除 USDT、BTC、ETH 外的 47 个币）没有真实库存，HOUSE 卖出后记负数；可充提资产只在事故时为负（HOUSE 的额度保留了 1,000 USDT 的余量），告警 `HouseInventoryNegative`。
- 校验：HOUSE 买入时买方手续费与限价必须为 0，卖出时卖方手续费为 0；`ledger.trades.house_side` 记下 HOUSE 的方向。
- 对账：不变量 5 的 `TRADE_SETTLE_MATCHES_TRADES` 把 `HOUSE_TRADE_SETTLE` 一起算。
- HOUSE 的库存（模拟资金）从 `ADJUSTMENT` 调入 `MARKET_MAKER`（`MANUAL_ADJUSTMENT`，需要 `ledger.manual_adjustment`，同事务写审计事件 `house:MARKET_MAKER`）：

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger adjust --house --asset USDT --amount 500000 --reason "HOUSE inventory" --key seed-house-USDT-v1
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger system USDT   # MARKET_MAKER 在列
```

`scripts/ops/house.sh seed` 会一次调好测试服的三种资产（2026-10-02 起 5,000,000 USDT、11.74 BTC、370.4 ETH，配合测试服放大的 HOUSE 上限，见 [market-maker.md](market-maker.md#配置)）与 HOUSE 的合约保证金。

HOUSE 在合约上是普通用户（`HOUSE_USER_ID`），保证金在它的 `FUTURES` 账户。`exchangectl ledger house-margin` 先把模拟资金调入 HOUSE 的 `SPOT`（同 `ledger adjust`，键 `<key>:credit`），再划转到 `FUTURES`（键 `<key>:move`；划转的资格检查只放行 HOUSE 自己）。读环境变量 `HOUSE_USER_ID`，需要 `ledger.manual_adjustment`：

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger house-margin --amount 1900000 --reason "HOUSE contract margin" --key seed-house-margin-v2
```

## 合约结算

需求 §11.7，实施计划 §7.3 任务 4。合约不走上面的成交消费：仓位在 derivatives-service，它把每一步（一笔成交的一方、一次资金费、一次强平或 ADL、追加保证金）换算成对用户 `FUTURES` 账户（USDT）的一组**动作**，调 gRPC `SettleFutures`；账本在一个事务里按顺序记账，每个动作一条 journal（键 `futures:<请求键>:<序号>`），并把结果写进 `ledger.futures_settlements`：同键同内容返回第一次的结果（`replayed`），同键不同内容 409。

| 动作 | 分录 | 用户一方 | 对手 |
|---|---|---|---|
| `UNFREEZE` | `ORDER_UNFREEZE` | 冻结 → 可用（订单预留、平仓释放的仓位保证金） | — |
| `FREEZE` | `ORDER_FREEZE` | 可用 → 冻结（追加保证金）；`partial` 时冻结可用余额允许的部分 | — |
| `FEE` | `TRADE_FEE` | 付手续费 | `FEE_REVENUE` |
| `PROFIT` | `REALIZED_PNL`（强平、ADL 为 `LIQUIDATION_SETTLE`、`ADL_SETTLE`） | 收已实现盈利 | `PNL_CLEARING` 付出 |
| `LOSS` | 同上 | 付已实现亏损 | `PNL_CLEARING` 收入；用户付不足的部分由 `INSURANCE_FUND` 付 |
| `FUNDING_PAY` / `FUNDING_RECEIVE` | `FUNDING_PAYMENT` | 付 / 收资金费 | `FUNDING_CLEARING`；付不足的部分由 `INSURANCE_FUND` 付 |
| `INSURANCE` | `INSURANCE_CONTRIBUTION` | 强平后剩下的保证金 | `INSURANCE_FUND` |

- 每个动作可选用户的可用或冻结余额（逐仓的保证金在冻结里）。
- 向用户收钱的动作（`FEE`、`LOSS`、`FUNDING_PAY`）最多收到余额和 `limit`（例如逐仓仓位的保证金）为止：亏损与资金费的缺口由保险基金补，手续费的缺口免收。所以引擎已经成交的交易总能结算；只有保险基金也不够时整个请求被拒（`LEDGER_INSUFFICIENT_BALANCE`），什么都不记，由合约服务停住等人工处理。
- 精确动作（`UNFREEZE`、`FREEZE`、`INSURANCE`）余额不够就拒绝：说明合约服务的保证金账和账本不一致，是 P1。
- `FUTURES` 冻结 = 挂单的初始保证金与手续费预留 + 所有仓位的保证金；未实现盈亏不入账。
- `PNL_CLEARING` 可为负。仓位按入场成本记账时，每个合约恒有 `PNL_CLEARING + Σ多头成本 − Σ空头成本 = 0`（不变量 6），需要仓位数据，由 derivatives-service 对账。
- 保险基金：测试环境用模拟资金注资（`ADJUSTMENT` → `INSURANCE_FUND`，`INSURANCE_CONTRIBUTION`，需开关 `ledger.manual_adjustment`，同事务写审计事件）；真实资金走链上 `FundSystemAccount`。

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger insurance-fund --amount 1000000 --reason "测试环境保险基金" --key insurance-seed-1
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger system USDT
```

## 对账

ledger-service 每 `RECONCILE_INTERVAL`（默认 1 小时，启动 1 分钟后先跑一次）检查，结果写 `ledger.reconciliation_runs`，指标 `ledger_reconcile_mismatches{check}`：

| 检查 | 含义 |
|---|---|
| `JOURNAL_BALANCED` | 每条 journal 每个资产的 line 和为 0（不变量 1） |
| `ACCOUNT_MATCHES_LINES` | 账户 `available`/`frozen` 等于其全部 line 的累加（不变量 2） |
| `SNAPSHOT_MATCHES_ACCOUNT` | 账户余额与版本等于最后一条 line 的快照 |
| `TRADES_SETTLED` | 没有停在 `FAILED` 的成交 |
| `TRADES_NUMBERED` | 每个交易对的成交编号（引擎按交易对从 1 计数）连续、不重复：有缺口说明漏了成交。编号字段出现前的成交记为 0，先计入 |
| `TRADE_SETTLE_MATCHES_TRADES` | 不变量 5：按资产，`TRADE_SETTLE` 与 `HOUSE_TRADE_SETTLE` 的入账合计 = 成交数量（base）与成交额（quote）的合计 |
| `TRADE_FEE_MATCHES_TRADES` | 按资产，现货 `TRADE_FEE` 进 `FEE_REVENUE` 的合计 = 成交事件里的手续费合计（合约手续费同为 `TRADE_FEE`，幂等键 `futures:` 开头，不计入；2026-09-30 修正，之前任何一笔合约手续费都会让这项误报） |
| `FUNDING_BATCHES_BALANCED` | 每次资金费结算（合约 + 结算时间，按请求键 `funding:<合约>:<时间>:...` 归组）付出的不少于收到的：`FUNDING_CLEARING` 只留舍入零头 |
| `PNL_CLEARING_ONLY_PNL` | `PNL_CLEARING` 只出现在 `REALIZED_PNL`、`LIQUIDATION_SETTLE`、`ADL_SETTLE` 分录里 |

任何非零都是 P1：错误日志 `ledger invariant broken`。提现在阶段 2 上线后按资产自动暂停。手工立即对账：

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger reconcile
```

纠错只能追加 `MANUAL_ADJUSTMENT` 分录，禁止直接改表（表触发器也会拒绝）。

## 端到端检查

`scripts/e2e/ledger.sh`（`task e2e` 一起跑）：注册 → 欢迎资金到账 → 划转成功/重放/冲突/余额不足/超精度 → 余额、划转记录与资金流水。结算由 `scripts/e2e/matching.sh` 检查：两个用户成交后的最终余额（含手续费与限价买单差额）、`TRADE_SETTLE`/`TRADE_FEE` 流水。
