# HOUSE 虚拟流动性运维

设计稿 §8（ADR-0013、ADR-0014、ADR-0015）；实现见 `internal/marketmaker`（market-maker 服务，运维端口 9091）、`internal/matching/domain/reference.go`（引擎里的参考簿）与 `internal/ledger`（`HOUSE_TRADE_SETTLE`）。参考行情本身（币安的 ticker、K 线与盘口）见 [market-data.md](market-data.md)。

阶段 2 的"做市机器人"（需求 §11.10，逐档挂真实订单）已随 ADR-0015 退役：market-maker 不再下单，只把参考市场的盘口作为 **参考簿** 发给撮合引擎，由引擎在撮合时让 HOUSE 当对手方。

**用户决定（2026-10-02）：所有交易一律和 HOUSE 做对手**，现货与合约、开多开空都是；用户之间不撮合（`market.internal_matching` 保持关闭）。所以测试服上每个交易对（以 USDT 与 BTC 计价）和每个合约都由 HOUSE 报价，HOUSE 的上限与资金也按"接下全部订单"放大（见下文"配置"与"测试服设置"）。

## 它做什么

- market-maker 从 `market.depth`、`derivatives.market.depth` 的末尾读公共盘口（只认标了 `reference` 的消息，也就是 market-data-service 转发的币安盘口；平台自己的盘口表示该交易对不显示参考市场，HOUSE 在那里不提供流动性）。
- 每 250 毫秒一轮，对每个现货交易对与合约算出 HOUSE 愿意成交的档位与剩余额度，发 `ReferenceBookUpdate` 到 `order.references`（合约 `derivatives.order.references`）；内容没变时 2 秒发一次心跳。HOUSE 在某个簿上有成交（market-maker 从末尾读 `trade.events`、`derivatives.trade.events` 里带 `house_side` 的成交）后，该簿下一轮照发、不等心跳：引擎把成交吃掉的档位从自己的副本里减掉，要等下一次更新才补回（审查 FE，C44）。
  - 档位：参考盘口最好的 20 档，价格放到本交易对的价格步长上（买价向下取、卖价向上取，HOUSE 永远不比参考市场给得多），落在同一格的合并，每档最多值 `HOUSE_LEVEL_CAP`（代码默认 20,000 USDT；以 BTC 计价的交易对按 BTC 的 USDT 价格折成 BTC；还没有该价格时不报价），数量取整到数量步长。一笔订单最多吃到当时发布的这些档：2026-10-07 用户在 BTC-USDT-PERP 市价平 5 BTC 只成交 1.567——20 档各取 min(币安该档数量, 20,000 USDT ≈ 0.23 BTC)，合计只有 1.567 BTC，其余撤单（IOC），之后几次也一样；测试服因此把单档上限放到 500,000,000（等于不限，见下）。
  - 20 档之外（审查 FI，C46）：只放上限还不够——币安前 20 档本身有时很薄（2026-10-07 06:10 实测 BTCUSDT 现货与永续卖方前 20 档都只有约 2.6 BTC），5 BTC 的市价单照样只成交一半。所以盘口其余部分（market-data 发的前 200 档）再合并成至多 6 档跟在后面：依次合并参考市场的 2、4、8、16、32 档，最后一档收下剩余全部，每档价格取所合并各档里最差的（买方最低、卖方最高，再放到价格步长上），数量为合计，单档上限照样适用。大单因此像在币安上一样往深处走，只是在合并档内按该档最差价成交（略差于逐档）；20 档以内的成交价不变。价格事件（通用价格控制，见 [market-data.md](market-data.md) 价格叠加一节）期间参考簿带 `overlay_factor`，HOUSE 每档只报 `OVERLAY_QUOTE_FRACTION`（默认 0.25，0 到 1 之间）、取整到数量步长，指标 `market_house_overlay_quoting{symbol}` 为 1；额度规则不变。`market_house_quoted_share{symbol}` 是 HOUSE 整个簿两侧报出的数量与参考盘口同一价格范围内（买方到 HOUSE 的最低买价、卖方到最高卖价；HOUSE 的价格格子可能更粗、合并档更深）的数量之比：每档上限不起作用时约为 1，价格事件期间约为 0.25（端到端 `price-event.sh` 比较事件前后的这个数，审查 C57 ④；按前 20 档比较时粗格子的簿读成 5.08，C58 改为按价格范围）。合并档只取离最好价 1% 以内的档位（`domain.DeepWithin`，审查 FT C50：冷门盘口的第 200 档可能很远，合并档按最差价成交会把它给到整档）。配置在代码里（`application.Config` 的 `Levels` 20、`Deep` 6）；每簿每侧至多 26 档，消息变大约三成，引擎与 WAL 的写入随之变化。基线（2026-10-07 06:5x，109 个合约上架后、196 个簿在报价）：每秒约 300 次发布（`market_house_updates_total`），market-maker CPU 约 40%（单核 100%），引擎 3–8%；改动后与之比较。引擎 WAL 的小时增量（2026-10-07 14:3x 实测最近一小时，审查 C56）：合约引擎参考簿 664,849 条、730 MB（平均每条 1,149 字节；命令 7,118 条、3 MB），现货引擎参考簿 338,234 条、368 MB（命令 3,806 条）；合约引擎原保留 2 小时，`deriv_matching.wal` 涨到 2.5 GB，改为与现货一样保留 30 分钟（[matching.md](matching.md)），清理后用一次 `VACUUM FULL` 把空间还给磁盘。磁盘告警 `HostDiskLow`（根分区可用 < 10 GB）、`HostDiskCritical`（< 5 GB）要 node exporter（`prometheus.yml` 的 `node` 任务），部署脚本在 < 8 GB 时停止。
  - 现货额度（ADR-0013）：
    - 可充提资产（instrument-service 里有网络的资产，现在是 USDT、BTC、ETH；每 30 秒从 `/v1/market/assets` 读，与账本同一个定义，读到之前一律当作可充提）要有库存才能卖，且保留 1,000 USDT 的价值不动（`HOUSE_SAFETY`）；买入花的 USDT 同样保留 1,000；安全线以上的部分平均分给 HOUSE 正在报价、会花掉它的簿（卖出花基础币、买入花计价币；USDT 由约 87 个簿分），两次读取持仓之间各簿合计不会花超（审查 M1，ADR-0015）。所以额度放到 500,000,000 以后，现货一笔能成交多少由库存决定：5,000,000 USDT 分给 87 个簿时，用户一笔最多卖出 0.67 BTC（审查 FI，C46）。测试服 `house.sh seed` 的 v4 让每个簿至少有 10 BTC 的等值（USDT 100,000,000 分 87 个簿、BTC 31.74 分 BTC-USDT/ETH-BTC/LINK-BTC 3 个、ETH 670.4（v1 7.4、v3 363、v4 300 累计注入，成交后余 669.6）分 2 个，按 BTC 85,600、ETH 2,700）；改成各簿共用一个池（不均分）留待以后；
    - 内部资产（其余 47 个币）没有库存也能卖（HOUSE 在 `MARKET_MAKER` 科目上记负数，ADR-0013）；
    - 一个资产的净头寸（多或空，**库存本身也算多头**）最多值 `HOUSE_SYMBOL_CAP`，全部现货头寸合计最多 `HOUSE_TOTAL_CAP`；持有某资产超过前者后，HOUSE 在所有交易对上都不再买入它（2026-10-02 曾因一次补库存超限导致 BTC、ETH 无人接盘，已撤回）。
    - 额度一律按 USDT 计价：以 BTC 计价的交易对（ETH-BTC）把两边资产各按自己的 USDT 价格折算（`internal/marketmaker/domain/house.go` 的 `SpotRooms`），USDT 本身恒为 1、不计入头寸。资产的 USDT 价格取它的 USDT 交易对的参考盘口中间价，不管那个交易对是否在交易：BTC-USDT 暂停时 HOUSE 撤出 BTC-USDT，但 BTC 仍有价格，ETH-BTC 照常报价（6fa3b68 审查；规格读取因此带上不在交易的交易对，标为 `Halted`，只计价、不报价）。读规格失败时 5 秒后再试，不是每一轮都试。
  - 合约额度：HOUSE 在该合约的净仓位多空各最多值 `HOUSE_CONTRACT_CAP`；到上限后该方向不再报价（`market_house_room` 为 0），用户只能做反方向，直到有人平仓或调高上限。HOUSE 在合约上是一个普通用户账户（`HOUSE_USER_ID`），仓位与保证金在它的 FUTURES 账户里，**自己永不被强平**（用户穿仓后的自动减仓里，HOUSE 与其他盈利仓位一样可以是对手方，ADR-0015）。正因为不被强平，HOUSE 的全部合约仓位按标记价合计最多值它合约权益（FUTURES 钱包余额加未实现盈亏，即 `margin_balance`）的 `HOUSE_CONTRACT_LEVERAGE` 倍（默认 10）：超过后各合约只报减仓方向，直到亏损收回或补了保证金（审查 A4）。剩下能增长的额度按 HOUSE 当前在报价的合约个数均分给各合约（每秒读一次仓位，两次之间所有合约合计不会用超）；与 HOUSE 同侧的用户在这时被强平会没有对手方，转 ADL（见 [derivatives.md](derivatives.md) 强平一节）。
