# market-sim：平台币 ASTRA 的模拟市场

设计见 [docs/设计-平台币ASTRA与模拟做市-2026-10-02.md](../设计-平台币ASTRA与模拟做市-2026-10-02.md)（下称"设计"），决策见 [ADR-0016](../adr/0016-platform-coin-simulated-market.md)（仅限学习项目，上线前门禁）。本文对应批次 A2（价格模型与机器人集群）、A3 的服务端（价格事件、守卫与审计；后台页面归后台会话）与 A4（平台币永续）。

## 做什么

market-sim（`cmd/market-sim`、`internal/marketsim`，REST 8098 只在内网、运维 9098）每 250 毫秒推进一次目标价 `P(t)`，让一组机器人账户围绕它交易 ASTRA-USDT：

- 机器人是普通用户（`scripts/ops/astra.sh seed` 照常注册），经交易服务与账本的内网接口下单（`X-User-Id`，同网关转发的身份），撮合、结算、行情都走平台自己的路径。ASTRA-USDT 没有 `reference_symbol`，所以它的 K 线、ticker、盘口、成交都来自这些成交（见 [market-data.md](market-data.md)）；HOUSE 不在这个交易对上报价。
- 机器人的成交互相抵消，整个机器人池等同平台（设计 §2 第 3 条）；用户买卖 ASTRA 的对手方就是它们。
- 单实例持数据库租约 `market-sim`，另一实例等待接替；等待中的实例管理接口一律 503 `SIM_NOT_READY`（它没有载入模拟，不能读也不能改）。状态（锚点、自身偏离、随机源）每 5 秒存一次，停止时、`REANCHOR` 时（与新的 `P0` 同一事务）、看门狗重定锚点后各再存一次，重启后接着走，做市商的挂单留在簿上，重启后按价格逐个接管，不会全部重挂。
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
| `PAUSE` | `duration_seconds`（0 直到结束） | 目标价冻结；做市商照常报价，噪声与趋势交易者停止，只留成交间隔下限的那一笔（1 分钟无成交时，在做市商围绕冻结价的报价上成交）：否则 5 分钟以上的暂停会让永续指数没有成交可用、合约进入只减仓（82ba533 审查） |
| `HALT` | — | 撤掉做市商全部挂单，交易对置 `HALT`，永续（在交易时）一起置 `HALT`（经 instrument-service gRPC）；结束事件即恢复 `TRADING`（永续若仍是 `HALT` 也恢复），机器人重新报价 |
| `REANCHOR` | — | `P0` 设为当前目标价，`BTC_0`、`ETH_0` 重新锚定，偏离与事件因子清零；新的 `P0` 写回设置（版本加一，审计 `market.sim.params_changed`，操作人为事件创建人），重启后从它继续 |

