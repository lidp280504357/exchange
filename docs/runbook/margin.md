# 杠杆交易（margin-service）

设计稿：[设计-杠杆交易-2026-10-06.md](../设计-杠杆交易-2026-10-06.md)；契约：[E0 契约草案](../设计-杠杆交易-E0契约-2026-10-06.md)、`api/openapi/margin.yaml`、`api/proto/exchange/margin/v1/`。本手册写已上线的部分：E1（账户、划转、借币、还币、整点计息、对账）与 E2（杠杆账户下单、按账户结算、自动借还）。E3（预警、强平）上线后补到这里。

## 服务

| 项 | 值 |
|---|---|
| 进程 | `margin-service`（compose 一个实例；整点计息持数据库租约 `margin-interest`，第二个实例只会等） |
| 端口 | HTTP 8099（网关转发 `/v1/margin/*`，`assets` 与 `pairs` 免登录）、gRPC 9199（E2 起给 spot-trading-service 的 `MarginService`）、运维 9099 |
| 库 | PostgreSQL schema `margin`（`migrations/margin`） |
| 依赖 | ledger-service gRPC（余额与负债都在账本，ADR-0001）、instrument-service（精度、交易对）、user-service（资格）、market-data-service `GET /v1/market/tickers`（估值，每秒一次） |
| 事件 | 发 `margin.events`（`MarginBorrowed`、`MarginRepaid`、`MarginInterestAccrued`；E3 加预警与强平） |
| 开关 | `margin.enabled`（借币、划入、杠杆下单；关着时还币、划出、查询照常）、`margin.auto_borrow`（E2）、`margin.liquidation`（E3） |

## 账本里怎么记

每个杠杆账户、每种资产三行，都是用户自己的账户（`owner_type = USER`）：

| 行 | 账户类型 | 余额 |
|---|---|---|
| 资产 | `MARGIN_CROSS` / `MARGIN_ISOLATED` | 可用 + 冻结，≥ 0 |
| 本金 | `MARGIN_CROSS_DEBT` / `MARGIN_ISOLATED_DEBT` | 负数，−借款本金 |
| 利息 | `MARGIN_CROSS_INTEREST` / `MARGIN_ISOLATED_INTEREST` | 负数，−未还利息 |

逐仓三行的 `scope` 列是交易对（`BTC-USDT`），其余账户 `scope` 为空。HOUSE 借出了多少不是账户，是 −Σ 本金行（按资产）。系统账户 `MARGIN_INTEREST_INCOME` 是 HOUSE 的利息收入，计息时确认。

| 分录 | 行 |
|---|---|
| `MARGIN_TRANSFER_IN` / `OUT` | SPOT ∓a，资产行 ±a；划出不得动用该资产负债占着的部分 |
| `MARGIN_BORROW` | 资产行 +a，本金行 −a |
| `MARGIN_INTEREST` | 利息行 −i，`MARGIN_INTEREST_INCOME` +i（整点按资产一笔汇总，每个账户一行） |
| `MARGIN_REPAY` | 资产行 −(i+p)，利息行 +i，本金行 +p（先还利息） |

账本拒绝把资产行打成负数（`LEDGER_INSUFFICIENT_BALANCE`）、把本金或利息行还成正数（`LEDGER_DEBT_OVERPAID`）、欠着利息时还本金（`LEDGER_INTEREST_FIRST`：还款里的利息部分必须等于还款额与所欠利息中较小者，审查 CJ ④）。margin-service 的一次操作是账本的一个 `PostMargin` 请求（每个动作一笔分录，同一事务，`margin_postings` 记请求，同键重放；同键的两个请求同时到时，后一个等到账户锁后发现前一个已记账，也是重放）。分录类型与键的全表见 [ledger.md](ledger.md) 的「杠杆账户」。

余额推送：`BalanceChanged`（频道 `balances`）与 `GET /v1/account/balances` 只有 SPOT、FUTURES；两站在 E4 之前会把其它账户当现货累加，所以杠杆行不推，由 margin-service 的 `margin` 频道推整个账户（E2）。`ledger.events` 的 `EntryPosted` 带全部行和 `scope`。

## 条款与种子

当前值在 margin-service 的表里：`asset_terms`（可借、作保证金、折扣、池上限、单用户上限、利率模型、固定小时利率、浮动曲线）、`pair_terms`（逐仓倍数与阈值）、`cross_terms`（全仓）。种子是 `deploy/instruments/margin.json`，部署脚本在服务起来后执行：

```bash
ssh exchange 'cd /opt/exchange/infra && sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin apply --file - < /opt/exchange/src/deploy/instruments/margin.json'
```

