# 杠杆交易（margin-service）

设计稿：[设计-杠杆交易-2026-10-06.md](../设计-杠杆交易-2026-10-06.md)；契约：[E0 契约草案](../设计-杠杆交易-E0契约-2026-10-06.md)、`api/openapi/margin.yaml`、`api/proto/exchange/margin/v1/`。本手册写已上线的部分：E1（账户、划转、借币、还币、整点计息、对账）、E2（杠杆账户下单、按账户结算、自动借还）、E2b（风险率监控、预警、账户推送）、C5（后台内部接口）与 E3（强平）。

## 服务

| 项 | 值 |
|---|---|
| 进程 | `margin-service`（compose 一个实例；整点计息持数据库租约 `margin-interest`，风险率监控与强平持 `margin-monitor`，第二个实例只会等） |
| 端口 | HTTP 8099（网关转发 `/v1/margin/*`，`assets` 与 `pairs` 免登录）、gRPC 9199（E2 起给 spot-trading-service 的 `MarginService`）、运维 9099 |
| 库 | PostgreSQL schema `margin`（`migrations/margin`） |
| 依赖 | ledger-service gRPC（余额与负债都在账本，ADR-0001）、instrument-service（精度、交易对、lot）、user-service（资格）、market-data-service `GET /v1/market/tickers`（估值，每秒一次）、spot-trading-service 的内部接口（强平撤单与市价单，`TRADING_SERVICE_URL`） |
| 事件 | 发 `margin.events`（`MarginBorrowed`、`MarginRepaid`、`MarginInterestAccrued`、`MarginLevelWarned`、`MarginLiquidationStarted`、`MarginLiquidationCompleted`）与 `margin.accounts`（`MarginAccountUpdated`） |
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

查看：`exchangectl margin terms`。后台把某交易对的逐仓关掉（`isolated = false`）后，只是不能再开新的逐仓账户，已开的照常借还、下单，按原条款计算（审查 CY C17 ①）。

## 借币与还币

- 先划入：账户由第一次划入建立（逐仓只收该交易对的两种资产）。
- 可借额度 = min（倍数界、预警线界、池余量、单用户上限），`GET /v1/margin/max-borrowable` 的 `limited_by` 说是哪一个：
  - 倍数界：借后负债 ≤ 净资产 ×（倍数 − 1），借来的资产按其折扣计入资产；
  - 预警线界：借后风险率 ≥ 预警线。默认阈值下倍数界总是先到（预警线都低于 L/(L−1)），只有后台把预警线调高时才是它（`MARGIN_LEVEL_TOO_LOW`）。
- 借入即计首小时利息（同一个账本请求里），利率是本小时的；池余量在借币事务里按资产行锁扣下，账本拒绝时退回。
- 还币先抵利息再抵本金，`ALL` 按可用余额还到为止；运营冻结的账户可以还，强平中的不行（`MARGIN_FROZEN`）。
- 写账本前先记 `PENDING`（`borrows`、`repays`、`transfers`）；账本超时或不可用时接口返回 `COMMON_UNAVAILABLE`，恢复循环每 5 秒用同一个键重发（账本只记一次），客户端用同一个 `Idempotency-Key` 重试拿到结果。账本拒绝的写记为 `FAILED`，失败原因存成 `[类别] 错误码: 说明`（类别如 `UNPROCESSABLE`、`INVALID`），同键重试原样返回，HTTP 状态也和第一次一样。客户端的 `Idempotency-Key` 不能以 `order:`、`trade-repay:`、`liquidation:` 开头（本服务自己的写用这些键，审查 CR：客户端用 `trade-repay:...` 会让同键的自动还款被当成已记过）。
- **在途的写也算**（审查 CK ①）：借币、划出与杠杆下单检查额度时，用的是账本余额加上该用户还在 `PENDING` 的写——在途借币当作已借到（可用、本金、首小时利息都加上），在途划出当作已划走，在途的整点利息当作已欠；在途的划入与还款要等账本记上才算。`PENDING` 先于账本余额读取：读的间隙里落账的写会被算两次而不会漏算，算两次也只会更保守。所以两个并发的借币，或者一边借一边划出，合起来也不会越过倍数与预警线。
- 资格与开关：margin-service 在划入与借币时查 `margin.enabled` 与 user-service 的 `MARGIN_TRADE` 资格（只限 ACTIVE 账户、同样受 `margin.enabled` 的规则约束，两站据此显示杠杆入口；审查 CO 的 C9，此前用 `SPOT_TRADE`），下单要自动借币时也查；杠杆下单另由交易服务先查开关与 `MARGIN_TRADE`（编码会话 8f568ccd）。划出与还币只降风险，两样都不查。账本的 `PostMargin` 只管记账，不查资格。
- 现货产品线关闭时（`product.spot`，产品线开关设计 §1 #7）：划入只收该杠杆账户欠着的资产（本金或利息，在用户锁下按含在途的余额判断），不按欠款截量——用户要还债就得能把钱转进来，与关闭期间只放行还款单同口径；其余划入答 403 `PRODUCT_CLOSED`（`details.product` 为 `spot`），不会因此新建逐仓账户；手动借币也答 `PRODUCT_CLOSED`（借币是新增暴露，审查 C61；下单自带的借币随新单在 spot-trading-service 被拒）；还币、划出照常（集成测试 `TestClosedSpotTakesOnlyWhatPaysADebt`）。这条规则偏宽松、可以接受：欠着哪怕 1e-8 的利息也算欠款，放行任意数量的划入；在途的还款不从欠款里扣，可能放行刚还清的资产。杠杆账户的下单由 spot-trading-service 按同一开关把关（只放行撤单、还款单与强平单）。