- `HALT` 事件运行期间每 10 秒检查一次交易对与永续，发现在交易（第一次停牌失败，或被人在后台恢复了）就再停：**要恢复交易请结束事件**（后台的事件页或 `exchangectl sim call`），不要在后台直接把交易对或合约改回 `TRADING`，否则 10 秒内又被停掉。交易对已停、只有永续那一下没停成时也照样补停（569a958 审查 H1）。
- 同一时间只运行一个移动或固定价格的事件（`JUMP`、`TARGET`、`PAUSE`），后到的排队等前一个结束。事件移动价格时分钟守卫让路，并从事件价格重新开始计算，事件结束后照常限速。
- 守卫（设计 §6.2，A3 审查后澄清）：一个运营单独一次最多让价格移动 30%，任何一小时内合计最多 50%，按生效时间（事件的 `starts_at`、参数改动的时刻）计，前后各一小时的都算；已取消的不算。各自的"幅度"：`JUMP` 是 `size`；`TARGET` 是目标价离当前目标价（开始后离起点）的距离；`TREND` 是 `mu` 在其时长内（最多一天，没有结束时间按一天）累计的漂移 `e^(mu×天数)−1`；`VOLATILITY` 是多出的一倍标准差 `sigma×|factor−1|×√天数`；参数改动见下文。超过时需要另一名运营批准（`approved_by`，不能是自己，只能由后台服务填，见下一条），否则 403 `SIM_EVENT_NEEDS_APPROVAL`（`details.move`）。即使批准，`JUMP` 最多 +100%（大于 −90%），`TARGET` 同样；事件最多提前 24 小时安排。
- 参数改动走同一套守卫（`PUT /internal/sim/params`）：幅度 = `p0` 的变化比例 + 新的 `floor`/`ceiling` 迫使当前目标价移动的比例 + `max_minute_move` 调高的部分；另有成交额预算：`daily_volume` 的对数变化，一小时内合计不超过 ln 2（一个人最多翻倍或减半，停掉或从零开启按两倍计）。超过需第二人，否则 403 `SIM_PARAMS_NEED_APPROVAL`（`details.move`、`details.volume`）；`max_minute_move` 硬上限 5%/分。每次改动连同批准人、幅度存在 `marketsim.param_changes` 并进审计。`REANCHOR` 写回的 `P0` 不移动价格，幅度为 0。
- 审批人身份来自调用方凭据（设计 §6.2，A3 复审后定的契约）：
  - 改动类请求（PUT、POST）必须签名（`internal/platform/svcsign`），每个调用方一把密钥：头 `X-Service-Signature: k=<键名>,t=<秒>,n=<随机数>,v1=<HMAC-SHA256(t\nn\n方法\n路径与查询\n正文)>`，5 分钟内有效，同一键名下的随机数只收一次（同一秒的两个相同请求也是两次）。未签名、键名不认识或签错 401 `SERVICE_UNSIGNED`。
  - 两把键：`ops`（`SIM_API_SECRET`，market-sim 容器里的 `exchangectl sim`）与 `admin`（`SIM_ADMIN_API_SECRET`，admin-service）。签名方担保正文里的 `actor`；**只有 `admin` 键能带 `approved_by`**，其它键带了 403 `SIM_APPROVAL_NEEDS_ADMIN`（在守卫之前拒绝，不论幅度）。
  - admin-service 的义务：`actor` 填当前会话的管理员，`approved_by` 填第二名在后台当场完成认证的管理员（不能是同一人），两者都不取自浏览器提交的字段；它们在正文里，随签名一起被覆盖，改一个字就验不过。这样单人份额以外的改动必须经过后台的双人流程，运维脚本只能做单人份额以内的事。
  - 密钥：测试服在 `/opt/exchange/infra/sim/sim.env`（`SIM_API_SECRET`，只挂给 market-sim）与 `sim/admin.env`（`SIM_ADMIN_API_SECRET`，挂给 market-sim 与 admin-service），部署脚本第一次运行时生成、不打印；本机在 `.env`。有服务器 root 的人本来就能读到两把键，守卫防的是后台里的单个管理员，不防服务器管理员。
- 事件移动价格时（以及之后一分钟）、报价沿价格带走价时（下一节），事件执行者每 1–2 秒比较最近成交价与报价中心，差距超过半个价差就按方向下市价单（差得越远单子越大，最多 20 档），让成交价跟上。
- 建事件、提前结束、改设置都写审计（`audit.events`：`market.sim.event_created`、`market.sim.event_canceled`、`market.sim.event_ended`、`market.sim.params_changed`，带参数与当时的目标价）。

## 价格带不锁死市场（设计 §4）

ASTRA-USDT 的价格带是 ±10%，锚点是交易服务看到的最近成交（[trading.md](trading.md)）。目标价离锚点超过带宽时，围绕目标价的报价会被逐档拒绝（`ORDER_PRICE_OUT_OF_BAND`），簿空、没有成交、锚点不再移动——市场被锁死。用户 2026-10-02 决定用三层避免：

