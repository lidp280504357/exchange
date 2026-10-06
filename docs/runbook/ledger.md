# 账本运维

需求 §5.9、§11.4；ADR-0001（余额只能由分录改变）、ADR-0008（金额十进制）。实现见 `internal/ledger`，契约 `api/proto/exchange/ledger/v1`（gRPC 与 `ledger.events`）、`api/proto/exchange/account/v1`（`account.events`）、`api/openapi/account.yaml`。

## 模型

- 账户 `ledger.accounts`：（所有者类型, 所有者, 账户类型, `scope`, 资产）唯一（`scope` 只有逐仓杠杆用，存交易对，其余为空，迁移 ledger 00008）。用户有 `SPOT`、`FUTURES` 与杠杆的六类行（见下文「杠杆账户」）；系统账户 `FEE_REVENUE`、`INSURANCE_FUND`、`DEPOSIT_PENDING`、`WITHDRAWAL_PENDING`、`UNCLAIMED_DEPOSIT`、`FUNDING_CLEARING`、`MARKET_MAKER`、`GAS_SUPPLY`、`ADJUSTMENT`、`PNL_CLEARING`（合约已实现盈亏的对手方，阶段 3）各资产一个。首次记账时自动建户。
- 分录 `journals` + `journal_lines`：**只追加**（触发器禁止 UPDATE/DELETE/TRUNCATE）；一条 journal 内同一资产的 line 之和为 0（提交时由约束触发器检查）；每条 line 带写入后的 `available_after`/`frozen_after` 与账户版本号。
- 余额非负（不变量 3）由表约束兜底：只有 `DEPOSIT_PENDING`、`ADJUSTMENT`、`PNL_CLEARING` 与 `MARKET_MAKER`（HOUSE 卖出内部资产，ADR-0013，阶段 4 B4）可为负；杠杆的负债行 `*_DEBT`、`*_INTEREST` 反过来只能 ≤ 0（按类型的符号约束，见「杠杆账户」）。
- 幂等：每条 journal 有唯一 `idem_key` 与内容摘要；同键同内容返回第一次的结果（`replayed`），同键不同内容返回 409 `COMMON_IDEMPOTENCY_CONFLICT`。gRPC 调用方的键按用户隔离（`u:<user>:<key>`）。
- 并发：一次记账按固定顺序锁住涉及的账户（`SELECT ... FOR UPDATE`），先在内存算完全部余额再写库，余额不足时什么都不写（`LEDGER_INSUFFICIENT_BALANCE`，422）。超精度金额直接拒绝（`LEDGER_AMOUNT_PRECISION`，400），精度来自 instrument-service。
- 每条 journal 发 `ledger.EntryPosted`（含全部 line，ClickHouse `ledger_entries` 由它投影），每个受影响的用户账户发 `ledger.BalanceChanged`（WebSocket `balances` 频道用）。

## 接口

