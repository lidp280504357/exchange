# 账本运维

需求 §5.9、§11.4；ADR-0001（余额只能由分录改变）、ADR-0008（金额十进制）。实现见 `internal/ledger`，契约 `api/proto/exchange/ledger/v1`（gRPC 与 `ledger.events`）、`api/proto/exchange/account/v1`（`account.events`）、`api/openapi/account.yaml`。

## 模型

- 账户 `ledger.accounts`：（所有者类型, 所有者, 账户类型, 资产）唯一。用户只有 `SPOT`、`FUTURES`；系统账户 `FEE_REVENUE`、`INSURANCE_FUND`、`DEPOSIT_PENDING`、`WITHDRAWAL_PENDING`、`UNCLAIMED_DEPOSIT`、`FUNDING_CLEARING`、`MARKET_MAKER`、`GAS_SUPPLY`、`ADJUSTMENT` 各资产一个。首次记账时自动建户。
- 分录 `journals` + `journal_lines`：**只追加**（触发器禁止 UPDATE/DELETE/TRUNCATE）；一条 journal 内同一资产的 line 之和为 0（提交时由约束触发器检查）；每条 line 带写入后的 `available_after`/`frozen_after` 与账户版本号。
- 余额非负（不变量 3）由表约束兜底：只有 `DEPOSIT_PENDING` 与 `ADJUSTMENT` 可为负。
- 幂等：每条 journal 有唯一 `idem_key` 与内容摘要；同键同内容返回第一次的结果（`replayed`），同键不同内容返回 409 `COMMON_IDEMPOTENCY_CONFLICT`。gRPC 调用方的键按用户隔离（`u:<user>:<key>`）。
- 并发：一次记账按固定顺序锁住涉及的账户（`SELECT ... FOR UPDATE`），先在内存算完全部余额再写库，余额不足时什么都不写（`LEDGER_INSUFFICIENT_BALANCE`，422）。超精度金额直接拒绝（`LEDGER_AMOUNT_PRECISION`，400），精度来自 instrument-service。
- 每条 journal 发 `ledger.EntryPosted`（含全部 line，ClickHouse `ledger_entries` 由它投影），每个受影响的用户账户发 `ledger.BalanceChanged`（WebSocket `balances` 频道用）。

## 接口

- gRPC `LedgerService`（`ledger-service:9185`）：`Freeze`、`Unfreeze`（`ORDER_*`/`WITHDRAW_*`）、`Transfer`、`GetBalances`，写操作都要幂等键。
- REST（经网关，需登录）：`GET /v1/account/balances`、`POST /v1/account/transfers`（必须带 `Idempotency-Key`）、`GET /v1/account/transfers`、`GET /v1/account/ledger`。
- 划转：现货 ↔ 合约在一个事务里完成（§13 验收 11）。需要功能开关 `account.transfer` 且账户资格允许（user-service `CheckEligibility(TRANSFER)`）；余额不足的划转记为 `FAILED` 并保留，同键重试得到同样的错误；成功/失败分别发 `account.AccountTransferCompleted`/`AccountTransferFailed`。

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

## 对账

ledger-service 每 `RECONCILE_INTERVAL`（默认 1 小时，启动 1 分钟后先跑一次）检查，结果写 `ledger.reconciliation_runs`，指标 `ledger_reconcile_mismatches{check}`：

| 检查 | 含义 |
|---|---|
| `JOURNAL_BALANCED` | 每条 journal 每个资产的 line 和为 0（不变量 1） |
| `ACCOUNT_MATCHES_LINES` | 账户 `available`/`frozen` 等于其全部 line 的累加（不变量 2） |
| `SNAPSHOT_MATCHES_ACCOUNT` | 账户余额与版本等于最后一条 line 的快照 |
| `TRADES_SETTLED` | 没有停在 `FAILED` 的成交 |
| `TRADES_NUMBERED` | 每个交易对的成交编号（引擎按交易对从 1 计数）连续、不重复：有缺口说明漏了成交。编号字段出现前的成交记为 0，先计入 |
| `TRADE_SETTLE_MATCHES_TRADES` | 不变量 5：按资产，`TRADE_SETTLE` 的入账合计 = 成交数量（base）与成交额（quote）的合计 |
| `TRADE_FEE_MATCHES_TRADES` | 按资产，`TRADE_FEE` 进 `FEE_REVENUE` 的合计 = 成交事件里的手续费合计 |

任何非零都是 P1：错误日志 `ledger invariant broken`。提现在阶段 2 上线后按资产自动暂停。手工立即对账：

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger reconcile
```

纠错只能追加 `MANUAL_ADJUSTMENT` 分录，禁止直接改表（表触发器也会拒绝）。

## 端到端检查

`scripts/e2e/ledger.sh`（`task e2e` 一起跑）：注册 → 欢迎资金到账 → 划转成功/重放/冲突/余额不足/超精度 → 余额、划转记录与资金流水。结算由 `scripts/e2e/matching.sh` 检查：两个用户成交后的最终余额（含手续费与限价买单差额）、`TRADE_SETTLE`/`TRADE_FEE` 流水。