1. **走价**：market-sim 每秒读最近一笔成交（`/v1/market/{symbol}/trades?limit=1`），按交易服务的规则得出锚点（5 分钟内的成交，否则新鲜的参考价，否则那笔旧成交），保留最近 3 秒的读数（交易服务自己缓存 1 秒，取读数的最低与最高两头都放得下的范围）。做市商的报价中心是被拉进带内 70% 处的目标价（`domain.QuoteCenter`）：目标价在带外时，报价停在靠目标一侧的带边（`walking`，指标 `market_sim_walking`），事件执行者按报价中心下市价单，成交带动锚点，报价再往前走一步，直到目标价回到带内；梯子里超出带宽的档位不发（`domain.InBand`）。"瞬时涨跌"等事件也这样分步走完，不豁免价格带。
2. **重锚**：market-sim 每 5 秒把目标价（按 tick）上报给 market-data-service（`PUT /internal/market/ASTRA-USDT/simulated-price`），盘口没有中间价时它就是该交易对的参考价；交易对 5 分钟没有成交时，交易服务的锚点改为盘口中间价或这个目标价（[market-data.md](market-data.md)）。
3. **看门狗**：机器人在交易、没有 `PAUSE` 或 `HALT` 事件时，3 分钟零成交（只在应当有成交时计：`daily_volume` 大于 0，或报价在走价，或有 `JUMP`、`TARGET` 在跑；没有吃单者时报价站着不动不算锁死），或做市商下的档位 3 分钟里全部被价格带拒绝：结束正在移动价格的 `JUMP`、`TARGET`（`ended_by` 为 `system:watchdog`，审计 `market.sim.event_ended`），模型从锚点继续（`Model.Rebase`：经事件因子，分钟守卫从这里重新计），计数 `market_sim_band_deadlocks_total`，告警 `MarketSimBandDeadlock`；`GET /internal/sim` 的 `watchdog` 记次数与最近一次。读不到最近成交（market-data-service 不可用）超过 10 秒时，看门狗分不清锁死与行情中断，从头计时、不动手。

节流与退避（同一节的要求）：被平台明确拒绝的下单（4xx：资金、价格带、订单条件）不消耗令牌（退回），超时、5xx 这类可能已经到了服务端的请求照样算一个令牌；失败的机器人按 1、2、4……最长 60 秒退避后再下单（`retry_at`），被价格带拒绝的最长只退 5 秒（快速走价时交易服务的锚点会先走一步，锚点对上后要尽快把梯子补回来）；做市商一侧资金不够只等下次重报，机器人不退避；做市商只在令牌桶还能留下 25% 速率时取令牌，吃单、趋势与事件执行者不会被重报饿死。

永续的报价中心是它的标记价往指数价拉一半处（标记价 = 指数价 ×（1 + 盘口溢价的 30 秒 EMA）：报价就在标记价上的话，吃单方向造成的溢价会一直保持，资金费率长期偏向一边；拉一半溢价逐步回落），梯子限制在合约的价格带（5%，围绕标记价）内；指数是平台现货的价格，现货走价时永续跟着走。

端到端 `scripts/e2e/astra.sh`（`sim.events` 打开、前后一小时没有别的移动价格的改动时）：单人把目标价一次推高 12%（超出 10% 的价格带），无人干预，3 分钟内最近成交走到目标价附近且两侧各 8 档以上，再推回原价，看门狗不介入。

## 心跳与停牌（设计 §9，A5）

market-sim 每 5 秒把目标价上报给 market-data-service（`PUT /internal/market/{symbol}/simulated-price`），不管机器人是否在交易、交易对是否在交易——这同时是它的心跳。上报在单独的协程里，报最近一轮的目标价：一轮慢了（交易服务变慢）不耽误心跳，market-data-service 慢了也不拖住机器人；但 30 秒没有新的一轮（主循环卡住）心跳就停，让交易对停牌。market-data-service 的 `SimGuard` 每 5 秒看一次：上报过的交易对（每次上报的时间存在 `market.sim_heartbeats`，所以 market-sim 先停、market-data-service 后重启也照样盯着；要彻底忘掉一个模拟市场，删掉它那一行再重启 market-data-service）1 分钟没有心跳，且开关 `sim.halt_on_loss` 打开时，先记入 `market.sim_halts`，再把交易对与以它为指数的永续置 `HALT`（原因 `simulated market silent for a minute`）；静默期间每次都把其中还在交易的再停一次，停到一半失败（instrument-service 不可用）的下一轮补完——运营想让静默的市场继续交易，就关 `sim.halt_on_loss`。心跳恢复并持续 30 秒（或开关关掉）后只把它停的恢复 `TRADING`，期间被运营改成别的状态的不动。指标 `market_sim_heartbeat_age_seconds{symbol}`、`market_sim_halted_pairs`，告警 `MarketSimHeartbeatLost`（超过 60 秒）。market-sim 停着时做市商的挂单留在簿上，噪声与趋势交易停止；重启后从保存的状态继续，交易对恢复后最多 30 秒（读交易对状态的周期）重新报价。

