# 现货订单运维

需求 §5.6、§11.1、§11.2；实现见 `internal/trading`（spot-trading-service，端口 HTTP 8088、运维 9088），契约 `api/openapi/trading.yaml`（`/v1/orders`、`/v1/fills`）与 `api/proto/exchange/order/v1`。撮合见 [matching.md](matching.md)。

## 下单链路

1. 网关验令牌、限流（`user` 600/分钟 + `user_order` 1200/分钟）、处理 Idempotency-Key，转发到交易服务。
2. 校验，任一不过就返回错误、不落库：
   - 交易对 TRADING，且两种资产可交易、未被风控限制：否则 `INSTRUMENT_NOT_TRADING`。
   - 价格按 tick、数量按 lot：否则 `INSTRUMENT_PRECISION`。
   - 数量在最小与最大之间：否则 `ORDER_QUANTITY_OUT_OF_RANGE`。
   - 名义金额不低于最小值：否则 `ORDER_MIN_NOTIONAL`。
   - 价格带：否则 `ORDER_PRICE_OUT_OF_BAND`。
   - 用户资格 `SPOT_TRADE`（杠杆账户的单是 `MARGIN_TRADE`，见下面"杠杆账户的订单"）：否则返回 `USER_*` 码。
3. 在同一用户的咨询锁下检查挂单上限（每交易对 200、总计 1000，超出为 `ORDER_TOO_MANY_OPEN`）和 `client_order_id`，然后存为 NEW，冻结状态 PENDING。
4. 调账本 gRPC `Freeze`，类型 `ORDER_FREEZE`，幂等键 `order:<订单ID>`。冻结的内容：
   - 限价买单：价格 × 数量，向上取整到计价资产精度。
   - 市价买单：`quote_amount`。
   - 市价买单按数量（B157，2026-10-07 起，币安同样有）：数量 × 保护价（锚点之上一个价格带，按 tick 向下取），向上取整到计价资产精度；要有锚点，没有时返回 `COMMON_INVALID_ARGUMENT`（请改用 `quote_amount`），`quantity` 与 `quote_amount` 只能给一个。送给引擎时是保护价上的限价 IOC（或 FOK）买单（与 derivatives-service 送市价单同一做法，引擎不用多一种市价单）：成交里带 `buyer_limit_price` = 保护价，账本结算时当场释放每笔与成交价的差额，订单结束时按限价买单释放剩下的（保护价 × 未成交数量）。`OrderAccepted` 与订单记录仍是 MARKET；没成交完的部分撤销原因为 `IOC`。订单表的形状约束由迁移 trading 00008 放开（市价买单 `quote_amount` 与 `quantity` 二选一，按数量的必须有 `protection_price`；新约束先 NOT VALID 加上、再去掉旧约束、再单独校验，没有一刻没有约束，每一步都可重跑（B162），校验期间订单照常可写）。杠杆账户上用 `AUTO_BORROW` 按数量市价买时，按保护价冻结也就按它借币，多借的价格带在订单结束后还掉（B160，见下一条）。
   - 卖单：数量（基础资产）。