规则同 `instruments apply`：缺的建，上次由种子写的（`updated_by = file`）按文件改，后台改过的保留（输出 `kept`），`--force` 才覆盖；`--dry-run` 只列不改。逐仓阈值不写时按倍数取默认：3 倍 1.25 / 1.15，5 倍 1.20 / 1.10，10 倍 1.10 / 1.05；全仓 3 倍 1.30 / 1.10，5 倍 1.20 / 1.10；强平费 2%。

查看：`exchangectl margin terms`。

## 借币与还币

- 先划入：账户由第一次划入建立（逐仓只收该交易对的两种资产）。
- 可借额度 = min（倍数界、预警线界、池余量、单用户上限），`GET /v1/margin/max-borrowable` 的 `limited_by` 说是哪一个：
  - 倍数界：借后负债 ≤ 净资产 ×（倍数 − 1），借来的资产按其折扣计入资产；
  - 预警线界：借后风险率 ≥ 预警线。默认阈值下倍数界总是先到（预警线都低于 L/(L−1)），只有后台把预警线调高时才是它（`MARGIN_LEVEL_TOO_LOW`）。
- 借入即计首小时利息（同一个账本请求里），利率是本小时的；池余量在借币事务里按资产行锁扣下，账本拒绝时退回。
- 还币先抵利息再抵本金，`ALL` 按可用余额还到为止；运营冻结的账户可以还，强平中的不行（`MARGIN_FROZEN`）。
- 写账本前先记 `PENDING`（`borrows`、`repays`、`transfers`）；账本超时或不可用时接口返回 `COMMON_UNAVAILABLE`，恢复循环每 5 秒用同一个键重发（账本只记一次），客户端用同一个 `Idempotency-Key` 重试拿到结果。账本拒绝的写记为 `FAILED`，失败原因存成 `[类别] 错误码: 说明`（类别如 `UNPROCESSABLE`、`INVALID`），同键重试原样返回，HTTP 状态也和第一次一样。
- **在途的写也算**（审查 CK ①）：借币、划出与杠杆下单检查额度时，用的是账本余额加上该用户还在 `PENDING` 的写——在途借币当作已借到（可用、本金、首小时利息都加上），在途划出当作已划走，在途的整点利息当作已欠；在途的划入与还款要等账本记上才算。`PENDING` 先于账本余额读取：读的间隙里落账的写会被算两次而不会漏算，算两次也只会更保守。所以两个并发的借币，或者一边借一边划出，合起来也不会越过倍数与预警线。
- 资格与开关只由 margin-service 检查（`margin.enabled`、user-service 的 `SPOT_TRADE` 资格）；账本的 `PostMargin` 只管记账，不再查资格。

## 杠杆账户下单（E2）

- `POST /v1/orders` 带 `account`（`MARGIN_CROSS`，或交易对自己的 `MARGIN_ISOLATED`）与 `side_effect`。spot-trading-service 在冻结前调 margin-service 的 gRPC `MarginService.ReserveOrder`（9199）：开关（`AUTO_BORROW` 另要 `margin.auto_borrow`，关着报 `MARGIN_DISABLED`、detail `flag`）、账户状态、两种资产都可在该账户持有、估值完整、可用余额（加上 `AUTO_BORROW` 时的可借额度）够冻结、按限价（市价单按行情价）成交后风险率不低于预警线。`AUTO_BORROW` 在这里按差额借入（键 `order:<order_id>`，首小时利息同计）。每张单的首次答复记在 `margin.order_reservations`，重放（交易服务的恢复）返回它，零借款也一样。`CheckOrder` 做同样的检查，什么都不改。
- gRPC 的答复只有两类（审查 CM 的 C8，交易服务据此判断）：**拒绝**是 `InvalidArgument`、`FailedPrecondition`、`AlreadyExists`、`PermissionDenied`，或 `Unavailable` + `MARGIN_PRICE_UNAVAILABLE`，订单被拒；**结果未知**是 `Internal` 与其它 `Unavailable`，交易服务的恢复稍后用同一个 `order_id` 再问。依赖返回的 `NotFound`（例如不认识的交易对）改成 `InvalidArgument`（错误码不变），依赖的限流与鉴权失败改成 `Unavailable`，所以 margin-service 不会答 `NotFound`、`ResourceExhausted`、`Unauthenticated`、`Unimplemented`。
- 冻结、撤单释放与结算都在杠杆账户的资产行上：账本按成交事件里双方的账户记 `MARGIN_TRADE_SETTLE`（HOUSE 一侧照旧 `MARKET_MAKER`），`trades` 表记下双方账户与是否自动还款，停住的成交重试时同样处理。
- `AUTO_REPAY`：结算的同一事务里，账本用这一方到账的资产（扣过手续费）先还利息、再还本金，至多还清该资产的负债，单独记一笔 `MARGIN_REPAY`（键 `trade-repay:<成交>:<buyer|seller>`，备注带订单号）。margin-service 消费 `ledger.events` 认出这些分录（消费组 `margin-service-ledger`），记成 `AUTO_REPAY` 的还款，同时更新借款簿与池子，并发 `MarginRepaid`。消费有延迟，不变量 7 的检查会跳过最近 1 分钟内变动过的借款。

