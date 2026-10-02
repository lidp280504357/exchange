# market-sim：平台币 ASTRA 的模拟市场

设计见 [docs/设计-平台币ASTRA与模拟做市-2026-10-02.md](../设计-平台币ASTRA与模拟做市-2026-10-02.md)（下称"设计"）。本文对应批次 A2（价格模型与机器人集群）、A3 的服务端（价格事件、守卫与审计；后台页面归后台会话）与 A4（平台币永续）。

## 做什么

market-sim（`cmd/market-sim`、`internal/marketsim`，REST 8098 只在内网、运维 9098）每 250 毫秒推进一次目标价 `P(t)`，让一组机器人账户围绕它交易 ASTRA-USDT：

- 机器人是普通用户（`scripts/ops/astra.sh seed` 照常注册），经交易服务与账本的内网接口下单（`X-User-Id`，同网关转发的身份），撮合、结算、行情都走平台自己的路径。ASTRA-USDT 没有 `reference_symbol`，所以它的 K 线、ticker、盘口、成交都来自这些成交（见 [market-data.md](market-data.md)）；HOUSE 不在这个交易对上报价。
- 机器人的成交互相抵消，整个机器人池等同平台（设计 §2 第 3 条）；用户买卖 ASTRA 的对手方就是它们。
- 单实例持数据库租约 `market-sim`，另一实例等待接替。状态（锚点、自身偏离、随机源）每 5 秒存一次，停止时再存一次，重启后接着走，做市商的挂单留在簿上，重启后按价格逐个接管，不会全部重挂。
- 开关 `sim.enabled`（按交易对，默认关）：打开、且交易对是 `TRADING` 时机器人才交易；关掉或交易对不再 `TRADING` 时撤掉做市商的全部挂单。`sim.events` 见「价格事件」，`sim.perp` 见「永续」。

## 价格模型（设计 §3）

```
P(t) = P0 × M(t) × exp(X(t)) × E(t)
M(t) = exp( β × ( w_btc × ln(BTC_t / BTC_0) + w_eth × ln(ETH_t / ETH_0) ) )
dX   = −θ X dt + μ dt + σ dW          （θ 按小时，μ、σ 按天）
```

- BTC、ETH 取 market-data-service 内网参考价（`/internal/market/{symbol}/reference`，每秒读一次）；不新鲜时 `M` 保持上一个值（指标 `market_sim_references_fresh`）。第一次拿到新鲜价格时锚定 `BTC_0`、`ETH_0`。
- 守卫：目标价一分钟内最多变动 `max_minute_move`（默认 3%），只限制目标价的速度，不改模型：大盘跳 10% 时目标价每分钟追 3%，几分钟后追上；下限 0.0001、上限 1,000,000。触发次数 `market_sim_guards_total{guard}`。
- 模型用 float64（对数与高斯噪声），价格与数量出模型时按交易对的 tick 与 lot 转成 decimal。随机源可设种子（`SIM_SEED`），状态里存着随机源，测试可复现。
- `E(t)` 是运营事件的因子（下一节），没有事件时保持上一个值（初始为 1）。

## 价格事件（设计 §6.2，A3）

开关 `sim.events`（按交易对，默认关）打开后，运营可以经管理接口建事件（立即或按 `starts_at` 定时），也可以提前结束：

| 类型 | 参数 | 效果 |
|---|---|---|
| `JUMP` | `size`（0.1 即 +10%，大于 −0.9），`duration_seconds`（0 立即，最长 600） | 事件因子 `E` 在这段时间内（对数线性）乘上 `1+size`，之后保持 |
| `TARGET` | `price`，`duration_seconds`，`hold_seconds` | 目标价沿指数路径走到 `price`，再固定 `hold_seconds`（0 不固定）；结束后模型从该价继续 |
| `TREND` | `mu`（每天），`duration_seconds`（0 直到结束） | 期间漂移改为 `mu` |
| `VOLATILITY` | `factor`，`duration_seconds`（0 直到结束） | 期间波动率乘 `factor` |
| `PAUSE` | `duration_seconds`（0 直到结束） | 目标价冻结；做市商照常报价，噪声与趋势交易者停止 |
| `HALT` | — | 撤掉做市商全部挂单，交易对置 `HALT`（经 instrument-service gRPC）；结束事件即恢复 `TRADING`，机器人重新报价 |
| `REANCHOR` | — | `P0` 设为当前目标价，`BTC_0`、`ETH_0` 重新锚定，偏离与事件因子清零；新的 `P0` 写回设置（版本加一，审计 `market.sim.params_changed`，操作人为事件创建人），重启后从它继续 |