## 杠杆账户下单（E2）

- `POST /v1/orders` 带 `account`（`MARGIN_CROSS`，或交易对自己的 `MARGIN_ISOLATED`）与 `side_effect`。spot-trading-service 在冻结前调 margin-service 的 gRPC `MarginService.ReserveOrder`（9199）：开关（`AUTO_BORROW` 另要 `margin.auto_borrow`，关着报 `MARGIN_DISABLED`、detail `flag`）、账户状态、两种资产都可在该账户持有、估值完整、可用余额（加上 `AUTO_BORROW` 时的可借额度）够冻结、按限价（市价单按行情价）成交后风险率不低于预警线。`AUTO_BORROW` 在这里按差额借入（键 `order:<order_id>`，首小时利息同计）。每张单的首次答复记在 `margin.order_reservations`（连同请求的摘要），重放（交易服务的恢复）返回它，零借款也一样；同一 `order_id` 内容不同时答 `COMMON_IDEMPOTENCY_CONFLICT`。第一次的答复没有送到（账本中断、崩溃）时，重放先找 `order:<order_id>` 下的借款：`PENDING` 的接着记账，`FAILED` 的答原来的拒绝，`DONE` 的就是答复——不再拿已被这笔借款改过的余额重新检查（审查 CR ②，C11）。`CheckOrder` 做同样的检查，什么都不改。
- gRPC 的答复只有两类（审查 CM 的 C8，交易服务据此判断）：**拒绝**是 `InvalidArgument`、`FailedPrecondition`、`AlreadyExists`、`PermissionDenied`，或 `Unavailable` + `MARGIN_PRICE_UNAVAILABLE`，订单被拒；**结果未知**是 `Internal` 与其它 `Unavailable`，交易服务的恢复稍后用同一个 `order_id` 再问。依赖返回的 `NotFound`（例如不认识的交易对）改成 `InvalidArgument`（错误码不变），依赖的限流与鉴权失败改成 `Unavailable`，所以 margin-service 不会答 `NotFound`、`ResourceExhausted`、`Unauthenticated`、`Unimplemented`。
- 冻结、撤单释放与结算都在杠杆账户的资产行上：账本按成交事件里双方的账户记 `MARGIN_TRADE_SETTLE`（HOUSE 一侧照旧 `MARKET_MAKER`），`trades` 表记下双方账户与是否自动还款，停住的成交重试时同样处理。
- `AUTO_REPAY`：结算的同一事务里，账本用这一方到账的资产（扣过手续费）先还利息、再还本金，至多还清该资产的负债，单独记一笔 `MARGIN_REPAY`（键 `trade-repay:<成交>:<buyer|seller>`，备注带订单号）。margin-service 消费 `ledger.events` 认出这些分录（消费组 `margin-service-ledger`），记成 `AUTO_REPAY` 的还款，同时更新借款簿与池子，并发 `MarginRepaid`。消费有延迟，不变量 7 的检查会跳过最近 1 分钟内变动过的借款。
- 订单结束时还借款（B160/B163，交易服务与账本做，本服务不用改）：杠杆账户上用 `AUTO_BORROW` 借过币的订单结束后，交易服务调账本 `RepayReleased`，把冻结里回来的在该单借款额度内还掉，键 `trade-repay:release:<订单ID>`、备注 `auto-repay order <订单ID> release`——前缀同上，本服务照样记成 `AUTO_REPAY` 还款。按数量的市价买按保护价冻结也就按它借币，价格带在成交结算时放回，这部分会还掉；限价买的价格改善（成交价低于限价省下的）**有意不还**，照旧留作借款（与币安一致，端到端「限价买借款 = 限价 × 数量 − 自有」按此核对）；撤销或部分成交的借币单还掉未用的冻结。账本在该单的成交没全部结算前拒绝（`LEDGER_TRADES_UNSETTLED`），交易服务的恢复任务下一轮再来，一直等到结算（B164：只有订单结束一小时后才越过账本停成 FAILED 的成交、按当时可用还；等得太久告警 `TradingOrderReleasesStuck`）。见 [trading.md](trading.md)、[ledger.md](ledger.md)。