演练 `scripts/fault/market-sim-down.sh`：停掉 market-sim，挂单留在簿上，1 分钟后交易对与永续停牌，心跳年龄过 60 秒；再启动，30 秒后两者恢复交易、每侧 8 档以上，价格带看门狗不介入。`scripts/fault/reference-outage.sh` 另查币安断流时模拟市场的 BTC/ETH 参考价变为不新鲜（市场因子保持）而 ASTRA-USDT 照常成交，恢复后重新跟随。

## 机器人（设计 §4）

| 角色 | 测试服数量 | 行为 |
|---|---|---|
| MAKER 做市商 | 6 | 每侧 `levels`（8）档，最优买卖相距 `spread`（0.2%），档距 `level_ticks`（5 个 tick）；价格放在按做市商错开相位的网格上，目标价小幅移动时大部分挂单不动。目标价偏离上次报价 `requote_ticks`（3 个 tick）或每 1–3 秒（随机）重报一次：撤掉不再需要的价位、补上缺的价位（由近到远、买卖交替）；每轮最多 2 个做市商重报。每档价值对数正态，中位 `level_size`（800 USDT），按做市商的 USDT 相对全体做市商平均值倾斜：少一半的买单只挂一半大小、卖单一倍半（多的反过来），靠卖出把 USDT 补回来。重报前按页读自己的全部挂单（每页 100，最多 10 页，超过即出错而不是只看前几页，免得把"缺"的档位重复挂上） |
| TAKER 噪声交易者 | 12 | 泊松到达，一天合计 `daily_volume` 的 80%：平均每天 `daily_volume × 0.8 / (order_size × e^0.32)` 单（单笔价值对数正态，中位 `order_size`、对数标准差 0.8，均值是中位的 1.38 倍；默认约 2,900 单），按 UTC 小时加权（欧美重叠时段最多）；方向五五开，按 `mu` 最多偏 10 个百分点，按自己的 USDT 偏离 `bot_usdt` 最多偏 20 个百分点；市价单。成交间隔有下限：1 分钟没有任何成交（从未成交时从机器人开始交易算起）时，下一轮由一个吃单者立刻下一笔（计数 `market_sim_quiet_takes_total`），下单成功后再等 1 分钟（被限流或被拒时下一轮再试，不白等一分钟）；`PAUSE` 期间照常，`daily_volume` 为 0、停牌时不下。原因是永续的指数取平台现货，5 分钟没有成交就没有指数、合约进入只减仓；只靠泊松到达，夜里约每分钟一单、重启后的头几分钟趋势交易者还没有窗口，2026-10-02 22:39 UTC（北京时间 10-03 06:39）一次部署后 5 分钟零成交，ASTRA-USDT-PERP 因此只减仓 |
| TREND 趋势交易者 | 4 | 平均每 30 秒看目标价最近 `trend_minutes`（15）分钟的方向，各以 `trend_strength`（0.3）的概率顺势下市价单；单笔价值使趋势交易者合计一天是 `daily_volume` 的 20%（`domain.TrendWorth`）。`daily_volume` 因此是噪声与趋势交易合计的日成交额目标（不含事件与走价时事件执行者的成交） |
| EXECUTOR 事件执行者 | 2 | 只在事件移动价格时（及之后一分钟）下市价单，让成交价跟上目标价（见「价格事件」） |