- 同一时间只运行一个移动或固定价格的事件（`JUMP`、`TARGET`、`PAUSE`），后到的排队等前一个结束。事件移动价格时分钟守卫让路，并从事件价格重新开始计算，事件结束后照常限速。
- 单人限额：单次移动（跳涨跳跌、目标价与当前价的距离）不超过 30%，一小时内（含这次，已取消的不算）合计不超过 50%；超过时需要另一名运营批准（`approved_by` 填另一个人，不能是自己），否则 403 `SIM_EVENT_NEEDS_APPROVAL`（`details.move` 是这次的幅度）。双人审批的流程在后台做。
- 事件移动价格时（以及之后一分钟）、报价沿价格带走价时（下一节），事件执行者每 1–2 秒比较最近成交价与报价中心，差距超过半个价差就按方向下市价单（差得越远单子越大，最多 20 档），让成交价跟上。
- 建事件、提前结束、改设置都写审计（`audit.events`：`market.sim.event_created`、`market.sim.event_canceled`、`market.sim.event_ended`、`market.sim.params_changed`，带参数与当时的目标价）。

## 价格带不锁死市场（设计 §4）

ASTRA-USDT 的价格带是 ±10%，锚点是交易服务看到的最近成交（[trading.md](trading.md)）。目标价离锚点超过带宽时，围绕目标价的报价会被逐档拒绝（`ORDER_PRICE_OUT_OF_BAND`），簿空、没有成交、锚点不再移动——市场被锁死。用户 2026-10-02 决定用三层避免：

1. **走价**：market-sim 每秒读最近一笔成交（`/v1/market/{symbol}/trades?limit=1`），按交易服务的规则得出锚点（5 分钟内的成交，否则新鲜的参考价，否则那笔旧成交），保留最近 3 秒的读数（交易服务自己缓存 1 秒，取读数的最低与最高两头都放得下的范围）。做市商的报价中心是被拉进带内 70% 处的目标价（`domain.QuoteCenter`）：目标价在带外时，报价停在靠目标一侧的带边（`walking`，指标 `market_sim_walking`），事件执行者按报价中心下市价单，成交带动锚点，报价再往前走一步，直到目标价回到带内；梯子里超出带宽的档位不发（`domain.InBand`）。"瞬时涨跌"等事件也这样分步走完，不豁免价格带。
2. **重锚**：market-sim 每 5 秒把目标价（按 tick）上报给 market-data-service（`PUT /internal/market/ASTRA-USDT/simulated-price`），盘口没有中间价时它就是该交易对的参考价；交易对 5 分钟没有成交时，交易服务的锚点改为盘口中间价或这个目标价（[market-data.md](market-data.md)）。
3. **看门狗**：机器人在交易、没有 `PAUSE` 或 `HALT` 事件时，3 分钟零成交，或做市商下的档位 3 分钟里全部被价格带拒绝：结束正在移动价格的 `JUMP`、`TARGET`（`ended_by` 为 `system:watchdog`，审计 `market.sim.event_ended`），模型从锚点继续（`Model.Rebase`：经事件因子，分钟守卫从这里重新计），计数 `market_sim_band_deadlocks_total`，告警 `MarketSimBandDeadlock`；`GET /internal/sim` 的 `watchdog` 记次数与最近一次。