## 整点计息

- 每 15 秒检查一次：从上一个完成的整点之后到当前整点（停机后最多补 48 小时，更早的整点不再计；它们已存下的 `PENDING` 利息由恢复循环照原样发给账本），按顺序计。整点过 30 秒才计：整点前最后几秒的自动还款要经 `ledger.events` 回到借款表，被还掉的本金就不计这一小时（审查 CR）。启动时从 `interest_runs` 恢复指标 `margin_interest_last_run_timestamp_seconds`，重启不会让 `MarginInterestStalled` 失明。
- 每笔借款按**整点那一刻的本金**计一小时（整点之后借的已在借入时计过首小时，整点之后还的整点照计）：本金 = 现在的本金 − 整点后借入 + 整点后还的本金，来自 `borrows`/`repays`。
- 利率：`FIXED` 用固定小时利率；`FLOATING` 按整点时池子的使用率（整点本金合计 ÷ 池上限）在曲线上取值。每个资产每小时的利率记在 `hourly_rates`（模型、利率、当时借出、池上限），每笔利息都能复算；利息 = 本金 × 利率，按资产精度向上取整，不复利。
- 整点那一刻的本金由借款表与整点后的借还一起算，两者在同一个只读快照里读（可重复读；审查 CK ②）：分开读时，两次读之间落账的借还只会出现在一边，整点就按一个从没有过的本金计息。
- 某资产这小时的利息第一次计算时，所有借款的利息在一个事务里一起存成 `PENDING`（`interest_charges`）；此后这些存下的就是这一小时的利息，重试照原样发给账本，不再重算。发账本时按账户顺序（用户、账户）每 1,000 个账户一笔分录：第一笔键 `margin-interest:<资产>:<整点 unix>`，第 n 笔（n ≥ 1）再加 `:<n>`（账本每笔至多 2,000 行，审查 CJ ⑤），每笔记上后它那部分记为 `DONE`。某资产有整点前发起、还没落账的借还时，这个资产这小时等下一轮；整点 `RUNNING` 直到所有资产计完，之后的整点等它。
- 查看：`SELECT * FROM margin.interest_runs ORDER BY hour DESC LIMIT 5`；指标 `margin_interest_last_run_timestamp_seconds`、`margin_ops_total{kind="interest"}`。

## 估值

- 每种资产按其 USDT 交易对的最新价（market-data-service 的 ticker；跟随币安的交易对是币安 ticker）。轮询失败超过 10 秒或 ticker 超过 1 分钟没更新，价格记为不新鲜，但仍按最近一次已知价计（协调会话 2026-10-06 01:15 定）。
- 价格事件（通用价格控制 2026-10-07，见 [market-sim.md](market-sim.md)「任意交易对的价格事件」）期间 ticker 是叠加后的价：默认（`risk`）杠杆估值随之，与真实暴涨暴跌一样会预警、强平；勾选「不连带」（`risk` 为 false）的事件按币安原价估值（审查 GD ①）——每次轮询另读 market-data 的 `GET /internal/market/overlay`，对这些交易对改读 `GET /internal/market/{symbol}/reference?for=risk` 的价。读不到 `overlay` 列表时按 ticker 原样计；某个交易对的风险价读不到时沿用上一次的价（1 分钟后记为不新鲜），从来没有价的就不计价（估值不完整、不强平），都记日志 `ticker poll failed`。
- 从来没有价格的资产：持有的不计入资产、欠的不计入负债，账户估值不完整，借币、划出、杠杆下单返回 `MARGIN_PRICE_UNAVAILABLE`，也不强平。
- 风险率 = 资产（乘折扣）÷ 负债，8 位小数；无负债时为 `null`（前端显示 999）。

