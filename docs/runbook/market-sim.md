# market-sim：平台币 ASTRA 的模拟市场

设计见 [docs/设计-平台币ASTRA与模拟做市-2026-10-02.md](../设计-平台币ASTRA与模拟做市-2026-10-02.md)（下称"设计"）。本文对应批次 A2：价格模型与机器人集群。后台的价格控制与事件（A3）、永续（A4）还没做。

## 做什么

market-sim（`cmd/market-sim`、`internal/marketsim`，REST 8098 只在内网、运维 9098）每 250 毫秒推进一次目标价 `P(t)`，让一组机器人账户围绕它交易 ASTRA-USDT：

- 机器人是普通用户（`scripts/ops/astra.sh seed` 照常注册），经交易服务与账本的内网接口下单（`X-User-Id`，同网关转发的身份），撮合、结算、行情都走平台自己的路径。ASTRA-USDT 没有 `reference_symbol`，所以它的 K 线、ticker、盘口、成交都来自这些成交（见 [market-data.md](market-data.md)）；HOUSE 不在这个交易对上报价。
- 机器人的成交互相抵消，整个机器人池等同平台（设计 §2 第 3 条）；用户买卖 ASTRA 的对手方就是它们。
- 单实例持数据库租约 `market-sim`，另一实例等待接替。状态（锚点、自身偏离、随机源）每 5 秒存一次，停止时再存一次，重启后接着走，做市商的挂单留在簿上，重启后按价格逐个接管，不会全部重挂。
- 开关 `sim.enabled`（按交易对，默认关）：打开、且交易对是 `TRADING` 时机器人才交易；关掉或交易对不再 `TRADING` 时撤掉做市商的全部挂单。`sim.events`、`sim.perp` 留给 A3、A4。

## 价格模型（设计 §3）

```
P(t) = P0 × M(t) × exp(X(t)) × E(t)
M(t) = exp( β × ( w_btc × ln(BTC_t / BTC_0) + w_eth × ln(ETH_t / ETH_0) ) )
dX   = −θ X dt + μ dt + σ dW          （θ 按小时，μ、σ 按天）
```

- BTC、ETH 取 market-data-service 内网参考价（`/internal/market/{symbol}/reference`，每秒读一次）；不新鲜时 `M` 保持上一个值（指标 `market_sim_references_fresh`）。第一次拿到新鲜价格时锚定 `BTC_0`、`ETH_0`。
- 守卫：目标价一分钟内最多变动 `max_minute_move`（默认 3%），只限制目标价的速度，不改模型：大盘跳 10% 时目标价每分钟追 3%，几分钟后追上；下限 0.0001、上限 1,000,000。触发次数 `market_sim_guards_total{guard}`。
- 模型用 float64（对数与高斯噪声），价格与数量出模型时按交易对的 tick 与 lot 转成 decimal。随机源可设种子（`SIM_SEED`），状态里存着随机源，测试可复现。
- `E(t)`（后台事件）在 A3 加入，现在恒为 1。

## 机器人（设计 §4）

| 角色 | 测试服数量 | 行为 |
|---|---|---|
| MAKER 做市商 | 6 | 每侧 `levels`（8）档，最优买卖相距 `spread`（0.2%），档距 `level_ticks`（5 个 tick）；价格放在按做市商错开相位的网格上，目标价小幅移动时大部分挂单不动。目标价偏离上次报价 `requote_ticks`（3 个 tick）或每 1–3 秒（随机）重报一次：撤掉不再需要的价位、补上缺的价位（由近到远、买卖交替）；每轮最多 2 个做市商重报。每档价值对数正态，中位 `level_size`（800 USDT） |
| TAKER 噪声交易者 | 12 | 泊松到达，平均每天 `daily_volume / order_size` 单（默认 2,000,000 / 400 = 5,000 单），按 UTC 小时加权（欧美重叠时段最多）；方向五五开，按 `mu` 最多偏 10 个百分点，按自己的 USDT 偏离 `bot_usdt` 最多偏 20 个百分点；市价单，价值对数正态，中位 `order_size` |
| TREND 趋势交易者 | 4 | 每半分钟左右看目标价最近 `trend_minutes`（15）分钟的方向，各以 `trend_strength`（0.3）的概率顺势下市价单 |
| EXECUTOR 事件执行者 | 2 | A3 的事件用，现在不交易 |

