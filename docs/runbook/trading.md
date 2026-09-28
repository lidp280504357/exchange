# 现货订单运维

需求 §5.6、§11.1、§11.2；实现见 `internal/trading`（spot-trading-service，端口 HTTP 8088、运维 9088），契约 `api/openapi/trading.yaml`（`/v1/orders`）与 `api/proto/exchange/order/v1`。撮合引擎是 §6.3 任务 3，在它上线前订单停在 NEW，撤单只记"已请求"。

## 下单链路

1. 网关验令牌、限流（`user` 600/分钟 + `user_order` 1200/分钟）、处理 Idempotency-Key，转发到交易服务。
2. 校验，任一不过就返回错误、不落库：
   - 交易对 TRADING，且两种资产可交易、未被风控限制：否则 `INSTRUMENT_NOT_TRADING`。
   - 价格按 tick、数量按 lot：否则 `INSTRUMENT_PRECISION`。
   - 数量在最小与最大之间：否则 `ORDER_QUANTITY_OUT_OF_RANGE`。
   - 名义金额不低于最小值：否则 `ORDER_MIN_NOTIONAL`。
   - 价格带：否则 `ORDER_PRICE_OUT_OF_BAND`。
   - 用户资格 `SPOT_TRADE`：否则返回 `USER_*` 码。
3. 在同一用户的咨询锁下检查挂单上限（每交易对 200、总计 1000，超出为 `ORDER_TOO_MANY_OPEN`）和 `client_order_id`，然后存为 NEW，冻结状态 PENDING。
4. 调账本 gRPC `Freeze`，类型 `ORDER_FREEZE`，幂等键 `order:<订单ID>`。冻结的内容：
   - 限价买单：价格 × 数量，向上取整到计价资产精度。
   - 市价买单：`quote_amount`。
   - 卖单：数量（基础资产）。
5. 冻结成功：同一事务把冻结状态记为 FROZEN，并经 outbox 发 `order.events: OrderAccepted` 与 `order.commands: PlaceOrder`，两者都按交易对分区。PlaceOrder 带着引擎需要的全部参数（费率、资产精度、市价单保护价），重放命令就能重建订单簿。返回 202 和订单（NEW）。
6. 账本明确拒绝（余额不足、精度等）：订单存为 REJECTED（`reject_reason` 为错误码），发 `OrderRejected`，返回该错误并在 `details.order_id` 带上订单 ID。
7. 账本不可达：冻结结果未知，订单保持 PENDING，照样返回 202。恢复任务每 5 秒处理 10 秒以前的 PENDING 订单：用同一幂等键重试冻结，账本已经冻结过的不会冻结第二次，然后按第 5 或第 6 步处理。

同一 `client_order_id` 的重复请求：内容相同返回原订单（原订单被拒则返回同样的错误），内容不同返回 409 `COMMON_IDEMPOTENCY_CONFLICT`。不传则用订单 ID。

价格带与市价保护价的锚点是最新成交价（任务 3、5）或参考价（任务 7）。目前还没有锚点：限价单不检查价格带，市价单不带保护价，市价卖单也不检查最小名义金额（这些交给引擎：空订单簿的市价单会被拒）。

## 撤单

- `DELETE /v1/orders/{id}`：订单标记为已请求撤单（`cancel_requested`），经 outbox 发 `order.commands: CancelOrder`，排在它的 PlaceOrder 之后，返回 202。
  - 已成交的订单返回 `ORDER_ALREADY_FILLED`，其他已结束的订单返回 `COMMON_CONFLICT`。
  - 重复撤单不会重复发命令。
  - 冻结还没记录的订单，在补完冻结时，CancelOrder 紧跟 PlaceOrder 发出。
- `DELETE /v1/orders?symbol=`：对该用户（某交易对）的全部活跃订单逐个请求撤单，返回请求数。
- 订单最终变为 CANCELED 并解冻剩余部分，以引擎的 `order.events` 为准（任务 3）。

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