## 风险率监控与账户推送（E2b）

- 监控持数据库租约 `margin-monitor`（一次一个进程）每秒一轮：估值所有有负债或状态不是 `NORMAL` 的账户（列表每 5 秒重读），用每秒更新的价格；某用户的余额在账本或本服务的写动过它时立即重读（`ledger.events` 里带杠杆行的分录、本服务的划转/借/还/计息/冻结），否则至多 5 秒重读一次。
- 状态：跌破预警线 `NORMAL → WARNED`，记 `warned_at`、发 `MarginLevelWarned`（`margin.events`；回到线上之前只发一次，网关推成 `margin` 频道的 `WARNING`，站内信与邮件由通知服务做）；回到预警线的 1.01 倍以上才 `WARNED → NORMAL`（在线附近来回波动只预警一次，审查 CY）。某个用户的余额读不到时只跳过这个用户（审查 CY C17 ③）。到强平线连续两轮交给强平（E3，受 `margin.liquidation` 控制）；估值不完整（有从未有价的资产）的账户不动状态、不强平；`FROZEN` 的账户不改状态，但到强平线照样强平；`LIQUIDATING` 由强平流程管。
- 推送：账户变化时把整个账户（`MarginAccountUpdated`：状态、风险率、阈值、总资产/总负债/净资产、逐仓强平价、各资产余额）发到主题 `margin.accounts`（按 user_id 分区，派生状态，保留 1 小时，直接生产不经 outbox，丢了由下一次补上），网关推成 `margin` 频道的 `ACCOUNT`。何时发：余额被动过、状态变了，或风险率、总资产、总负债相对上次变动超过 0.1%（价格引起），每个账户每秒至多一次；没有负债的账户只在被动过时发。
- 指标：`margin_monitor_accounts`、`margin_monitor_last_pass_timestamp_seconds`、`margin_warnings_total`、`margin_liquidations_due_total`；告警 `MarginMonitorStalled`（超过 1 分钟没有完成一轮，critical：既不预警也不强平）。
- 局限："被动过"的通知只在本进程内（`Touched`），监控与强平只在持租约 `margin-monitor` 的实例上跑。多实例时，别的实例处理的写不会立即触发推送：有负债的账户至多 5 秒后重读补上，没有负债的账户要等下一次被动过才推（编码会话提出）。测试服是单实例；要多实例时改成跨实例通知。

## 强平（E3）