- 节流：全部机器人合计每秒 `orders_per_second`（20）单、`cancels_per_second`（10）次撤单（令牌桶），超出的这一轮放弃（`market_sim_throttled_total`）。
- 余额：每 10 分钟（以及开始交易时）读一遍每个机器人的 SPOT 余额（`market_sim_inventory{asset}` 是合计）；余额不够的单子被账本拒绝时计为 `unfunded`，下一轮再说。库存靠倾斜自己回到平均：做市商按上表倾斜挂单大小，噪声交易者按自己的 USDT 偏离 `bot_usdt` 偏向买或卖；设计 §4 说的"机器人之间划转"没有做（要给账本加一种用户之间的划转，暂不值得），整体不够时用 `astra.sh mint` 增发。
- 机器人全部零手续费：它们的用户 ID 在 `MARKET_MAKER_USER_IDS`（spot-trading-service、derivatives-service 读，`astra.sh seed` 写入 `apps.env` 并重启这两个服务）。

## 永续 ASTRA-USDT-PERP（设计 §5.2，A4）

- 规格在 `deploy/instruments/test.json`（`PREPARE`；风险阶梯按 125 倍表、名义上限缩小 10 倍：5,000 USDT 以内 125 倍……10,000,000 以内 2 倍）。指数价来自平台自己的 ASTRA-USDT（引擎盘口中间价，见 [market-data.md](market-data.md)），标记价与资金费沿用通用规则。HOUSE 不为它报价，它的订单互相成交（[derivatives.md](derivatives.md)）。
- 开关 `sim.perp`（按合约，默认关）打开、且合约 `TRADING` 时：做市商在永续上同样以目标价为中心挂梯子（每轮一个做市商重报），噪声交易者按 `perp_daily_volume`（默认每天 1,000,000 USDT）的泊松流下市价单；每个机器人的仓位价值超过 `perp_bot_cap`（默认 20,000 USDT）后只做减仓方向（做市商撤掉加仓一侧，噪声交易者下只减仓的单）。机器人之间的多空在池内相互抵消。
- 保证金：每 10 秒读一次每个机器人的仓位与 FUTURES 可用余额（两次之间的做市成交最多让仓位略超 `perp_bot_cap`），余额低于 `perp_margin`（默认 30,000）的一半时从它的现货 USDT 划转补足（`POST /v1/account/transfers`，幂等键按这 10 秒）。
- 关掉 `sim.perp`、关掉 `sim.enabled` 或 `HALT` 事件时撤掉做市商在永续上的挂单。`GET /internal/sim` 有 `perp`、`perp_running`，每个机器人有 `perp_position`、`futures_usdt`。
- 启用：`scripts/ops/astra.sh perp-open`（合约置 `TRADING`）、`astra.sh perp-on`。

## 设置

设置存在 `marketsim.settings`（一行 JSON，`version` 每次加一），首次启动写入默认值；改设置走管理接口（后台的 A3 页面也调它），下一轮生效。字段（数值，均为模型参数，不是账务金额）：

`p0`、`w_btc`、`w_eth`、`beta`、`theta`、`sigma`、`mu`、`max_minute_move`、`floor`、`ceiling`、`levels`、`spread`、`level_ticks`、`level_size`、`requote_ticks`、`daily_volume`、`order_size`、`trend_minutes`、`trend_strength`、`orders_per_second`、`cancels_per_second`、`bot_usdt`、`perp_daily_volume`、`perp_bot_cap`、`perp_margin`；含义与默认值见 `internal/marketsim/domain/params.go`，`Validate` 给出范围。其中几条是硬上限，不论谁签名、有没有批准（审查 M4）：`floor` 不低于 0.0001、`ceiling` 不高于 1,000,000，`max_minute_move` 至多 5%/分，`orders_per_second`、`cancels_per_second` 各至多 100，`daily_volume`、`perp_daily_volume` 至多每天 1 亿 USDT，`order_size`、`level_size` 至多 50,000，`bot_usdt`、`perp_bot_cap`、`perp_margin` 至多 1000 万；超出答 400。库里存的设置启动时也按硬上限夹取（超出的按上限跑、日志记下改了哪些，库里那行不改）；夹取后仍不合法（例如 `floor` 不低于 `ceiling` 这类上限管不到的）则拒绝启动：compose 会一直重启它，管理接口在不就绪的实例上答 503，只能直接改库——`UPDATE marketsim.settings SET params = jsonb_set(params, '{字段}', '值'), version = version + 1`，改完重启 market-sim（569a958 审查）。

