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

## 对账

ledger-service 每 `RECONCILE_INTERVAL`（默认 1 小时）检查，结果写 `ledger.reconciliation_runs`，指标 `ledger_reconcile_mismatches{check}`：

| 检查 | 含义 |
|---|---|
| `JOURNAL_BALANCED` | 每条 journal 每个资产的 line 和为 0（不变量 1） |
| `ACCOUNT_MATCHES_LINES` | 账户 `available`/`frozen` 等于其全部 line 的累加（不变量 2） |
| `SNAPSHOT_MATCHES_ACCOUNT` | 账户余额与版本等于最后一条 line 的快照 |

任何非零都是 P1：错误日志 `ledger invariant broken`。提现在阶段 2 上线后按资产自动暂停。手工立即对账：

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger reconcile
```

纠错只能追加 `MANUAL_ADJUSTMENT` 分录，禁止直接改表（表触发器也会拒绝）。

## 端到端检查

`scripts/e2e/ledger.sh`（`task e2e` 一起跑）：注册 → 欢迎资金到账 → 划转成功/重放/冲突/余额不足/超精度 → 余额、划转记录与资金流水。