节流与退避（同一节的要求）：被平台拒绝的下单不消耗令牌（退回）；被拒的机器人按 1、2、4……最长 60 秒退避后再下单（`retry_at`）；做市商一侧资金不够只等下次重报，机器人不退避；做市商只在令牌桶还能留下 25% 速率时取令牌，吃单、趋势与事件执行者不会被重报饿死。

永续的报价中心是它的标记价（设计 §4），梯子限制在合约的价格带（5%，围绕标记价）内；标记价跟随指数，也就是平台现货，现货走价时永续跟着走。

端到端 `scripts/e2e/astra.sh`（`sim.events` 打开时）：经第二名运营批准把目标价一次推高 15%，无人干预，3 分钟内最近成交走到目标价附近且两侧各 8 档以上，再推回原价，看门狗不介入。

## 机器人（设计 §4）

| 角色 | 测试服数量 | 行为 |
|---|---|---|
| MAKER 做市商 | 6 | 每侧 `levels`（8）档，最优买卖相距 `spread`（0.2%），档距 `level_ticks`（5 个 tick）；价格放在按做市商错开相位的网格上，目标价小幅移动时大部分挂单不动。目标价偏离上次报价 `requote_ticks`（3 个 tick）或每 1–3 秒（随机）重报一次：撤掉不再需要的价位、补上缺的价位（由近到远、买卖交替）；每轮最多 2 个做市商重报。每档价值对数正态，中位 `level_size`（800 USDT） |
| TAKER 噪声交易者 | 12 | 泊松到达，平均每天 `daily_volume / order_size` 单（默认 2,000,000 / 400 = 5,000 单），按 UTC 小时加权（欧美重叠时段最多）；方向五五开，按 `mu` 最多偏 10 个百分点，按自己的 USDT 偏离 `bot_usdt` 最多偏 20 个百分点；市价单，价值对数正态，中位 `order_size` |
| TREND 趋势交易者 | 4 | 每半分钟左右看目标价最近 `trend_minutes`（15）分钟的方向，各以 `trend_strength`（0.3）的概率顺势下市价单 |
| EXECUTOR 事件执行者 | 2 | 只在事件移动价格时（及之后一分钟）下市价单，让成交价跟上目标价（见「价格事件」） |

- 节流：全部机器人合计每秒 `orders_per_second`（20）单、`cancels_per_second`（10）次撤单（令牌桶），超出的这一轮放弃（`market_sim_throttled_total`）。
- 余额：每 10 分钟（以及开始交易时）读一遍每个机器人的 SPOT 余额（`market_sim_inventory{asset}` 是合计）；余额不够的单子被账本拒绝时计为 `unfunded`，下一轮再说。整体不够时用 `astra.sh mint` 增发。
- 机器人全部零手续费：它们的用户 ID 在 `MARKET_MAKER_USER_IDS`（spot-trading-service、derivatives-service 读，`astra.sh seed` 写入 `apps.env` 并重启这两个服务）。

## 永续 ASTRA-USDT-PERP（设计 §5.2，A4）

- 规格在 `deploy/instruments/test.json`（`PREPARE`；风险阶梯按 125 倍表、名义上限缩小 10 倍：5,000 USDT 以内 125 倍……10,000,000 以内 2 倍）。指数价来自平台自己的 ASTRA-USDT（引擎盘口中间价，见 [market-data.md](market-data.md)），标记价与资金费沿用通用规则。HOUSE 不为它报价，它的订单互相成交（[derivatives.md](derivatives.md)）。
- 开关 `sim.perp`（按合约，默认关）打开、且合约 `TRADING` 时：做市商在永续上同样以目标价为中心挂梯子（每轮一个做市商重报），噪声交易者按 `perp_daily_volume`（默认每天 1,000,000 USDT）的泊松流下市价单；每个机器人的仓位价值超过 `perp_bot_cap`（默认 20,000 USDT）后只做减仓方向（做市商撤掉加仓一侧，噪声交易者下只减仓的单）。机器人之间的多空在池内相互抵消。
- 保证金：每分钟读一次每个机器人的仓位与 FUTURES 可用余额，余额低于 `perp_margin`（默认 30,000）的一半时从它的现货 USDT 划转补足（`POST /v1/account/transfers`，幂等键按分钟）。
- 关掉 `sim.perp`、关掉 `sim.enabled` 或 `HALT` 事件时撤掉做市商在永续上的挂单。`GET /internal/sim` 有 `perp`、`perp_running`，每个机器人有 `perp_position`、`futures_usdt`。
- 启用：`scripts/ops/astra.sh perp-open`（合约置 `TRADING`）、`astra.sh perp-on`。