环境变量（compose 的 market-sim 段）：`TRADING_SERVICE_URL`、`LEDGER_SERVICE_URL`、`MARKET_DATA_SERVICE_URL`、`INSTRUMENT_SERVICE_URL`、`INSTRUMENT_GRPC_ADDR`、`DERIVATIVES_SERVICE_URL`；`SIM_SYMBOL`（默认 ASTRA-USDT）、`SIM_QUOTE`（默认 USDT）、`SIM_PERP_SYMBOL`（默认 ASTRA-USDT-PERP，空为不做永续）、`SIM_SEED`（默认 0，取时钟）。

## 管理接口（内网，`market-sim:8098`，网关不转发）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/internal/sim` | 状态：`symbol`、`enabled`、`running`、`target_price`、`last_price`（十进制字符串，可为 null）、`references_fresh`、`params`、`version`、`guards`（按守卫计数）、`bots`（`user_id`、`role`、`label`、`enabled`、`balances_known`、`usdt`、`coin`、`perp_position`、`futures_usdt`、`error`、`error_at`、`retry_at`（退避到何时，null 为不在退避））、价格带（`anchor_price` 锚点、`price_band`、`quote_center` 报价中心、`walking`、`band_distance` 目标价离锚点几个带宽（±1 以内在带内）、`last_trade_at`、`watchdog` 的 `fired` 与 `last_at`）、`perp`、`perp_running`、`at` |
| PUT | `/internal/sim/params` | `{"params": {...全部字段...}, "actor": "操作人", "approved_by": "批准人（需要时）"}` → `{"version": n}`；不合法 400，超过单人份额 403 `SIM_PARAMS_NEED_APPROVAL` |
| POST | `/internal/sim/bots` | `{"user_id", "role": "MAKER|TAKER|TREND|EXECUTOR", "label"}` → 204；同一用户再登记无变化，标签被别的用户占用返回 409；下一轮开始交易 |
| GET | `/internal/sim/events` | 进行中与排队的事件；`?all=1&limit=50` 取最近的全部状态。每条：`id`、`type`、`size`、`price`、`mu`、`factor`、`duration_seconds`、`hold_seconds`、`starts_at`、`status`（`SCHEDULED`、`RUNNING`、`DONE`、`CANCELED`）、`created_by`、`approved_by`、`reason`、`created_at`、`started_at`、`ended_at`、`from_price`、`ended_by` |
| POST | `/internal/sim/events` | `{"type", "size", "price", "mu", "factor", "duration_seconds", "hold_seconds", "starts_at"（RFC 3339，可省，最多 24 小时后）, "actor", "approved_by", "reason"}` → 201 事件；`sim.events` 关时 403 `SIM_EVENTS_OFF`，超过单人份额 403 `SIM_EVENT_NEEDS_APPROVAL`，参数不合法或超过硬上限 400 |
| POST | `/internal/sim/events/{id}/end` | `{"actor", "reason"}` → 200 事件：排队的取消，进行中的就地结束（`HALT` 恢复交易：只动仍是 `HALT` 的交易对与永续，恢复到一半失败时再结束一次会接着做完）；已结束的 409 |
| GET | `/internal/sim/history` | `?minutes=`（默认与最长一天）：每 10 秒一个点 `{at, target_price, last_price}`（也存在 `marketsim.samples`，保留一天，重启后接着画） |
| GET | `/internal/sim/stream` | 同样的点每秒一个，server-sent events（`data: {...}`） |

`GET /internal/sim` 另有 `events`（进行中与排队的事件）。上表的 PUT、POST 都要签名（见「价格事件」的守卫一节）；GET 不用。建事件与改设置逐个处理（从读小时预算到写入），两名运营同时提交不会都钻过单人份额。