- 触发：监控到强平线连续两轮，且 `margin.liquidation` 对该用户打开（`AUTO`）；或管理员的强平申请经第二人批准后，由 admin-service 调内部接口（`MANUAL`，带 `approval_id`；同一个 `approval_id` 再调返回同一笔强平）。没有负债答 `MARGIN_NOTHING_OWED`（409），已在强平中答 `MARGIN_FROZEN`。
- 开始时在用户锁内记一行 `liquidations`（`STARTED`），账户改为 `LIQUIDATING`（不能下单、借币、划转），发 `MarginLiquidationStarted`。之后由监控每秒推进，每一步做完就进下一步，重启后从记下的步骤接着做：
  1. `CANCEL`：调交易服务的 `POST /internal/orders/cancel`，直到账户没有冻结；
  2. `SELL`（审查 DD C19 ①②⑤）：只卖够用的——要卖出的报价资产 x 满足"报价资产可用 + x ≥ 报价资产自身负债 + 要买回的 + 强平费率 ×（已卖 + x + 要买回的）"，再多 2%（`sellBuffer`）；"要买回的"见第 4 步。按估值从大到小，从各资产超出它自身负债的部分里卖（按 lot 向上取整到够数，不超过该部分按 lot 向下取整的量，也不超过交易对的单笔上限），对 HOUSE 发市价卖单换成报价资产（全仓是 USDT，逐仓是该交易对的报价资产），`POST /internal/orders/liquidations`，side_effect NONE。
     订单一轮一轮下：每单先记进 `liquidation_orders`（主键含 `attempt`，迁移 margin 00007）再发；交易服务按"强平 + 交易对 + 方向 + attempt"幂等，同一单再发就读回它现在的状态（`status`、`filled_quantity`、`filled_quote`）。成交了多少只认交易服务报的终态（`FILLED`/`CANCELED`/`EXPIRED`/`REJECTED`，记为 `DONE`；被拒记为 `REFUSED`、成交为 0），不从余额变化推断；交易服务的 403、404 与连不上都不算拒绝，只是等着再读。一轮的单全部终结、账户也没有冻结（账本结算完）之后，按当时的持仓重算还差多少，差就下一轮——同一交易对的下一单是 `attempt + 1`：上一单成交了就立刻下，连续没成交的按 0、2、4、8……秒、最多 1 分钟退避；交易对暂停（`HALT`/`PREPARE`）时不下单，等它恢复。只有不再缺、或剩下的都卖不了（不足一个 lot 或最小数量、交易对不存在或已 `CANCEL_ONLY`/`DELISTED`、同一交易对已下满 1000 单）才进下一步——还能卖的抵押不会交给保险基金；
  3. `FEE`（审查 DD C19 ④，先于买入）：强平费 = 强平费率 ×（卖出所得 + 将要买入的金额）。将要买入的金额按与 `BUY` 步同样的规则计（审查 DJ 的 C23 ②）：在额度 (报价资产可用 − 自身负债 − 费率 × 卖出所得) ÷ (1 + 费率) 内，按缺口从大到小，每笔不超过剩下的额度；不足一单最小量的缺口不买、不计费，由基金补；费率取交易对或全仓条款里的 `liquidation_fee`（默认 2%），记在报价资产上，最多收到账户剩下的报价资产为止（`LIQUIDATION_FEE` → `INSURANCE_FUND`，键 `liquidation-fee:<强平ID>`）。金额先存进这一行再记账，重试记同样的数（审查 CY 的决定 (b)：先收费、再还债、再由基金补）；
  4. `BUY`：还欠的非报价资产（"要买回的"：缺口按 lot 向上取整、至少一单的最小量，按价格 ×1.02 折成报价资产）用收费后剩下的报价资产（先留够报价资产自身的负债）发市价买单，缺口大的先买；订单、终态、`attempt`、退避与暂停同 `SELL`；剩下的报价资产不够买一个最小量时不再买，缺口由基金补；
  5. `REPAY`：每种资产用账户里的余额还自己的负债，先息后本，记成强平还款（`LIQUIDATION_REPAY`，分录 `MARGIN_LIQUIDATE`，键 `liquidation:<强平ID>:repay:<资产>`，第 n 次再加 `:<n>`）。账本拒绝时按当时的余额与负债重算、换下一个键再还（审查 DD C19 ③）：第一次立刻，之后按同样的退避；
  6. `COVER`：保险基金 `INSURANCE_FUND` 只补账户还不了的部分（负债 − 可用；某资产账户里还有余额就先回到 `REPAY` 用它还），按该负债币种（`INSURANCE_COVER`，同样先息后本）。基金不允许为负，按可借资产分别注资（测试服 2026-10-06 已按各资产借贷池上限的 10% 注资，USDT 沿用合约的保险基金；上线时双人划转）。某资产不够时账本拒绝：强平状态记为 `SHORTFALL`，负债保留，告警 `InsuranceFundShort`；每分钟换一个键重试（`...:cover:<资产>:<n>`），运维给基金注资（`exchangectl ledger insurance-fund --asset <资产>`）后下一次就能完成（审查 CY 的决定 (a)）。对用户仍显示为进行中；
  7. `SETTLE`：记下各资产还了多少（账户自己还的加上基金补的）、账户剩下什么、基金补了多少（按当时价格折成 USDT），状态 `COMPLETED`。账户回到原来的状态：管理员冻结过的仍是 `FROZEN`，否则 `NORMAL`。最后发 `MarginLiquidationCompleted`。