- 节流：全部机器人合计每秒 `orders_per_second`（20）单、`cancels_per_second`（10）次撤单（令牌桶），超出的这一轮放弃（`market_sim_throttled_total`）。
- 余额：每 10 分钟（以及开始交易时）读一遍每个机器人的 SPOT 余额（`market_sim_inventory{asset}` 是合计）；余额不够的单子被账本拒绝时计为 `unfunded`，下一轮再说。整体不够时用 `astra.sh mint` 增发。
- 机器人全部零手续费：它们的用户 ID 在 `MARKET_MAKER_USER_IDS`（spot-trading-service、derivatives-service 读，`astra.sh seed` 写入 `apps.env` 并重启这两个服务）。

## 设置

设置存在 `marketsim.settings`（一行 JSON，`version` 每次加一），首次启动写入默认值；改设置走管理接口（后台的 A3 页面也调它），下一轮生效。字段（数值，均为模型参数，不是账务金额）：

`p0`、`w_btc`、`w_eth`、`beta`、`theta`、`sigma`、`mu`、`max_minute_move`、`floor`、`ceiling`、`levels`、`spread`、`level_ticks`、`level_size`、`requote_ticks`、`daily_volume`、`order_size`、`trend_minutes`、`trend_strength`、`orders_per_second`、`cancels_per_second`、`bot_usdt`；含义与默认值见 `internal/marketsim/domain/params.go`，`Validate` 给出范围。

环境变量（compose 的 market-sim 段）：`TRADING_SERVICE_URL`、`LEDGER_SERVICE_URL`、`MARKET_DATA_SERVICE_URL`、`INSTRUMENT_SERVICE_URL`；`SIM_SYMBOL`（默认 ASTRA-USDT）、`SIM_QUOTE`（默认 USDT）、`SIM_SEED`（默认 0，取时钟）。

## 管理接口（内网，`market-sim:8098`，网关不转发）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/internal/sim` | 状态：`symbol`、`enabled`、`running`、`target_price`、`last_price`（十进制字符串，可为 null）、`references_fresh`、`params`、`version`、`guards`（按守卫计数）、`bots`（`user_id`、`role`、`label`、`enabled`、`balances_known`、`usdt`、`coin`、`error`、`error_at`）、`at` |
| PUT | `/internal/sim/params` | `{"params": {...全部字段...}, "actor": "操作人"}` → `{"version": n}`；不合法返回 400 |
| POST | `/internal/sim/bots` | `{"user_id", "role": "MAKER|TAKER|TREND|EXECUTOR", "label"}` → 204；同一用户再登记无变化，标签被别的用户占用返回 409；一分钟内开始交易 |

后台（admin-service，另一个会话负责）按这个接口做"模拟市场"页面（设计 §6）。

## 运维

首次在测试服启用（部署之后）：

```bash
# 1. 机器人：注册 24 个用户、交给 market-sim、注入 10 亿 ASTRA 与每个 100,000 USDT、设为零手续费（持运维锁，会重启两个交易服务）
scripts/ops/astra.sh seed
# 2. 开放交易对，打开机器人
scripts/ops/astra.sh open
scripts/ops/astra.sh on
# 3. 看状态：目标价、最近成交价、每个机器人的余额与最近错误
scripts/ops/astra.sh status
```

- `astra.sh off` 关掉机器人（撤掉做市商挂单）；`astra.sh mint 50000000` 给机器人再增发 5000 万 ASTRA（平均分，`mint 100000 USDT` 增发 USDT），都是带审计的账本调整，需要开关 `ledger.manual_adjustment`。
- 重新跑 `seed` 只注册还没有的机器人；调整用固定的幂等键，不会重复入账。

## 指标与告警

`market_sim_target_price`、`market_sim_last_price`、`market_sim_running`、`market_sim_references_fresh`、`market_sim_inventory{asset}`、`market_sim_orders_total{role,result}`（placed、unfunded、failed）、`market_sim_cancels_total{role}`、`market_sim_guards_total{guard}`、`market_sim_throttled_total{kind}`、`market_sim_errors_total{op}`。

告警：`MarketSimFailing`（10 分钟失败超过 100 次）、`MarketSimReferencesStale`（运行中 5 分钟没有新鲜的 BTC/ETH 参考价）。

## 还没做（后续批次）

- A3：后台价格控制与事件（`E(t)`、跳涨跳跌、目标价、趋势、波动、暂停、停牌、重新锚定）、守卫与双人审批、审计、事件主题 `market.sim.events`。
- A4：`platform:` 指数源与 ASTRA-USDT-PERP，机器人在永续上做市（`sim.perp`）；derivatives 的"只与 HOUSE 成交"判断要加"有参考市场"条件。
- A5：market-sim 心跳中断 60 秒自动停牌、故障注入、ADR-0016。
- 后台按 `bot` 标记过滤机器人的订单与成交：用户标签在 admin 的库里，需要后台会话提供写入方式。