- gRPC `LedgerService`（`ledger-service:9185`）：`Freeze`、`Unfreeze`（`ORDER_*`/`WITHDRAW_*`；`account_type` 可为 `SPOT`、`FUTURES`、`MARGIN_CROSS`、`MARGIN_ISOLATED`，逐仓带 `scope` = 交易对）、`Transfer`、`GetBalances`（只有 SPOT/FUTURES），写操作都要幂等键；杠杆的 `PostMargin`、`AccrueMarginInterest`、`GetMarginBalances`、`ListMarginDebts` 只给 margin-service 用，见「杠杆账户」。
- REST（经网关，需登录）：`GET /v1/account/balances`、`POST /v1/account/transfers`（必须带 `Idempotency-Key`）、`GET /v1/account/transfers`、`GET /v1/account/ledger`。
- 划转：现货 ↔ 合约在一个事务里完成（§13 验收 11）。需要功能开关 `account.transfer` 且账户资格允许（user-service `CheckEligibility(TRANSFER)`）；余额不足的划转记为 `FAILED` 并保留，同键重试得到同样的错误；成功/失败分别发 `account.AccountTransferCompleted`/`AccountTransferFailed`。
- 管理后台（2026-10-02 设计 C2）：
  - `Adjust` 可调现货或合约账户（`account_type`，默认 `SPOT`）。
  - **风控冻结**：`PlaceHold`、`ReleaseHold`、`ListHolds`。冻结把用户现货可用余额的一部分转入冻结（分录 `ADMIN_FREEZE`，键 `hold:<冻结单ID>`），解冻原样转回（`ADMIN_UNFREEZE`，键 `hold-release:<ID>`，备注为解冻理由），每张冻结单只能解冻一次（`LEDGER_HOLD_RELEASED`）。冻结单记在 `ledger.holds`（迁移 ledger 00005），两步都在同一事务里写审计 `ledger.hold_placed`/`ledger.hold_released`（操作者为管理员邮箱）。别处多解冻了这部分冻结资金时，全额解冻会一直报 `LEDGER_INSUFFICIENT_BALANCE`：运维用 `exchangectl ledger release-hold --id <冻结单ID> --reason "..." [--amount <数量>] [--force]`：账户的冻结余额是这张冻结单、该用户其它冻结单、现货活动订单的冻结、已冻结未结算的提现与账本还没结算的成交共用的，命令先列出这几项（括号里是笔数），只有扣掉其它几项后剩下的（且不超过冻结单金额）才算这张冻结单的，默认全部解冻，`--amount` 只能更少，超过就拒绝（C5.5 ⑯：否则会把活动订单或提现的冻结一并放掉，之后撤单或结算报 `LEDGER_INSUFFICIENT_BALANCE`）；剩下 0 就只标记已解冻。订单按整笔冻结额计算，账本已冻结、trading 还没标记的（`PENDING`）也算；成交后 trading 就放开了订单，账本要等消费 `trade.events` 结算时才动冻结余额，所以近 24 小时里该用户花掉这个资产的成交（买单的计价金额、卖单的数量），账本还没有 `trade:<成交ID>` 分录的也扣掉（C5.5 ⑳）；限价买单按 `max(成交额, 限价 × 成交数量)` 扣，因为成交价优于限价时，差价要等结算时（`trade-release:<成交ID>`）才从冻结里放回（C5.5 ㉒）。在死信队列里卡了超过 24 小时的成交不再计入，先按报错里的提示把它们处理掉。用户还有在途的订单、提现或未结算的成交时拒绝：先撤单、等账本结算（卡住的看 `exchangectl ledger trades --failed` 与 `exchangectl dlq`），或确认后加 `--force`（C5.5 ⑱、⑳：在途的整笔扣、活动订单的已成交部分可能扣两次，这样算只会让上限偏低）。账本解冻时自己也扣掉该用户其它未解冻冻结单的金额，命令算错了也放不掉别的冻结单（C5.5 ⑳）；解冻 0（只标记已解冻，没有分录；迁移 ledger 00006 放开了"已解冻必有解冻分录"的约束）不受这个限制（C5.5 ㉒）。审计带 `"forced": true`、实际解冻数、解冻时的冻结余额 `frozen_at_release` 与上限的组成 `cap`（冻结余额、其它冻结单、订单、提现、未结算成交及各自笔数、上限、是否带了 `--force`）（C5.5 ⑧、⑱、⑳）。`GetUnclaimedRelease`（C5.5 ⑦）按 `deposit-release:<充值ID>` 查待处理充值是否已放行，返回放行的分录与收款用户（`user_id`，2026-10-03 审查 AJ），wallet-service 驳回前用它，分配无主充值前也用它：上一次分配账本已付款、钱包没记上时，按收款用户补记。只做现货：合约账户的冻结是保证金，由 derivatives-service 对账。用户的资金流水能看到这两类分录（站内显示为"风控冻结/风控解冻"）。
  - **无主充值**（B7a，2026-10-03）：`CreditUnclaimed`（wallet-service 调用）把托管方报到、地址不属于任何用户的一笔充值记入 `UNCLAIMED_DEPOSIT`，分录与低于最小额的未入账充值相同（`DEPOSIT_PENDING` → `UNCLAIMED_DEPOSIT`，`DEPOSIT_CREDIT`，键 `deposit:<充值ID>`），只是没有用户；放行（下一条）不接受空 UUID 作用户，要先由管理员查明主人（见 [custody.md](custody.md)）。
  - **未入账充值的放行**（C2c）：`ReleaseUnclaimed`（wallet-service 调用，后台「充值 → 待处理 → 入账给用户」）把一笔已记入 `UNCLAIMED_DEPOSIT` 的充值按原资产、原数量转给用户现货账户，分录仍是 `DEPOSIT_CREDIT`（`UNCLAIMED_DEPOSIT` → 用户 `SPOT`，键 `deposit-release:<充值ID>`；备注不带理由，理由只进审计，C5.5 ⑦），同一事务写审计 `ledger.unclaimed_released`（操作者为管理员邮箱）。同键重放（换个理由、换个管理员也一样）返回原分录；`UNCLAIMED_DEPOSIT` 不够（不可为负）时被拒。