- 查询：用户 `GET /v1/margin/liquidations`；后台账户详情的 `liquidations`；`exchangectl margin liquidations [--user ID]` 列出强平，含进行到哪一步、在等什么（`note`）。手工强平（测试与演练用，等同一次已批准的申请）：`exchangectl margin liquidate --user ID --account MARGIN_CROSS|MARGIN_ISOLATED:<交易对>`，在 margin-service 容器里执行。
- 卖单的数量不超过交易对的单笔上限，超过的部分在下一轮以下一个 `attempt` 再卖。`SELL`、`BUY` 卡住（交易对长期暂停、HOUSE 不接单）时强平一直等着，`note` 写在等什么，由 `MarginLiquidationStuck` 叫人；监控日志里"a liquidation waits"每分钟至多一条。
- 指标与告警：`margin_liquidations_total{trigger}`（AUTO、MANUAL 从 0 开始）、`margin_liquidation_oldest_seconds`（不含 `SHORTFALL` 的，那些归 `InsuranceFundShort`）、`margin_liquidations_shortfall`、`margin_liquidations_due_total`；告警 `MarginLiquidationStuck`（一笔强平超过 10 分钟没完成，critical）、`InsuranceFundShort`（有强平在等基金，critical）、`MarginLiquidationDue`（10 分钟内到强平线的账户多于监控自动强平的，即 `margin.liquidation` 对这些用户关着，warning：账户停在 `WARNED`，要么人工强平——后台申请经第二人批准或 `exchangectl margin liquidate`——要么盯着）——用 `exchangectl margin liquidations` 看卡在哪一步、在等什么。

## 对账

| 不变量 | 在哪里查 | 内容 |
|---|---|---|
| 7 | margin-service 每小时（启动后 1 分钟第一次），`exchangectl margin reconcile` 随时 | 账本每个账户每种资产的 −本金行、−利息行 = `margin.loans`；池子已借出 = 借款本金合计 + 在途借币，且 ≤ 池上限 |
| 8 | 账本对账（`exchangectl ledger reconcile`，`MARGIN_INTEREST_CONSERVED`） | `MARGIN_INTEREST` 分录里 HOUSE 收入 = 各账户利息行之和，`MARGIN_INTEREST_INCOME` 只随它们动 |
| 9 | 账本对账（`MARGIN_ROWS_SIGNED`）与表约束 | 资产行 ≥ 0，本金与利息行 ≤ 0、不冻结 |

margin-service 的结果记在 `margin.reconciliation_runs`，指标 `margin_reconcile_mismatches{check}`。最近 1 分钟内变过的借款不比（两次读之间可能有写入）。

告警（`deploy/observability/alerts.yml`）：`MarginReconciliationMismatch`（任一检查有差异，critical）、`MarginInterestStalled`（2 小时没有完成的整点，warning：账本不可用，或某资产有借还卡在 `PENDING`——看 `margin.borrows`/`repays` 里 `PENDING` 的行与恢复循环的日志）。

## 后台内部接口（C5）

admin-service 经 margin-service 的 HTTP 端口调 `/internal/margin/*`（协调会话 2026-10-06 03:24 决定 ⑤⑥）：只在 compose 网络里，网关不转发 `/internal`，经网关来的请求（带 `X-User-Id`）一律 404；不签名，写操作带 `X-Admin-Id`（管理员邮箱，记为 `updated_by` / `frozen_by`）。审批与审计在 admin-service，margin-service 只照发来的内容按读到的版本改，并做自己的校验。字段名用本服务的列名。