- 发空簿（撤走 HOUSE 的流动性）的情况：开关 `market.house_liquidity` 不允许该交易对、`market.reference_feed` 关闭、参考盘口 3 秒没有消息或漏了增量（等下一个快照）、HOUSE 的余额与仓位 10 秒没读到；交易对或合约离开 `TRADING`（暂停、只撤单、下架）：market-maker 跟着 `instrument.events` 的状态变更，收到状态变更的下一轮就发空簿并把它移出列表（测试服实测：改状态的命令返回后 276 毫秒 HOUSE 撤出，恢复 `TRADING` 后 258 毫秒重新报价，含 outbox 转发的 200 毫秒），不等 30 秒一次的规格读取（需求 §761 暂停时撤报价；用户的挂单按 §630 保留，只是没有 HOUSE 可成交，2026-10-03 起），重新 `TRADING` 时立即重读规格。引擎自己也会丢弃比订单早 5 秒以上的参考簿（`RefMaxAge`，时间都在命令里，重放结果一致）。
- 产品线开关（产品线开关设计 2026-10-07，K1b）不撤 HOUSE 的报价：所有成交都对 HOUSE，关闭的产品线上订单服务只收平仓单与强平单，HOUSE 的报价只会被它们用到；停报价会让持仓用户平不了仓、强平单无单可成，所以发布者不按产品线过滤（设计稿 §1 #3，协调会话 16:48 更正）。
- 库存每秒从账本 gRPC（`MARKET_MAKER` 各资产的可用余额）与 derivatives-service 内网接口（HOUSE 账户的仓位与合约账户）读一次；交易对与合约规格每 30 秒从 instrument-service 读一次。