```sql
SELECT id, user_id, asset, amount, reason, actor, created_at, released_at, released_by FROM ledger.holds WHERE released_at IS NULL ORDER BY created_at DESC;
```

## 模拟资金（阶段 1）

阶段 1 没有链上充值，测试资金来自 `ADJUSTMENT` 对手科目（`MANUAL_ADJUSTMENT` 分录，§11.4）：

- **欢迎资金（注册赠送）**：ledger-service 消费 `auth.UserRegistered`，开关 `ledger.welcome_credit` 打开、且后台配置的赠送清单不为空时，给新用户 SPOT 账户记清单里的每个资产；键 `welcome:<user_id>` 保证一人一次，重投的事件看到已有分录就不再发（不论清单后来改成什么）。
  - 清单存在 `ledger.settings` 的 `welcome_credits`（迁移 ledger 00007，资产与数额的列表，数额是十进制字符串）。表里还没有值时，启动时用环境变量 `WELCOME_FUNDS`（默认 `USDT:10000,BTC:0.1,ETH:2`）写入首值（`updated_by` 为 `system:WELCOME_FUNDS`）；之后以表为准，环境变量不再起作用（设计 2026-10-04 §4.2）。
  - 发放读清单时缓存 30 秒：改动最多 30 秒后对新注册生效。上线时把清单清空（`[]`），开关保留为总闸，两者都满足才发。
  - 内部接口（后台调用，网关不转发 `/internal`）：`GET /internal/ledger/settings/welcome-credits` 返回 `{credits, flag_enabled, version, updated_by, updated_at}`；`PUT` 同一路径，带 `{credits, expected_version, actor, reason}` 整体替换。版本过期返回 409 `LEDGER_SETTINGS_CHANGED`；资产不存在、数额不大于 0、小数位超过资产精度、同一资产两次或超过 10 项，返回 400。每次修改发审计 `ledger.settings.welcome_credits`，带前后两份清单。提高赠送要不要第二个人批准，由后台按设计 §5 判断，账本只记它收到的结果。
  - instrument-service 每分钟读一次这个接口，作为平台资料里的 `welcome_credits`（站点的「注册即送」文案），开关关着时那里显示为空。
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
  - 处理：`exchangectl ledger trades --failed` 看原因，修复后 `exchangectl ledger retry-trades` 重新结算。ledger-service 也每分钟自动重试一次（每次最多 200 笔，结清的记 INFO 日志 `failed trades settled on their retry`）：HOUSE 可充提资产一时不够（补足之前）这类原因不必人工处理。
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
- `MARKET_MAKER` 可以为负：内部资产（instrument-service 里没有网络的站内币）没有真实库存，HOUSE 卖出后记负数。可充提资产（有网络的，现在是 USDT、BTC、ETH；ledger-service 启动时与之后每 30 秒从 instrument-service 读（与 market-maker 同频），读到之前一律按可充提算，日志 `HOUSE's backed assets`）不得低于 0：使它低于 0 的借方被拒、那笔成交挂为 `FAILED`；贷方永远放行，已经低于 0（事故或规则变更前留下的）也能被补足，挂起的成交随后的自动重试结清（ADR-0013「修订」）。挂起成交里 HOUSE 应付的量由 `GetSystemBalances` 的 `parked` 报给 market-maker，从报价用的持仓里扣掉，免得重试结清前再卖一次。告警 `HouseInventoryNegative`。
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