## 设置

设置存在 `marketsim.settings`（一行 JSON，`version` 每次加一），首次启动写入默认值；改设置走管理接口（后台的 A3 页面也调它），下一轮生效。字段（数值，均为模型参数，不是账务金额）：

`p0`、`w_btc`、`w_eth`、`beta`、`theta`、`sigma`、`mu`、`max_minute_move`、`floor`、`ceiling`、`levels`、`spread`、`level_ticks`、`level_size`、`requote_ticks`、`daily_volume`、`order_size`、`trend_minutes`、`trend_strength`、`orders_per_second`、`cancels_per_second`、`bot_usdt`、`perp_daily_volume`、`perp_bot_cap`、`perp_margin`；含义与默认值见 `internal/marketsim/domain/params.go`，`Validate` 给出范围。

环境变量（compose 的 market-sim 段）：`TRADING_SERVICE_URL`、`LEDGER_SERVICE_URL`、`MARKET_DATA_SERVICE_URL`、`INSTRUMENT_SERVICE_URL`、`INSTRUMENT_GRPC_ADDR`、`DERIVATIVES_SERVICE_URL`；`SIM_SYMBOL`（默认 ASTRA-USDT）、`SIM_QUOTE`（默认 USDT）、`SIM_PERP_SYMBOL`（默认 ASTRA-USDT-PERP，空为不做永续）、`SIM_SEED`（默认 0，取时钟）。

## 管理接口（内网，`market-sim:8098`，网关不转发）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/internal/sim` | 状态：`symbol`、`enabled`、`running`、`target_price`、`last_price`（十进制字符串，可为 null）、`references_fresh`、`params`、`version`、`guards`（按守卫计数）、`bots`（`user_id`、`role`、`label`、`enabled`、`balances_known`、`usdt`、`coin`、`perp_position`、`futures_usdt`、`error`、`error_at`、`retry_at`（退避到何时，null 为不在退避））、价格带（`anchor_price` 锚点、`price_band`、`quote_center` 报价中心、`walking`、`band_distance` 目标价离锚点几个带宽（±1 以内在带内）、`last_trade_at`、`watchdog` 的 `fired` 与 `last_at`）、`perp`、`perp_running`、`at` |
| PUT | `/internal/sim/params` | `{"params": {...全部字段...}, "actor": "操作人"}` → `{"version": n}`；不合法返回 400 |
| POST | `/internal/sim/bots` | `{"user_id", "role": "MAKER|TAKER|TREND|EXECUTOR", "label"}` → 204；同一用户再登记无变化，标签被别的用户占用返回 409；下一轮开始交易 |
| GET | `/internal/sim/events` | 进行中与排队的事件；`?all=1&limit=50` 取最近的全部状态。每条：`id`、`type`、`size`、`price`、`mu`、`factor`、`duration_seconds`、`hold_seconds`、`starts_at`、`status`（`SCHEDULED`、`RUNNING`、`DONE`、`CANCELED`）、`created_by`、`approved_by`、`reason`、`created_at`、`started_at`、`ended_at`、`from_price`、`ended_by` |
| POST | `/internal/sim/events` | `{"type", "size", "price", "mu", "factor", "duration_seconds", "hold_seconds", "starts_at"（RFC 3339，可省）, "actor", "approved_by", "reason"}` → 201 事件；`sim.events` 关时 403 `SIM_EVENTS_OFF`，超过单人限额 403 `SIM_EVENT_NEEDS_APPROVAL`，参数不合法 400 |
| POST | `/internal/sim/events/{id}/end` | `{"actor", "reason"}` → 200 事件：排队的取消，进行中的就地结束（`HALT` 恢复交易）；已结束的 409 |
| GET | `/internal/sim/history` | `?minutes=`（默认与最长一天）：每 10 秒一个点 `{at, target_price, last_price}`（内存里，重启后从头积累） |
| GET | `/internal/sim/stream` | 同样的点每秒一个，server-sent events（`data: {...}`） |