## 币本位合约（币本位设计 2026-10-06 §2.3，批次 G1）

- 报价的合约读 instrument-service 的全部合约（`/v1/market/contracts?margin_type=ALL`），`TRADING` 且有 `reference_symbol` 的才报（平台币的两个永续没有）。币本位合约的盘口是币安 COIN-M 的（数量是张），HOUSE 按张报价：每档最多值 20,000 美元（BTC 200 张、ETH 2,000 张），`HOUSE_CONTRACT_CAP` 按美元名义（张 × 面值）折成张。
- 额度按结算资产分开：每个币本位账户（HOUSE 的 BTC、ETH FUTURES）的仓位合计（美元）最多是该账户权益（币，按该币的 USDT 交易对参考盘口中间价折成美元）的 `HOUSE_CONTRACT_LEVERAGE` 倍，超出时该方向不再报价；同一结算资产的合约平分剩余额度。没有该币价格时没有额度。USDT 账户与 U 本位合约照旧。
- HOUSE 的 BTC、ETH 是可充提资产，不能为负：亏损超过账户余额时账本由同一资产的保险基金补，基金不够时整笔拒绝。测试服由 `scripts/ops/house.sh seed` 注资（HOUSE 币本位保证金 BTC 6、ETH 200，约各 50 万美元；保险基金 BTC +2、ETH +40、ASTRA 100,000），`exchangectl ledger house-margin --asset BTC --amount …` 可补。合约按币安列表扩充后（§3.4，批次 G1c）同一命令按上架的、有 `reference_symbol` 的合约逐个补：每个合约一次 50 万美元的保证金（`HOUSE_CONTRACT_CAP` 的 10%；U 本位给 USDT，币本位按当时标记价折成该币），币本位另给该币的保险基金 10 万美元一次；键按合约与币（`seed-house-margin-<合约>-v1`、`seed-insurance-coinm-<币>-v1`），重跑不重复。没有标记价的币本位合约跳过、下次再补。上架后还要重跑 `house.sh flags`，HOUSE 才报新合约。