运维与端到端经 `exchangectl sim` 调（在 market-sim 容器里，用 `ops` 键签名，所以不能填 `approved_by`；超出单人份额的改动走后台）：

```bash
# 在测试服的 /opt/exchange/infra
sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T market-sim \
  /app/exchangectl sim call POST /internal/sim/events '{"type":"JUMP","size":0.05,"actor":"ops","reason":"..."}'
```

它把 `HTTP <状态>` 打到标准错误、答复打到标准输出，状态 300 以上时以 1 退出。

后台（admin-service）按这个接口做了"模拟市场"五页（设计 §6，后台重构 C5，见 [admin.md](admin.md)「模拟市场」），用 `svcsign.Client{KeyID: "admin", Secret: SIM_ADMIN_API_SECRET}` 签名，`actor`、`approved_by` 按上面的契约由它填：超出单人份额的改动在后台存成审批，另一位有 `sim.control` 的管理员批准后才带着 `approved_by` 发过来。

端到端 `scripts/e2e/astra.sh` 只用 `ops` 键：未签名 401、带 `approved_by` 403 `SIM_APPROVAL_NEEDS_ADMIN`、单人 35% 403，然后单人份额以内移动价格（2% 来回、带外 12% 来回、合约强平 4% 来回，合计约 35%）；前后一小时内已有移动价格的事件或参数改动时跳过这几段（再跑一次要隔一小时）。批准的路径由后台的端到端覆盖（`admin.sh`：OPERATOR 申请明天开始的 35% 跳涨，ADMIN 批准，检查发起人与批准人后取消）。

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

永续盘口每侧 10 档以上、以标记价为中心、价差约 0.2%，指数来自平台现货（`source` 为 `platform`）、未降级；机器人的 FUTURES 保证金补到约 30,000 USDT。端到端 `scripts/e2e/astra.sh` 检查盘口、成交、K 线、用户的一买一卖与限价挂撤，以及（开关打开时）未签名的改动被拒、运维键带批准人被拒、事件的单人限额、跳涨与回调、价格带走价（12% 上去再回来，看门狗不介入）、永续开平仓与事件触发的强平；交易对不在交易或机器人没开时跳过，前后一小时内有别的移动价格的改动时跳过移动价格的几段。

- `astra.sh off` 关掉机器人（撤掉做市商挂单）；`astra.sh mint 50000000` 给机器人再增发 5000 万 ASTRA（平均分，`mint 100000 USDT` 增发 USDT），都是带审计的账本调整，需要开关 `ledger.manual_adjustment`。
- 重新跑 `seed` 只注册还没有的机器人；调整用固定的幂等键，不会重复入账。

## 指标与告警

`market_sim_target_price`、`market_sim_last_price`、`market_sim_running`、`market_sim_references_fresh`、`market_sim_walking`、`market_sim_inventory{asset}`、`market_sim_orders_total{role,result}`（placed、unfunded、out_of_band、failed；永续的角色带 `PERP_` 前缀）、`market_sim_cancels_total{role}`、`market_sim_guards_total{guard}`、`market_sim_throttled_total{kind}`、`market_sim_errors_total{op}`、`market_sim_band_deadlocks_total`。

告警：`MarketSimFailing`（10 分钟失败超过 100 次）、`MarketSimReferencesStale`（运行中 5 分钟没有新鲜的 BTC/ETH 参考价）、`MarketSimBandDeadlock`（15 分钟内看门狗动过手：查 `GET /internal/sim` 的 `anchor_price`、`band_distance`、机器人的 `error` 与 `retry_at`，以及日志 `the market was locked`）。

## 还没做（后续批次）

- 事件主题 `market.sim.events` 进 ClickHouse（后台概览用 `/history`）。A3 的后台页面已由后台 C5 完成，确认框里的强平影响估算用 derivatives-service 的 `/internal/derivatives/contracts/{symbol}/price-impact`。
- A4：永续端到端里的资金费结算与 ADL（`astra.sh` 已有开平仓与事件触发的强平；资金费在测试服 08:00 UTC 那轮人工核对过）。
- A5：验收记录写回设计文档（心跳停牌、两个演练与 ADR-0016 已做）。