| 接口 | 内容 |
|---|---|
| `GET /internal/margin/assets` | 各资产条款（`asset_terms` 各列）、`lent`（借款表的未还本金合计）、`pool_available`、`utilization`、本小时 `hourly_rate` 与 `rate_hour`、`interest_owed`、`borrowers`（欠该资产的账户数）、`version`、`updated_by`（种子写的显示为空串）、`updated_at` |
| `PUT /internal/margin/assets/{asset}` | 全部条款列 + `expected_version`（0 = 新增，资产须在 instrument-service 里）。`haircut` 大于 0 且不超过 1、`user_cap` ≤ `pool_cap`、利率不为负、浮动曲线递增（不管当前用哪个模型）、各值不超过列的小数位；版本变了 409 `MARGIN_PARAMS_CHANGED` |
| `GET` / `PUT /internal/margin/settings` | 全仓条款 `cross`（`leverage`、`warn_level`、`liquidation_level`、`liquidation_fee`）、`isolated_defaults`（逐仓 3/5/10 倍的建议阈值）、版本；PUT 带 `cross` 与 `expected_version`，倍数只能 3 或 5 |
| `GET /internal/margin/pairs`、`PUT /internal/margin/pairs/{symbol}` | 各交易对的逐仓条款（`isolated`、倍数、阈值、强平费）、`accounts`（该交易对开过的逐仓账户数）、版本；PUT 倍数只能 3、5、10，`expected_version` 为 0 时新增（交易对须在 instrument-service 里，两种资产都在杠杆资产表里） |
| `GET /internal/margin/accounts` | `status`、`account`、`symbol`、`user_id`、`user_ids` 或 `exclude_user_ids`（审查 L3，后台默认只看真人：只要这些用户的、或除这些用户以外的账户；逗号分隔或重复给出，最多 1,000 个 UUID，两者只能给一个，否则答 400；`user_ids=` 给空即谁的都不要；在库里按 `= ANY` / `<> ALL` 筛；同名 `POST /internal/margin/accounts/list` 其余筛选仍在查询串、这两个放进 JSON 正文，最多 5,000 个，审查 C76，契约 `api/internal/margin.yaml`）、`limit`（1–500，默认 500）筛选；风险率最低的在前，无负债的排后（按净资产），什么都没有的账户不列；`truncated` 表示还有更多（最多估值 5,000 个账户）。每行：用户、账户、倍数、状态、风险率、阈值、总资产/总负债/净资产、逐仓强平价、`warned_at`、`frozen_by`/`frozen_reason`/`frozen_at`（只有管理员冻结时有值）、`unpriced`（持有或欠着、价格不新鲜或从未有价的资产）、`updated_at` |
| `GET /internal/margin/accounts/{user_id}/{account}` | `account` 为 `MARGIN_CROSS` 或 `MARGIN_ISOLATED:<交易对>`；上面一行再加 `balances`（每资产可用、冻结、借入、利息、净值，`price_usdt` 为估值所用价格——不新鲜时是最近已知价、从未有价为 null，`asset_usdt`、`liability_usdt`、`haircut`、本小时 `hourly_rate`）、`loans`（含 `opened_at`）、`loan_changes`（视图 `loan_changes` 最近 50 条：借、还、利息，`kind`、`status`、`reason`、`journal_key` 为账本分录的幂等键）、`interest`（最近 50 笔，含 `PENDING`）、`liquidations`（E3 起有内容） |
| `POST .../freeze`、`POST .../unfreeze` | 冻结：带 `reason`；`NORMAL`/`WARNED` 才能冻结，否则 409 `MARGIN_FROZEN`；冻结后不能借币、划出、下单，还币照常，计息与强平照常，并请交易服务撤掉该账户的挂单（`/internal/orders/cancel`；失败只记日志，冻结照样生效）。解冻：只解管理员的冻结，否则 409 `MARGIN_NOT_FROZEN`；解冻后为 `NORMAL`（低于预警线时由监控重新预警） |
| `POST .../liquidate` | 带 `approval_id`（第二位管理员批准的申请）：按 `MANUAL` 开始强平，同一 `approval_id` 再调返回同一笔；没有负债 409 `MARGIN_NOTHING_OWED`，已在强平中 409 `MARGIN_FROZEN` |
| `POST /internal/margin/users/{id}/settle` | 测试账户清理（用户类型设计 §1 #9，L4b；`exchangectl users purge` 调用）：正文 `{actor, reason}`（理由至少 3 个字符）。对该用户的每个杠杆账户请交易服务撤掉挂单（`/internal/orders/cancel`，计入它答的 `requested`），等这些单释放冻结（最多 8 秒），再用各账户自己持有的同币种可用余额还清借款与利息（利息在前，`AUTO_REPAY` 还款、幂等键 `settle:<UUID>`，客户端的幂等键不能用这个前缀）；不够的返回在 `remaining_debt` 里（清理工具从现货划入或补记后再调一次），账户里的其他资产不卖。答 200 `{canceled_orders, cancel_refused:[{account, symbol, code}], repaid:[{account, symbol, asset, principal, interest}], remaining_debt:[{account, symbol, asset, amount}], complete}`（`complete` 即 `remaining_debt` 为空）。重复调用只处理那时还有的：全部完成后 `canceled_orders` 为 0，`cancel_refused`、`repaid`、`remaining_debt` 都是 `[]`，`complete: true`；没有杠杆账户的用户同样如此——不问 user-service 是否有这个用户（协调会话 06:14 的约定），没有"用户不存在"的 404，`id` 不是 UUID 答 400。有账户正在强平时 409 `MARGIN_FROZEN`（details `account`，强平自己会还；什么都不撤），交易服务或账本不可达 503；交易服务拒绝某个账户的撤单（4xx）时其余照常，该账户与错误码列在响应与审计的 `cancel_refused`（审查 C79 ③、C80 ①；那些挂单可能还在，`complete` 只看负债），它们冻结的部分不能还、留在 `remaining_debt`。每次调用审计 `margin.user_settled`（details 含 cancel_refused、complete；中途失败也记，带 `error` 码，C79 ④）。耗时：撤单后最多等 8 秒，加上每笔还款记账，通常几秒；处理器把写超时延到 1 分钟，调用方超时给 30 秒以上（审查 C79 ②）。契约 `api/internal/margin.yaml` |

