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

价格带与市价保护价的锚点按顺序取：① 该交易对 5 分钟内的最新成交价（交易服务从自己的 `fills` 取）；② 新鲜的参考价（market-data-service 内网接口 `/internal/market/{symbol}/reference`：跟随参考市场的交易对是币安价，见 [market-maker.md](market-maker.md)；不跟随的平台币 ASTRA-USDT 是平台自己的价格或盘口中价，都没有时是 market-sim 30 秒内上报的目标价，见 [market-data.md](market-data.md)、[market-sim.md](market-sim.md#价格带不锁死市场设计-4)），这样一笔旧成交不会把没有参考市场的交易对锁死；③ 最后一笔成交价，不论多旧；缓存 1 秒：限价价格偏离锚点超过交易对的 `price_band` 返回 `ORDER_PRICE_OUT_OF_BAND`；市价买单保护价为锚点 × (1 + band)，卖单为锚点 × (1 − band)，市价卖单还按锚点检查最小名义金额。既无成交也无参考价的交易对没有锚点：限价单不检查价格带，市价单不带保护价（空订单簿的市价单由引擎拒绝）。测试数据里 ETH-BTC（端到端脚本用的交易对）的价格带是 100%，BTC-USDT 为 10%。做市账户（`MARKET_MAKER_USER_IDS`）的订单手续费率为 0。

## 撤单

- `DELETE /v1/orders/{id}`：订单标记为已请求撤单（`cancel_requested`），经 outbox 发 `order.commands: CancelOrder`，排在它的 PlaceOrder 之后，返回 202。
  - 已成交的订单返回 `ORDER_ALREADY_FILLED`，其他已结束的订单返回 `COMMON_CONFLICT`。
  - 重复撤单不会重复发命令。
  - 冻结还没记录的订单，在补完冻结时，CancelOrder 紧跟 PlaceOrder 发出。
- `DELETE /v1/orders?symbol=`：对该用户（某交易对）的全部活跃订单逐个请求撤单，返回请求数。
- 订单最终变为 CANCELED（`cancel_reason` USER）并解冻剩余部分，以引擎的 `order.events` 为准。

## 引擎事件与解冻

交易服务消费 `order.events`（跳过自己发的 OrderAccepted 和无 sequence 的 OrderRejected）与 `trade.events`，消费组为 `spot-trading-service`：

- 订单更新只在事件 sequence 大于订单记录的 sequence 时生效，重投与乱序都无害。更新的内容包括状态、累计成交数量与金额、撤销原因（USER、IOC、FOK、SELF_TRADE、NO_LIQUIDITY）或引擎拒单码（`ORDER_WOULD_TAKE`、`ORDER_NO_LIQUIDITY`、`ORDER_SELF_TRADE`）。
- 订单进入终态（FILLED、CANCELED、REJECTED）后，解冻"冻结额 − 成交消耗"，调账本 `Unfreeze`（`ORDER_UNFREEZE`，幂等键 `release:<订单ID>`），完成后标记 `released`。成交消耗的计算：
  - 卖单：成交数量；
  - 市价买单：成交金额；
  - 限价买单：限价 × 成交数量（成交价低于限价的差额由结算当场释放，见 [ledger.md](ledger.md#成交结算)）。
- 账本不可达时，事件重试；恢复任务每 5 秒补解冻 10 秒以前完成、还没解冻的订单。
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