`GET /internal/sim` 另有 `events`（进行中与排队的事件）。

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

测试服 2026-10-02 已按上面三步启用：24 个机器人、ASTRA-USDT `TRADING`、`sim.enabled` 打开；目标价约 1 USDT，盘口每侧 20 档以上、价差约 0.2%。随后打开价格事件与永续：

```bash
scripts/ops/astra.sh events-on   # sim.events
scripts/ops/astra.sh perp-open   # ASTRA-USDT-PERP 置 TRADING
scripts/ops/astra.sh perp-on     # sim.perp
```

永续盘口每侧 10 档以上、以标记价为中心、价差约 0.2%，指数来自平台现货（`source` 为 `platform`）、未降级；机器人的 FUTURES 保证金补到约 30,000 USDT。端到端 `scripts/e2e/astra.sh` 检查盘口、成交、K 线、用户的一买一卖与限价挂撤，以及（开关打开时）事件的单人限额、跳涨与回调、价格带走价（15% 上去再回来，看门狗不介入）和永续开平仓；交易对不在交易或机器人没开时跳过。

- `astra.sh off` 关掉机器人（撤掉做市商挂单）；`astra.sh mint 50000000` 给机器人再增发 5000 万 ASTRA（平均分，`mint 100000 USDT` 增发 USDT），都是带审计的账本调整，需要开关 `ledger.manual_adjustment`。
- 重新跑 `seed` 只注册还没有的机器人；调整用固定的幂等键，不会重复入账。

## 指标与告警

`market_sim_target_price`、`market_sim_last_price`、`market_sim_running`、`market_sim_references_fresh`、`market_sim_walking`、`market_sim_inventory{asset}`、`market_sim_orders_total{role,result}`（placed、unfunded、out_of_band、failed；永续的角色带 `PERP_` 前缀）、`market_sim_cancels_total{role}`、`market_sim_guards_total{guard}`、`market_sim_throttled_total{kind}`、`market_sim_errors_total{op}`、`market_sim_band_deadlocks_total`。

告警：`MarketSimFailing`（10 分钟失败超过 100 次）、`MarketSimReferencesStale`（运行中 5 分钟没有新鲜的 BTC/ETH 参考价）、`MarketSimBandDeadlock`（15 分钟内看门狗动过手：查 `GET /internal/sim` 的 `anchor_price`、`band_distance`、机器人的 `error` 与 `retry_at`，以及日志 `the market was locked`）。

## 还没做（后续批次）

- A3 的后台页面（概览、价格控制、事件日程、机器人集群，后台会话负责）、确认框里的强平影响估算（设计 §6.3：按目标价估算会被强平的仓位与保险基金承担额；后台已能读全部仓位，由 admin-service 算）、事件主题 `market.sim.events` 进 ClickHouse（概览先用 `/history` 与 `/stream`）。
- A4：永续端到端里的资金费结算与事件触发的强平、ADL（`astra.sh` 已有开仓与平仓）；`HALT` 事件目前只停现货交易对，永续靠指数（平台现货）断档进入降级只减仓。
- A5：market-sim 心跳中断 60 秒自动停牌、故障注入、ADR-0016。
- 后台按 `bot` 标记过滤机器人的订单与成交：用户标签在 admin 的库里，需要后台会话提供写入方式。