5. 冻结成功：同一事务把冻结状态记为 FROZEN，并经 outbox 发 `order.events: OrderAccepted` 与 `order.commands: PlaceOrder`，两者都按交易对分区。PlaceOrder 带着引擎需要的全部参数（费率、资产精度、市价单保护价、`house_only`），重放命令就能重建订单簿。返回 202 和订单（NEW）。
   - `house_only`（阶段 4 B4，ADR-0015）：交易对跟随参考市场、`market.house_liquidity` 对它打开且 `market.internal_matching` 关闭时为真，订单只和 HOUSE 的虚拟流动性成交（见 [market-maker.md](market-maker.md)）。与 HOUSE 成交时用户照常付手续费（HOUSE 不付），结算见 [ledger.md](ledger.md#house-的现货成交adr-0013)。
6. 账本明确拒绝（余额不足、精度等）：订单存为 REJECTED（`reject_reason` 为错误码），发 `OrderRejected`，返回该错误并在 `details.order_id` 带上订单 ID。
7. 账本不可达：冻结结果未知，订单保持 PENDING，照样返回 202。恢复任务每 5 秒处理 10 秒以前的 PENDING 订单（每次最老的 100 笔）：用同一幂等键重试冻结，账本已经冻结过的不会冻结第二次，然后按第 5 或第 6 步处理。
   - 什么算"明确拒绝"、什么算"结果未知"，下单与恢复用同一个判定：账本或 margin-service 带错误码的回答（参数、余额、找不到、未登录、限流、冲突、禁止，以及虽是 503 的 `MARGIN_PRICE_UNAVAILABLE`）都是拒绝，订单记 REJECTED；内部错误、不可用、超时与取消才是结果未知。恢复遇到拒绝时这笔结束、接着处理下一笔。
   - 每次调账本与 margin-service 最多等 5 秒（`Service.CallTimeout`，写在代码里、没有配置项），超时按结果未知、订单留 PENDING；恢复任务每一轮补冻结最多 30 秒、补解冻另有 30 秒（最前面卡住的杠杆单耗不到解冻的时间），到点就停、下一轮接着处理，只把真正有了结果（接受或拒绝）的订单算作"已恢复"。依赖挂住时请求与恢复循环都不会被卡死（审查 CN、CP）。
   - REJECTED 是终态：同一 `client_order_id` 再提交得到同一个错误（含 503 的 `MARGIN_PRICE_UNAVAILABLE`），要重试就换一个 `client_order_id`。
   - 指标 `trading_orders_pending_freeze`（PENDING 订单数，含最近几秒内正在处理的）与 `trading_orders_pending_freeze_oldest_seconds`（最老一笔已等的秒数，没有为 0）每 5 秒在恢复之前更新、有自己的 5 秒期限，依赖挂住时也照常变化；最老一笔超过 5 分钟持续 5 分钟告警 `TradingOrdersPendingFreeze`（最前面卡住的订单会让后面的轮不到）。排查：`SELECT id, account_type, created_at FROM trading.orders WHERE freeze_state = 'PENDING' ORDER BY created_at LIMIT 20;`，再看服务日志里 `did not complete` 的原因。

同一 `client_order_id` 的重复请求：内容相同返回原订单（原订单被拒则返回同样的错误：冻结或 margin-service 的拒绝连同 HTTP 状态与 details 记在订单的 `reject_status`、`reject_details`，迁移 trading 00006，重复请求原样返回；撮合引擎的拒单没有这两项，按 422），内容不同返回 409 `COMMON_IDEMPOTENCY_CONFLICT`。账户或 `side_effect` 不同也算内容不同。不传则用订单 ID。

### 杠杆账户的订单（杠杆设计 2026-10-06，批次 E2）

- 请求可带 `account`（`SPOT` 默认、`MARGIN_CROSS`、`MARGIN_ISOLATED` 即该交易对的逐仓账户）与 `side_effect`（`NONE` 默认、`AUTO_BORROW`、`AUTO_REPAY`，只用于杠杆账户；`SPOT` 带别的值返回 `COMMON_INVALID_ARGUMENT`）。订单表的 `account_type`、`side_effect` 两列（迁移 trading 00004，之前的订单都是 `SPOT` / `NONE`），响应、`OrderAccepted` 与 `PlaceOrder` 里的订单都显式带上。
- `margin.enabled` 对该用户关闭时，杠杆账户的订单直接返回 403 `MARGIN_DISABLED`，不落库；`AUTO_BORROW` 在 `margin.auto_borrow` 关闭时同样被拒，`details.flag` 为 `margin.auto_borrow`。开关之后查资格：杠杆单查 `MARGIN_TRADE`（只 ACTIVE、受 `margin.enabled` 的全部规则，与两站和 margin-service 同一个资格，审查 CO），不符合返回 user-service 的原因码（`USER_RISK_REVIEW` 等）；现货订单仍查 `SPOT_TRADE`、不看这两个开关，链路与之前完全一样。
- 冻结之前先调 margin-service 的 gRPC `MarginService.ReserveOrder`（地址 `MARGIN_GRPC_ADDR`，测试服 `margin-service:9199`）：检查账户状态、资产、风险率，`AUTO_BORROW` 时借入差额；按订单 ID 幂等。明确拒绝（`MARGIN_LIMIT`、`MARGIN_LEVEL_TOO_LOW`、`MARGIN_FROZEN`、`MARGIN_PRICE_UNAVAILABLE` 等）时订单存为 REJECTED，错误原样返回，margin-service 的 details（`max_borrowable`、`margin_level` 等）与 `order_id` 一起带回；margin-service 不可达时与账本不可达一样保持 PENDING，恢复任务先重放 `ReserveOrder` 再冻结。margin-service 还没有这个调用（`Unimplemented`）时按 `MARGIN_DISABLED` 拒绝，不留 PENDING。
- 借到的数量与借款 ID（`ReserveOrder` 的 `borrowed`、`borrow_id`）记在订单的 `borrowed`、`borrow_id` 两列（迁移 trading 00005；00006 加约束两者同有同无）并写一条 info 日志；冻结随后被拒时借款不退，订单上照样留着记录，用户自己还。借到负数、或借到却没有借款 ID 的回答不合契约，按结果未知处理（恢复任务再问一次）。
- margin-service 的内部接口（只在 compose 网络内，网关不转）：强平市价单 `POST /internal/orders/liquidations`（`liquidation_id`、`user_id`、`account`、`symbol`、`side`、卖出给 `quantity`、买入给 `quote_amount`，可选 `side_effect` 与 `attempt`；不查开关、资格、挂单上限与最小名义额，不调 `ReserveOrder`；有参考市场的交易对不设保护价，没有的（平台币等）保留锚价一半到两倍；按强平 ID + 交易对 + 方向 + `attempt`（默认 1，被拒后重下或分批用下一个）幂等，订单记 `liquidation_id`，迁移 trading 00007）与强平前撤单 `POST /internal/orders/cancel`（`user_id`、`account`，逐仓带 `symbol`；只撤该账户的活跃订单）。两个接口拒绝带 `X-User-Id` 的请求（经网关来的，404）。契约见 E0 草案 §3.4。
- 冻结与解冻都记在订单的账户上：账本 `Freeze` / `Unfreeze` 的 `account_type` 为订单的账户，逐仓账户的 `scope` 为交易对。撮合引擎把双方订单的账户与 `side_effect` 原样写进 `TradeExecuted`（HOUSE 一侧为空），账本按它结算（见 [margin.md](margin.md)）。

价格带与市价保护价的锚点：跟随参考市场的交易对（`reference_symbol` 非空）先取新鲜的参考价（market-data-service 内网接口 `/internal/market/{symbol}/reference`：币安价，价格事件期间是叠加后的价，HOUSE 也按它报价，见 [market-maker.md](market-maker.md)），没有才用最后一笔成交价，不论多旧——2026-10-07 之前是 5 分钟内的成交价优先，价格事件把参考价推高 16% 时，带内的新价限价单被拒、市价买保护价够不到 HOUSE 按新价挂的卖单，直到有一笔成交打到新价位（审查 B144）。不跟随的交易对（平台币 ASTRA-USDT）照旧：① 5 分钟内的最新成交价；② 新鲜的参考价（平台自己的价格（60 秒 TWAP）或盘口中价，都没有时是 market-sim 30 秒内上报的目标价，见 [market-data.md](market-data.md)、[market-sim.md](market-sim.md#价格带不锁死市场设计-4)），这样一笔旧成交不会把它锁死；③ 最后一笔成交价，不论多旧——market-sim 的走价按同一规则算锚点，B144 一度让它也先取 TWAP，走价时做市档越带（审查 B150）。成交价由交易服务从自己的 `fills` 取；缓存 1 秒。规则：限价价格偏离锚点超过交易对的 `price_band` 返回 `ORDER_PRICE_OUT_OF_BAND`；市价买单保护价为锚点 × (1 + band)，卖单为锚点 × (1 − band)，市价卖单还按锚点检查最小名义金额。既无成交也无参考价的交易对没有锚点：限价单不检查价格带，市价单不带保护价（空订单簿的市价单由引擎拒绝）。测试数据里 ETH-BTC（端到端脚本用的交易对）的价格带是 100%，BTC-USDT 为 10%。做市账户（`MARKET_MAKER_USER_IDS`）的订单手续费率为 0。

## 撤单

- `DELETE /v1/orders/{id}`：订单标记为已请求撤单（`cancel_requested`），经 outbox 发 `order.commands: CancelOrder`，排在它的 PlaceOrder 之后，返回 202。
  - 已成交的订单返回 `ORDER_ALREADY_FILLED`，其他已结束的订单返回 `COMMON_CONFLICT`。
  - 重复撤单不会重复发命令。
  - 冻结还没记录的订单，在补完冻结时，CancelOrder 紧跟 PlaceOrder 发出。
- `DELETE /v1/orders?symbol=`：对该用户（某交易对）的全部活跃订单逐个请求撤单，返回请求数。
- 订单最终变为 CANCELED（`cancel_reason` USER）并解冻剩余部分，以引擎的 `order.events` 为准。

## 现货产品线开关（设计 2026-10-07 产品线开关，批次 K1a）

- 开关 `product.spot`（默认开，见 [feature-flags.md](feature-flags.md)）。关闭后新单一律返回 403 `PRODUCT_CLOSED`（`details.product` 为 `spot`），不落库：在幂等键与 `client_order_id` 之后检查，存单前在同一用户的锁下再查一次；撤单照常。杠杆账户的单同样受它管（同一盘口，设计 §1 #7 的 16:59 更正），只放行还款单：`side_effect` 为 `AUTO_REPAY`、且该杠杆账户欠着订单带来的资产（卖单带来计价资产，买单带来基础资产——空头靠买入还币），欠款经账本 gRPC `GetMarginBalances` 查（本金加利息），在 `margin.enabled` 与交易对之后、资格之前；带来的超过欠款的部分留在账户里。存单前锁内复查时，只有开头就看到关闭（查过欠款）的还款单放行，开头还开着的一律拒绝、重试时再查欠款（审查 B155）。不受它管的：margin-service 的强平单（不经下单接口），以及做市账户（`MARKET_MAKER_USER_IDS`，即 market-sim 的机器人）——与 HOUSE 一样照常报价（设计 §1 #3 的 16:48 更正：所有成交都对它们，停了就没有对手方）。
- 关闭时由 admin-service（后台「产品线」卡，批次 K3）调内部接口 `POST /internal/products/spot/cancel-open`（`{actor, reason}`；只在 compose 网络内，带 `X-User-Id` 的请求 404）：先重读开关，还开着就返回 409 `COMMON_CONFLICT`；然后对全部用户（做市账户除外）现货与杠杆账户里关闭后不再收的活跃订单逐个请求撤单（每个用户一个事务；强平单与还款单——`AUTO_REPAY` 且账户欠着它带来的资产，在事务里问账本，账本答不上来按不是还款撤掉、审计 details 加 `"ledger": "unread"`（B156）——都留着，所以重跑也不会撤掉关闭后下的还款单，审查 B155），每单在同一事务里经 outbox 发审计 `admin.orders.canceled`（目标 `user:<用户ID>`，details 含 `order_id`、`symbol`、`account_type`、`product`；ClickHouse `audit_logs` 的 `actor_id` 是调用方给的 `actor`）；1 秒后再扫一遍，收走关闭那一刻正在下的单。返回 202 `{canceled, orders: [{order_id, user_id, symbol, type: ORDER}]}`（`type` 与合约的接口一致，合约另有 `CONDITIONAL`），已请求过撤单的不再计入。重开不回放撤掉的单。 某个用户撤不下来（锁超时等）时跳过他、接着处理其他人；第二遍再试一次，仍有撤不下的就返回 503 `COMMON_UNAVAILABLE`（details `canceled`、`failed_users`，都是个数），重跑收走剩下的（与合约侧一致，审查 C60）。
- 兜底：恢复任务每 5 秒看一次，现货关着时把关闭前 30 秒起存下、关闭后不再收的单（做市账户、强平单与杠杆还款单除外）撤掉，审计的 `actor` 为 `system:spot-trading-service`——将来有多个实例、某个实例的开关副本晚了几秒放进来的单，或者只用 `exchangectl` 关了开关、没调撤单接口时关闭前后进来的单。更早的单只由撤单接口撤。
- 计数 `GET /internal/products/spot` → `{product: "spot", closed, open_orders, open_positions}`：`open_orders` 是现货与杠杆账户的活跃订单数（做市账户与强平单除外；还款单与已请求撤单、引擎还没确认的仍算），`open_positions` 是欠着款的杠杆账户数（账本 `ListMarginDebts`，最多每 15 秒数一次）；契约见 `api/internal/products.yaml`（三条线同形），给后台的「产品线」卡与确认框用；账本不可达时整个接口报错，后台按读不到处理。
- 关闭期间行情照常；market-sim 只暂停平台币的现货机器人，模型与永续的机器人照常（见 [market-sim.md](market-sim.md#产品线开关设计-2026-10-07)）；向合约账户的划入归合约产品线管（见 [ledger.md](ledger.md#接口)）。
- 手动：`exchangectl flags set product.spot --off --reason ...` 关、`--on` 开。只改开关不撤旧单（兜底只管关闭前 30 秒以后的：开关的时间是数据库的钟、订单的是服务的钟，除了开关副本最多 5 秒的滞后，余下是两只钟的差，与合约侧相同，审查 C60），撤单用上面的接口（后台切换时一并完成）。
- 端到端：`scripts/e2e/trading.sh` 最后一节把现货关闭约半分钟——新单被拒、计数接口看得到自己的挂单、撤单接口收走它并留下审计、重开后订单回到自己的检查；中途失败时退出前把开关打开（打不开就报 FAIL 并给出手动命令）。它撤的是全部用户的现货挂单（做市账户除外），与运营关闭时一样。现货关着时平台币永续（指数取自平台现货）的标记价停更，10 秒后进入只减仓，重开不会自动解除（B165，2026-10-07 起 ASTRA 两个永续因此只减仓两天）：脚本在关闭前取服务器时间，结束时（成败都一样）用 `scripts/e2e/lib/remote.sh` 的 `lift_reduce_only` 解除这以后因 `INDEX_SOURCES` 或 `MARK_PRICE_STALE` 进入的只减仓——与部署脚本同一口径，等各自标记价不再 `degraded` 再解除，解除人记为 `e2e-trading.sh`，最多等 200 秒，解不掉就 FAIL 并给出手动命令。

## 引擎事件与解冻

交易服务消费 `order.events`（跳过自己发的 OrderAccepted 和无 sequence 的 OrderRejected）与 `trade.events`，消费组为 `spot-trading-service`：

- 订单更新只在事件 sequence 大于订单记录的 sequence 时生效，重投与乱序都无害。更新的内容包括状态、累计成交数量与金额、撤销原因（USER、IOC、FOK、SELF_TRADE、NO_LIQUIDITY）或引擎拒单码（`ORDER_WOULD_TAKE`、`ORDER_NO_LIQUIDITY`、`ORDER_SELF_TRADE`）。
- 订单进入终态（FILLED、CANCELED、REJECTED）后，解冻"冻结额 − 成交消耗"，调账本 `Unfreeze`（`ORDER_UNFREEZE`，幂等键 `release:<订单ID>`），完成后标记 `released`。成交消耗的计算：
  - 卖单：成交数量；
  - 市价买单：成交金额；
  - 限价买单：限价 × 成交数量（成交价低于限价的差额由结算当场释放，见 [ledger.md](ledger.md#成交结算)）。
- 账本不可达时，事件重试；恢复任务每 5 秒补解冻 10 秒以前完成、还没解冻的订单。
- 借了币的杠杆单（B160）：杠杆账户上的订单在 margin-service 借过币（`borrowed` > 0）时，结束后把冻结里回来的在借款额度内还掉——未用的冻结；按数量的市价买还包括保护价在成交价之上多冻、结算时放回的部分（`domain.Order.BorrowToRepay`）。为等账本把成交结算完（价差在结算时放回），订单结束 10 秒内只解冻、不标记已解冻，恢复任务（每 5 秒、取 10 秒以前完成的）再调账本 `RepayReleased`（见 [ledger.md](ledger.md)）并标记；一笔订单只还一次。时间只是起点（B163）：调用带上该单的成交数量，账本没把它的成交结算完就拒绝（`LEDGER_TRADES_UNSETTLED`），订单留着不标记，下一轮再来，一直等到结算——不按时间放弃（B164）：订单结束一小时后调用改带 `skip_failed_trades`，只越过账本停成 FAILED 的成交（它们要等原因修好才结算），按当时可用还；账本还没记录的成交照样等（结算积压时不会把价格带当成最终结果留作借款）。恢复任务某一笔失败时接着处理后面的（B163），并在订单上记 `release_attempted_at`（迁移 trading 00009）：每轮取「结束或上次尝试以来等得最久」的 100 笔，一直失败的排到后面，不再挡住别的（以前按结束时间取，前 100 笔卡住时后面的轮不到）。日志：订单结束 5 分钟内还在等结算的只记 Debug（结算慢几秒很常见），之后与其他失败一样记 Warn。指标 `trading_orders_unreleased`（结束了、解冻或还款没完成的订单数，含最近 10 秒内结束的）与 `trading_orders_unreleased_oldest_seconds`（其中结束最早的一笔已过去的秒数，没有为 0）与冻结的两个指标一起每 5 秒更新；超过 15 分钟持续 5 分钟告警 `TradingOrderReleasesStuck`。排查：`SELECT id, account_type, status, filled_quantity, updated_at, release_attempted_at FROM trading.orders WHERE released = false AND freeze_state = 'FROZEN' AND status IN ('FILLED','CANCELED','REJECTED','EXPIRED') ORDER BY updated_at LIMIT 20;`，再在账本查该单的成交（`SELECT trade_id, status, error_code FROM ledger.trades WHERE buyer_order_id = '<订单ID>' OR seller_order_id = '<订单ID>';`）：没有记录是账本的成交消费落后，FAILED 见 [ledger.md](ledger.md) 的失败成交重试。全部成交的限价买单没有未用冻结，借款照旧留着（价格改善的部分不还，杠杆端到端按此核对借款额）；撤销的限价单还掉未用部分。端到端 `margin.sh` 的 B160 一步：按数量市价买约 30 USDT 的 SOL（20 USDT 自有），约 10 秒后借款只剩成交花费超出自有的部分。
- 成交记录：每笔成交为买卖双方各写一条 `fills`，按成交与订单去重。`GET /v1/orders/{id}/fills` 按 sequence 列出，`GET /v1/fills?symbol=` 按时间倒序分页。
- 成交消耗的冻结资金由账本结算转给对手方（`TRADE_SETTLE`/`TRADE_FEE`），与这里的解冻互不依赖先后。

## 开放交易对

种子交易对初始为 PREPARE，`exchangectl instruments apply` 不改已有交易对的状态。测试环境开放 BTC-USDT：

```bash
exchangectl instruments pair-status BTC-USDT --to TRADING --reason "phase 2: spot orders"
```

需求 §11.10 要求交易对开放前有做市报价，这一条在上线前检查（做市机器人是任务 7）。

## 查看与排查

```bash
exchangectl ledger balances <user_id>        # 冻结是否到位（frozen）
```

- ClickHouse：`SELECT occurred_at, event_type, payload FROM events WHERE topic = 'order.events' ORDER BY occurred_at DESC LIMIT 20`
- 命令主题 `order.commands` 不入 ClickHouse，用 `rpk topic consume order.commands -n 5` 查看。
- 恢复任务的日志：`orders recovered`、`order freeze did not complete`。
- 指标：`http_server_requests_total{route=~"/v1/orders.*"}`、`outbox_pending{schema="trading"}`。