## 引擎怎么用参考簿

详见 [matching.md](matching.md#house-的参考簿adr-0015)。要点：

- 交易服务下单时决定订单是否 `house_only`：交易对跟随参考市场、`market.house_liquidity` 打开、`market.internal_matching` 关闭时为真，此时订单**只和 HOUSE 成交**（用户之间不互相成交），用户挂的限价单在参考价穿过时按其限价成交（HOUSE 当吃单方）。
- `market.internal_matching` 打开时用户订单先与其他用户的挂单成交（同价时用户挂单优先），再与 HOUSE 成交。测试服保持关闭。
- 现货成交里 HOUSE 一侧记在系统科目 `MARKET_MAKER`（分录 `HOUSE_TRADE_SETTLE`，HOUSE 不付手续费），用户一侧照常 `TRADE_SETTLE`/`TRADE_FEE`；合约成交里 HOUSE 一侧是 `HOUSE_USER_ID` 的普通合约结算。

## 指标与告警

| 指标 | 含义 |
|---|---|
| `market_house_active{symbol}` | 1 表示 HOUSE 在该交易对/合约上提供流动性，0 表示发了空簿 |
| `market_house_inventory{asset,backed}` | `MARKET_MAKER` 各资产可用余额（内部资产卖出后为负） |
| `market_house_exposure_usdt{symbol}` | 该交易对基础资产或合约仓位的 USDT 价值（空头为负） |
| `market_house_room{symbol,side}` | 最近一次发出的可买、可卖数量（基础资产） |
| `market_house_updates_total{kind}` | 发出的参考簿（`levels`/`empty`） |
| `market_house_publish_failures_total` | 发布失败的轮次（下一轮重发） |
| `market_house_contract_equity_usdt` | HOUSE 的 USDT 合约权益（USDT 的 FUTURES `margin_balance`，按标记价）；第一次读到之前是 NaN（不触发告警） |
| `market_house_contract_exposure_usdt` | HOUSE 全部 U 本位合约仓位按标记价的合计价值 |
| `market_house_coin_contract_equity{asset}` | HOUSE 币本位账户的权益（该币的 FUTURES `margin_balance`），以币计 |
| `market_house_coin_contract_equity_usdt{asset}` | 同上按该币 USDT 交易对的参考中间价折成美元（没有价格时没有这条序列）；不再有该币结算的合约时这几条序列删除 |
| `market_house_coin_contract_exposure_usdt{asset}` | HOUSE 在该币结算的合约仓位的合计美元价值（张数 × 面值） |
| `market_house_contract_max_leverage` | 配置的 `HOUSE_CONTRACT_LEVERAGE` |

告警（`deploy/observability/alerts.yml`）：`HouseInventoryNegative`（可充提资产库存为负，critical）、`HouseRoomExhausted`（某方向额度 10 分钟为 0：补库存或调上限）、`HouseContractOverLeveraged`（合约仓位合计超过权益的 `HOUSE_CONTRACT_LEVERAGE` 倍 5 分钟：只能减仓，用 `exchangectl ledger house-margin` 补保证金）、`HouseContractEquityGone`（合约权益不大于 0，critical：之后的亏损由保险基金承担）；币本位账户按币各有一对：`HouseCoinContractOverLeveraged{asset}`（该币结算的仓位美元价值超过该账户美元权益的 `HOUSE_CONTRACT_LEVERAGE` 倍 5 分钟，用 `house-margin --asset` 补）、`HouseCoinContractEquityGone{asset}`（该币账户权益不大于 0，critical：亏损由该币的保险基金补，基金不够时结算被拒）、`HouseCoinContractUnpriced{asset}`（有该币结算的仓位、该币却没有参考价 10 分钟：HOUSE 在那里不再报价，杠杆也没人看着）、`HousePublishFailing`、`ReferenceBookStale`（参考盘口不同步或 30 秒没变，见 [market-data.md](market-data.md)）。

日志：`house liquidity not published`、`house liquidity: HOUSE's holdings not read`、`house liquidity idle: set HOUSE_USER_ID`（没配 HOUSE 账户时服务空转）。

## 配置

`deploy/compose/docker-compose.apps.yml` 的 market-maker 段：`LEDGER_GRPC_ADDR`、`INSTRUMENT_SERVICE_URL`、`DERIVATIVES_SERVICE_URL`；上限用 `HOUSE_LEVEL_CAP`、`HOUSE_SYMBOL_CAP`、`HOUSE_TOTAL_CAP`、`HOUSE_CONTRACT_CAP`、`HOUSE_SAFETY`（USDT，代码默认值按设计稿 §8.6：20000、100000、1000000、100000、1000）与 `HOUSE_CONTRACT_LEVERAGE`（倍数，默认 10）覆盖（`HOUSE_BACKED_ASSETS` 已取消，见上）——C45 起它们只是第一次启动时存入库里的第 1 版，之后以库里的运行时额度为准（见「运行时额度」）；启动日志 `house liquidity caps` 打出生效的值。
  - **生产**（compose 不设 `HOUSE_*` 时的代码默认值，即设计稿 §8.6）：单档 20,000、单资产 100,000、现货合计 1,000,000、单合约 100,000、保留额 1,000（USDT），合约杠杆上限 10 倍。上线前按当时的库存与保证金在后台「额度」卡调整（第 1 版就是这些值）。
  - **测试服**：所有订单都由 HOUSE 接（2026-10-02），按设计默认值一个用户 125 倍开 2 BTC 就能占满 BTC-USDT-PERP 一侧。用户 2026-10-07（04:4x）决定单档、单资产/交易对、单合约、合计四项全部 500,000,000 USDT（compose 里是这四个值，保留额 1,000、杠杆 10 用默认）；这四项因此几乎不起作用：现货一笔能成交多少受 HOUSE 的库存与各簿的均分限制（见上「现货额度」），合约受它的合约保证金（`house.sh seed`）的 10 倍限制。（此前一度是单资产 2,000,000、现货合计 20,000,000、单合约 5,000,000，已被取代。）`HOUSE_USER_ID` 在服务器 `apps.env`（market-maker 与 derivatives-service 都读；测试服沿用原做市账户的用户 ID）。

## 运行时额度（用户决定 2026-10-07，C45）

- 生效的额度存在库里（schema `marketmaker`：`house_caps` 一行，`house_caps_changes` 记每次改动的新值、旧值、操作人、批准人、审批号、原因与签名的键 `signed_by`；迁移 marketmaker 00001、00002）。服务第一次启动时把环境变量 `HOUSE_*` 的值存为第 1 版，之后以库里的为准，环境变量不再起作用；每 10 秒重读一次（多实例或直接改库也会跟上），本实例改的立即生效，下一轮（250 毫秒）报价就用新额度。启动日志与每次变化打 `house liquidity caps`（带 `version`）。
- 内部接口（端口 8091，`HTTP_ADDR`；网关不转发 `/internal`，只给 admin-service，后台「HOUSE」页的「额度」卡由后台会话做（A69：双人审批 `HOUSE_CAPS`，后台审计））：
  - `GET /internal/house/caps` → `level`、`symbol`、`total`、`contract`、`safety`（USDT，十进制字符串）、`contract_leverage`（倍数）、`version`、`updated_by`、`updated_at`；
  - `PUT /internal/house/caps` `{"level"?, "symbol"?, "total"?, "contract"?, "safety"?, "contract_leverage"?, "version", "actor", "approver"?, "approval_id"?, "reason"}`：只改给出的项（在锁住库里那一行的同一个事务里套到当前值上，不是套到内存里最多 10 秒前的副本上，C47 ③），`version` 必须是读到的那一版（中间有人改过答 409 `HOUSE_CAPS_VERSION`，重读再改），`actor`、`reason` 必填、至少改一项；答新的一版。范围（C47 ②，不合答 400 `COMMON_INVALID_ARGUMENT`）：`level` ≥ 0（0 = 不限），`symbol`、`total`、`contract`、`safety` > 0，金额都不超过 10^15 USDT，`contract_leverage` 1–125；**一次改动每项最多变为原来的 10 倍或十分之一**（`level` 从 0 改成数值或改回 0 不算），超过答 400 `HOUSE_CAPS_STEP`（`details` 里有 `cap`、`from`、`to`）：多半是多打或少打了一位，要大幅调整就分几次改。环境变量 `HOUSE_*` 也要在这个范围里，否则服务不启动；
  - `GET /internal/house/caps/changes?limit=`（默认 20，最多 100）→ `items`：新的在前，每项 `version`、`caps`、`previous`（第 1 版为 null）、`actor`、`approver`、`approval_id`、`reason`、`signed_by`、`at`。
  - `GET /internal/house/rooms/{symbol}`（通用价格控制 J2，只读、不签名）→ `symbol`、`buy`、`sell`（HOUSE 按上次发布还能买、卖的数量：基础币或张）、`mid`（参考中价）、`unit_value`（一个单位值多少 USDT：现货与 U 本位为价格，币本位为面值）、`inverse`（币本位合约）、`updated_at`；HOUSE 没在报价时 404。market-sim 用它估算价格事件的最坏损失（见 [market-sim.md](market-sim.md)「任意交易对的价格事件」）；后台审批价格事件时，批准框按同一算法逐个交易对给出估算（admin-service 读它，A81，见 [admin.md](admin.md)「任意币种价格事件」）。
- **改动要签名**（审查 FL，C47，照 market-sim）：`PUT` 必须带 `X-Service-Signature`（`internal/platform/svcsign`，HMAC 覆盖时间、随机数、方法、路径与正文，5 分钟内有效、同一签名只收一次），否则 401 `SERVICE_UNSIGNED`；读不要签名。两把键，每个调用方一把：`ops`（`HOUSE_CAPS_API_SECRET`，market-maker 容器里的 `exchangectl house`）与 `admin`（`HOUSE_CAPS_ADMIN_API_SECRET`，admin-service）。签名方担保正文里的 `actor`；**只有 `admin` 键能带 `approver`/`approval_id`**，其它键带了 403 `HOUSE_CAPS_APPROVAL_NEEDS_ADMIN`。缺哪把键（或不足 32 个字符），用那把签的改动一律被拒，启动日志告警，额度照库里的生效、报价不受影响。
- 密钥：测试服在 `/opt/exchange/infra/house/caps.env`（`HOUSE_CAPS_API_SECRET`，只挂给 market-maker）与 `house/admin.env`（`HOUSE_CAPS_ADMIN_API_SECRET`，挂给 market-maker 与 admin-service），部署脚本第一次运行时生成、不打印；本机在 `.env`。
- 手工（服务器上，在 market-maker 容器里）：读 `exchangectl house caps`、`exchangectl house changes`；改 `exchangectl house call PUT /internal/house/caps '{"safety":"2000","version":<读到的版本>,"actor":"ops:<名字>","reason":"..."}'`（`ops` 键，不能带批准人）。例：`sudo docker compose -f docker-compose.yml -f docker-compose.apps.yml exec -T market-maker /app/exchangectl house caps`。本机：`scripts/ops/house.sh caps`（当前值、版本与最近改动）、`scripts/ops/house.sh caps set safety=2000 level=1000000 "原因"`（读当前版本后签名改，actor 为 `ops:<本机用户>`）。
- 指标（C47 ④）：`market_house_cap_usdt{cap="level|symbol|total|contract|safety"}`、`market_house_contract_max_leverage`、`market_house_caps_version`（0 表示还没读到库里的）；`market_house_caps_signing_key{key="ops|admin"}` 为 1 表示该键可用，0 表示启动时缺失或不足 32 个字符（告警 `HouseCapsSigningKeyMissing`，10 分钟，warning：用它签的改动都会被拒，检查服务器 `house/caps.env`、`house/admin.env`）。
- 库表也守范围（迁移 marketmaker 00003 的 `house_caps_bounds`，审查 FP C48）：直接改库越界会被拒；`Seed` 存第 1 版前同样校验。

### 各项额度的用途与改动影响（C47、A69 的说明文字）

改动保存后 250 毫秒内（下一轮报价）生效；保存时版本已被别人改过会被拒，重读后再改。测试服当前值：`level`、`symbol`、`total`、`contract` 四项 500,000,000，`safety` 1,000，`contract_leverage` 10；生产首次默认见上一节。

- **每档上限 `level`（USDT，≥ 0，0 = 不限）**：每个盘口每一档（含 20 档之外的合并档）最多报出的 USDT 等值数量；从 0 改成数值或改回 0 不受十倍步进限制。调低：大单要分多次才能成交完（2026-10-07 用户 5 BTC 市价平仓只成交 1.567 就是 20,000 的单档上限所致）；调高：行情突刺时 HOUSE 单笔吃的亏更大。
- **单资产上限 `symbol`（USDT，> 0）**：HOUSE 持有某个资产（站内资产可以是负数，即卖空）的市值上限，库存本身也算；到上限后所有交易对都停止买入（或卖出）该资产。调低：热门币很快买满，用户卖不出去；调高：库存风险集中在少数资产上。
- **总上限 `total`（USDT，> 0）**：除 USDT 以外全部现货持仓（按绝对值）合计的市值上限。调低：全站很快停止买入，所有交易对只剩一个方向；调高：整体库存风险上升。
- **单合约上限 `contract`（USDT，> 0）**：每个合约上 HOUSE 净头寸的名义价值上限（币本位按张 × 面值），到达后该方向只减仓。调低：用户开仓被拒或只成交一部分；调高：单个合约的方向性风险增大。
- **安全边际 `safety`（USDT，> 0）**：可充提资产（USDT、BTC、ETH）卖出时保留不卖的市值，买入时保留的计价资产余额。不能设为 0；越小越可能把可充提资产卖光（影响用户提现与对账余量）；调高则这些资产上可报的量减少。
- **合约杠杆上限 `contract_leverage`（倍，1 到 HOUSE 报价的合约里最高的杠杆上限）**：上限按合约规格的 `max_leverage`（第一档）取最大，改动时向 instrument-service 读一次规格，读不到答 503、不改（审查 C73；用户 2026-10-10 决定杠杆按币安各合约，BTC/ETH 150 倍）；表上另有 1000 倍的兜底约束（迁移 marketmaker 00004，原来是 125）。超过规格的值答 400 `COMMON_INVALID_ARGUMENT`，`details.max_leverage` 是当时的上限；规格之后降到现值以下时，改其他额度不受影响。HOUSE 在同一结算资产上的全部合约头寸合计 ≤ 该结算资产合约账户权益 × 倍数（HOUSE 不被强平，靠这一条限住亏损）。调低：报价量缩小，超过时只报减仓方向；调高：极端行情下 HOUSE 的合约权益可能被打穿。

## 测试服设置（一次性）

```bash
# 1. apps.env 加 HOUSE_USER_ID（沿用原做市账户），部署后生效；compose 里的上限先部署，再补资金
# 2. HOUSE 的资金（需要 ledger.manual_adjustment，按幂等键重复执行无害）：
#    现货库存 5,000,000 USDT、11.74 BTC、370.4 ETH（BTC、ETH 各约 100 万 USDT，单资产上限的一半，买卖两个方向各留约 100 万）；
#    合约保证金 2,000,000 USDT（exchangectl ledger house-margin：记到 HOUSE 现货再划转到 FUTURES）
scripts/ops/house.sh seed
# 3. 开关：公共盘口与 K 线都是币安的，HOUSE 为现在上架的、跟随参考市场的全部交易对（test.json 的与后台上架的一样）
#    及其上的合约报价；名单从线上 /v1/market/pairs、/v1/market/contracts 读（API=… 可换环境）
scripts/ops/house.sh flags
# 4. 开放交易：把 test.json 里仍是 PREPARE 的 USDT 交易对改为 TRADING
scripts/ops/house.sh open
```

`scripts/ops/house.sh show` 看 HOUSE 的 `MARKET_MAKER` 余额；合约一侧在后台「HOUSE 敞口」页的"各合约净头寸"。HOUSE 合约亏损超过 FUTURES 余额时账本会拒绝结算并挂起（`DerivativesSettlementParked` 告警），用 `exchangectl ledger house-margin --amount <USDT> --reason ... --key <新键>` 补足（见 [ledger.md](ledger.md)）。

## 验证与故障

- `scripts/e2e/house.sh`：HOUSE 在每个交易中的交易对与合约上都有报价（`market_house_active`）；`test.json` 的全部 USDT 交易对（前 50 与 2026-10-02 的扩展）都在交易、盘口是币安的（1000SHIB、1000BONK 按 1000 个计价）、新用户市价买卖 SOL-USDT 立即按盘口价与 HOUSE 成交、低于盘口的限价单挂着直到撤单、ETH-BTC（以 BTC 计价）按 HOUSE 的卖一买一成交、两个合约各自市价开仓与只减仓平仓、之后账本与合约对账通过。其余端到端脚本（`matching.sh`、`trading.sh`、`marketdata.sh`、`derivatives.sh`、`funding.sh`、`admin.sh`）也都以 HOUSE 为对手方，价格从当时的盘口推出。
- `scripts/fault/reference-outage.sh`：切断 market-data-service 的外网，几秒内 HOUSE 撤走流动性、公共盘口退回平台自己的（空）；恢复后重新显示币安盘口、HOUSE 重新提供流动性。
- `scripts/fault/matching-failover.sh`：撮合主实例被杀时发出的市价单在备实例接管后与 HOUSE 成交且只成交一次，接管前挂着的单子还在、能撤。
- 要临时停掉 HOUSE：`exchangectl flags set market.house_liquidity --off --reason "..."`（或把某个交易对从 allow 列表去掉），下一轮就发空簿；用户挂着的单子留在簿上，等开关恢复后按参考价成交。