- `GAS_SUPPLY`（平台付的链上 gas 与托管方手续费从这里出，`ChainFeePosting`）：自建钱包由 `exchangectl wallet fund` 记平台转进热钱包的真实转账（`DEPOSIT_PENDING` → `GAS_SUPPLY`）；托管模式没有这样的转账，用户付的提现手续费本来就在托管方，`exchangectl ledger gas-supply --asset USDT --amount 20 --reason "..."` 把 `FEE_REVENUE` 挪到 `GAS_SUPPLY`（`MANUAL_ADJUSTMENT` 分录、像保险基金注资一样需要开关 `ledger.manual_adjustment`、不超过 `FEE_REVENUE`、同事务写审计 `ledger.gas_supply`，键重放无害；两边都不是钱包应有数，不变量 4 不变）。见 [custody.md](custody.md#托管方的手续费审查-④2026-10-03)。

## 杠杆账户（杠杆设计 2026-10-06，批次 E1/E2）

记账约定（杠杆设计 §3、E0 契约 §7）：每个杠杆账户每个资产三行，都记在用户名下——

| 账户类型 | 含义 | 符号 |
|---|---|---|
| `MARGIN_CROSS` / `MARGIN_ISOLATED` | 资产（可用 + 订单冻结） | ≥ 0 |
| `MARGIN_CROSS_DEBT` / `MARGIN_ISOLATED_DEBT` | 借款本金 | ≤ 0，不冻结 |
| `MARGIN_CROSS_INTEREST` / `MARGIN_ISOLATED_INTEREST` | 应计未还利息 | ≤ 0，不冻结 |

逐仓三行的 `scope` 是交易对（如 `BTC-USDT`），全仓为空。负债记成用户自己名下的负数行，每条 journal 按资产才仍然和为 0：借币在用户的资产行与负债行之间平衡，HOUSE 不出现；HOUSE 的已借出额 = −Σ `*_DEBT`（查询得出，不记账）；利息在计息时记入系统账户 `MARGIN_INTEREST_INCOME`，用户没还的部分就是他的利息行。

| 分录类型 | journal 的键 | 动作 |
|---|---|---|
| `MARGIN_TRANSFER_IN` / `MARGIN_TRANSFER_OUT` | `margin:margin-transfer:<划转ID>:0` | 现货 ↔ 杠杆资产行；划出后这个资产的资产行（可用 + 冻结）不得少于它的负债行与利息行之和——负债占着的部分划不走（`LEDGER_INSUFFICIENT_BALANCE`） |
| `MARGIN_BORROW` | `margin:margin-borrow:<借款ID>:0` | 资产行 +A / 负债行 −A |
| `MARGIN_INTEREST` | 借币的首小时 `margin:margin-borrow:<借款ID>:1`；整点 `margin-interest:<资产>:<整点的 Unix 秒>`，第 n 块（n ≥ 1）再加 `:<n>` | 利息行 −i / `MARGIN_INTEREST_INCOME` +Σi；整点每资产每小时一笔，每笔至多 1,000 个账户，多的按账户顺序分块 |
| `MARGIN_REPAY` | `margin:margin-repay:<还款ID>:0`；成交的自动还款 `trade-repay:<成交ID>:<buyer 或 seller>` | 资产行 −(I+P) / 利息行 +I / 负债行 +P。先息后本：I 必须等于还款额与所欠利息中较小者（`LEDGER_INTEREST_FIRST`）；多还 `LEDGER_DEBT_OVERPAID` |
| `MARGIN_TRADE_SETTLE` | `trade:<成交ID>` | 订单在杠杆账户上时的成交结算：行与 `TRADE_SETTLE`/`HOUSE_TRADE_SETTLE` 相同，只是这一方的账户是杠杆资产行 |
| `MARGIN_LIQUIDATE` | `margin:margin-repay:<还款ID>:0`（强平还款与保险基金补足）、`margin:liquidation-fee:<强平ID>:0` | 强平（批次 E3）的三种动作：`LIQUIDATION_REPAY` 同 REPAY，用强平后账户里的余额还；`INSURANCE_COVER` 由 `INSURANCE_FUND` 付：基金 −(I+P)、利息行 +I、负债行 +P，同样先息后本、不能多还，基金不够时拒绝；`LIQUIDATION_FEE`：资产行 −f、`INSURANCE_FUND` +f |

- `PostMargin` 一次请求可含几步（借币 = `BORROW` + 首小时 `INTEREST`），要么全记、要么全不记，每步一条 journal，请求记在 `ledger.margin_postings`：同键同内容返回原来的 journal，同键不同内容 `COMMON_IDEMPOTENCY_CONFLICT`；同键的两个请求同时到，后一个等锁后发现前一个已记账，就重放而不再动余额（审查 CJ）。
- 成交结算（批次 E2）：`TradeExecuted` 带双方的账户类型（空为现货），订单在杠杆账户上的一方在该账户的资产行结算（逐仓的 `scope` 是成交的交易对），分录类型 `MARGIN_TRADE_SETTLE`；手续费仍记 `TRADE_FEE`，从这一方收到的资产里扣。订单带 `AUTO_REPAY` 的一方在同一事务里另记一条 `MARGIN_REPAY`（备注 `auto-repay order <订单ID> trade <交易对> <成交ID>`）：用这笔成交收到的资产（扣过手续费）还这个资产的负债，先息后本、最多还清；margin-service 消费 `ledger.events` 里的这类分录同步借款表。
- `GetBalances`、`GET /v1/account/balances` 与 `BalanceChanged`（频道 `balances`）只有 SPOT/FUTURES：两站把非 FUTURES 的行都当现货累加，杠杆行（含负数的负债行）会把资产算错。杠杆账户经 margin-service 的 `/v1/margin/accounts` 与 `margin` 频道看；`EntryPosted` 的行带 `scope`。
- 后台的风控冻结（holds）只在 SPOT：杠杆账户由 margin-service 整体冻结（`FROZEN`），强平又要能卖出账户里的全部资产。
- 杠杆划转的开关与账户资格由 margin-service 检查（划入要 `margin.enabled` 与 `MARGIN_TRADE` 资格，划出不查），账本的 `PostMargin` 不再查。

借款、计息、估值与借款表的对账（不变量 7）见 [margin.md](margin.md)。

## 对账

ledger-service 每 `RECONCILE_INTERVAL`（默认 1 小时，启动 1 分钟后先跑一次）检查，结果写 `ledger.reconciliation_runs`，指标 `ledger_reconcile_mismatches{check}`：

| 检查 | 含义 |
|---|---|
| `JOURNAL_BALANCED` | 每条 journal 每个资产的 line 和为 0（不变量 1） |
| `ACCOUNT_MATCHES_LINES` | 账户 `available`/`frozen` 等于其全部 line 的累加（不变量 2） |
| `SNAPSHOT_MATCHES_ACCOUNT` | 账户余额与版本等于最后一条 line 的快照 |
| `TRADES_SETTLED` | 没有停在 `FAILED` 的成交 |
| `TRADES_NUMBERED` | 每个交易对的成交编号（引擎按交易对从 1 计数）连续、不重复：有缺口说明漏了成交。编号字段出现前的成交记为 0，先计入 |
| `TRADE_SETTLE_MATCHES_TRADES` | 不变量 5：按资产，`TRADE_SETTLE`、`HOUSE_TRADE_SETTLE` 与 `MARGIN_TRADE_SETTLE` 的入账合计 = 成交数量（base）与成交额（quote）的合计 |
| `TRADE_FEE_MATCHES_TRADES` | 按资产，现货 `TRADE_FEE` 进 `FEE_REVENUE` 的合计 = 成交事件里的手续费合计（合约手续费同为 `TRADE_FEE`，幂等键 `futures:` 开头，不计入；2026-09-30 修正，之前任何一笔合约手续费都会让这项误报） |
| `FUNDING_BATCHES_BALANCED` | 每次资金费结算（合约 + 结算时间，按请求键 `funding:<合约>:<时间>:...` 归组）付出的不少于收到的：`FUNDING_CLEARING` 只留舍入零头 |
| `PNL_CLEARING_ONLY_PNL` | `PNL_CLEARING` 只出现在 `REALIZED_PNL`、`LIQUIDATION_SETTLE`、`ADL_SETTLE` 分录里 |
| `MARGIN_ROWS_SIGNED` | 杠杆不变量 9：资产行 ≥ 0，负债行与利息行 ≤ 0 且没有冻结（表约束之外再核一遍） |
| `MARGIN_INTEREST_CONSERVED` | 杠杆不变量 8：按资产，历次 `MARGIN_INTEREST` 记入 `MARGIN_INTEREST_INCOME` 的合计 = 它们记到用户利息行的合计（取反）= `MARGIN_INTEREST_INCOME` 的余额。收入科目只由计息改变：以后要把利息收入划走（例如转入 `FEE_REVENUE`），这项检查要同时改 |

杠杆不变量 7（账本的负债 = margin-service 借款表的未还本金与利息，且不超过借贷池上限）要借款表，由 margin-service 每小时对账（指标 `margin_reconcile_mismatches{check}`，见 [margin.md](margin.md)），也可 `exchangectl margin reconcile`。

任何非零都是 P1：错误日志 `ledger invariant broken`。提现在阶段 2 上线后按资产自动暂停。手工立即对账：

```bash
ssh exchange sudo docker exec exchange-infra-ledger-service-1 /app/exchangectl ledger reconcile
```

纠错只能追加 `MANUAL_ADJUSTMENT` 分录，禁止直接改表（表触发器也会拒绝）。

## 端到端检查

`scripts/e2e/ledger.sh`（`task e2e` 一起跑）：注册 → 欢迎资金到账 → 划转成功/重放/冲突/余额不足/超精度 → 余额、划转记录与资金流水。结算由 `scripts/e2e/matching.sh` 检查：两个用户成交后的最终余额（含手续费与限价买单差额）、`TRADE_SETTLE`/`TRADE_FEE` 流水。