`journal_key`：借币 `margin:margin-borrow:<借款ID>:0`、其首小时利息 `margin:margin-borrow:<借款ID>:1`、还币 `margin:margin-repay:<还款ID>:0`、自动还款 `trade-repay:<成交ID>:<buyer|seller>`、整点利息 `margin-interest:<资产>:<整点 unix>[:<n>]`（记账时写进 `interest_charges.journal_key`）。迁移 margin 00004 加了 `accounts.frozen_by`/`frozen_at`、`loans.opened_at`（借款从 0 变为有欠款的时间）、`interest_charges.journal_key` 与视图 `loan_changes`。

## 测试服设置

- 开关现状（2026-10-06 13:25 起）：`margin.enabled`、`margin.auto_borrow`、`margin.liquidation` 都对所有人打开（enabled=true、无规则）。**上线前三者都要回到按用户或地区的规则**，后台上线清单的杠杆项据此判定。各自打开的时间与依据：
- `margin.enabled` 已于 2026-10-06 07:06（北京时间；UTC 10-05 23:06:48）对所有人打开：审查 CR ③ 的条件是 C11 部署（7039fc5）且 margin.sh 通过（65 项），条件满足后由本会话持运维锁打开（理由写在开关历史里）。`margin.auto_borrow` 依协调会话 07:40 的决定 ③ 于 2026-10-06 08:39（北京时间；UTC 00:39:36）在 E3（42a96e7）部署、margin.sh 通过（75 项）后同样对所有人打开。`margin.liquidation` 依审查 DJ 的决定（全局打开的条件：C23 ① 部署且 margin.sh 通过）于 2026-10-06 13:25（北京时间；UTC 05:25:35）在 183fac8 部署、margin.sh 通过（76 项）后对所有人打开，此后到强平线连续两轮的账户都由监控自动强平；同日 13:27 的演练在全局开关下通过。
- 故障演练：`scripts/fault/margin-liquidation.sh`（`task fault` 包含）——新用户全仓 100 USDT 自动借币买入约 210 USDT 的 ASTRA（风险率约 1.33），运营价格事件把 ASTRA 压低 20%：收件箱出现 `MARGIN_WARNED`，监控连续两轮到强平线后自动强平（三个开关未全局打开的，只对该用户打开）：只卖够负债与强平费的 ASTRA、保险基金恰好收到强平费且没有付出（按该用户的分录核对）、跌价前欠的 USDT 还清、账户回到 NORMAL、`MARGIN_LIQUIDATED` 到达；中途 `SHORTFALL`，或最新一笔强平还款被拒且一分钟没有进展（被拒的还款会换键重还，单独一笔不算失败），立即判失败。结束时价格按跌幅的精确反向回调（1 ÷ 0.8 − 1，模型此间的漂移不占额度）、开关还原，任一还原失败都判失败并给出手工命令（审查 DH 的 C20、DK 的 C22）。**跌价对整个平台生效**：所有带负债、持 ASTRA 的杠杆账户都可能被预警并收到邮件（跌价前列出个数），ASTRA 永续上的仓位可能被强平或减仓且回调不撤销——除模拟市场机器人外有人持仓时跳过（开始时、买入前、跌价前各查一次；买入后才出现的，先以 AUTO_REPAY 卖回并还清借款再跳过）。要模拟市场在运行、价格事件开着，并且一小时内没有过调价（占单人一小时额度约 45%，之后一小时 astra.sh 的调价跳过）。有人值守时跑完看一眼 `scripts/ops/astra.sh status`。
- 端到端：`scripts/e2e/margin.sh`（`task e2e` 包含）。它持运维锁，注册的用户拿到 user_id 后把这两个开关**只对这个用户**打开（加进开关的 `allow-users`，开关原本已对所有人打开时不动），结束时（含失败）还原成原来的状态；单独运行时自己取锁。

## 常用命令

```bash
# 在 margin-service 容器里（ssh exchange 后 cd /opt/exchange/infra）
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin liquidations
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin terms
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin loans
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T margin-service /app/exchangectl margin reconcile
```