## 整点计息

- 每 15 秒检查一次：从上一个完成的整点之后到当前整点（停机后最多补 48 小时），按顺序计。
- 每笔借款按**整点那一刻的本金**计一小时（整点之后借的已在借入时计过首小时，整点之后还的整点照计）：本金 = 现在的本金 − 整点后借入 + 整点后还的本金，来自 `borrows`/`repays`。
- 利率：`FIXED` 用固定小时利率；`FLOATING` 按整点时池子的使用率（整点本金合计 ÷ 池上限）在曲线上取值。每个资产每小时的利率记在 `hourly_rates`（模型、利率、当时借出、池上限），每笔利息都能复算；利息 = 本金 × 利率，按资产精度向上取整，不复利。
- 整点那一刻的本金由借款表与整点后的借还一起算，两者在同一个只读快照里读（可重复读；审查 CK ②）：分开读时，两次读之间落账的借还只会出现在一边，整点就按一个从没有过的本金计息。
- 某资产这小时的利息第一次计算时，所有借款的利息在一个事务里一起存成 `PENDING`（`interest_charges`）；此后这些存下的就是这一小时的利息，重试照原样发给账本，不再重算。发账本时按账户顺序（用户、账户）每 1,000 个账户一笔分录：第一笔键 `margin-interest:<资产>:<整点 unix>`，第 n 笔（n ≥ 1）再加 `:<n>`（账本每笔至多 2,000 行，审查 CJ ⑤），每笔记上后它那部分记为 `DONE`。某资产有整点前发起、还没落账的借还时，这个资产这小时等下一轮；整点 `RUNNING` 直到所有资产计完，之后的整点等它。
- 查看：`SELECT * FROM margin.interest_runs ORDER BY hour DESC LIMIT 5`；指标 `margin_interest_last_run_timestamp_seconds`、`margin_ops_total{kind="interest"}`。

## 估值

- 每种资产按其 USDT 交易对的最新价（market-data-service 的 ticker；跟随币安的交易对是币安 ticker）。轮询失败超过 10 秒或 ticker 超过 1 分钟没更新，价格记为不新鲜，但仍按最近一次已知价计（协调会话 2026-10-06 01:15 定）。
- 从来没有价格的资产：持有的不计入资产、欠的不计入负债，账户估值不完整，借币、划出、杠杆下单返回 `MARGIN_PRICE_UNAVAILABLE`，也不强平。
- 风险率 = 资产（乘折扣）÷ 负债，8 位小数；无负债时为 `null`（前端显示 999）。

## 对账

| 不变量 | 在哪里查 | 内容 |
|---|---|---|
| 7 | margin-service 每小时（启动后 1 分钟第一次），`exchangectl margin reconcile` 随时 | 账本每个账户每种资产的 −本金行、−利息行 = `margin.loans`；池子已借出 = 借款本金合计 + 在途借币，且 ≤ 池上限 |
| 8 | 账本对账（`exchangectl ledger reconcile`，`MARGIN_INTEREST_CONSERVED`） | `MARGIN_INTEREST` 分录里 HOUSE 收入 = 各账户利息行之和，`MARGIN_INTEREST_INCOME` 只随它们动 |
| 9 | 账本对账（`MARGIN_ROWS_SIGNED`）与表约束 | 资产行 ≥ 0，本金与利息行 ≤ 0、不冻结 |

margin-service 的结果记在 `margin.reconciliation_runs`，指标 `margin_reconcile_mismatches{check}`。最近 1 分钟内变过的借款不比（两次读之间可能有写入）。

告警（`deploy/observability/alerts.yml`）：`MarginReconciliationMismatch`（任一检查有差异，critical）、`MarginInterestStalled`（2 小时没有完成的整点，warning：账本不可用，或某资产有借还卡在 `PENDING`——看 `margin.borrows`/`repays` 里 `PENDING` 的行与恢复循环的日志）。

## 测试服设置

- `margin.enabled` 暂不全局打开（协调会话 2026-10-06 03:24 决定 ①）：何时打开由协调会话在本服务 E2 的 gRPC 侧与审查 CK ①–③ 的修复部署后决定。`margin.auto_borrow` 同样。
- 端到端：`scripts/e2e/margin.sh`（`task e2e` 包含）。它持运维锁，注册的用户拿到 user_id 后把这两个开关**只对这个用户**打开（加进开关的 `allow-users`，开关原本已对所有人打开时不动），结束时（含失败）还原成原来的状态；单独运行时自己取锁。

## 常用命令

```bash
# 在 margin-service 容器里（ssh exchange 后 cd /opt/exchange/infra）
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin terms
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin loans
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin reconcile
```
