# 功能开关运维（ADR-0005）

高风险能力默认关闭，由 PostgreSQL `config.flags` 表控制；服务每 5 秒刷新本地副本，缺失的开关按关闭处理。管理后台 `/admin/` 的"功能开关"页可切换启用状态（OPERATOR/ADMIN，见 [admin.md](admin.md)）；规则（地区、账户状态、白名单等）用命令行工具 `exchangectl` 修改。每次修改在同一事务里写 `config.flag_changes` 历史，并经 `config.outbox` 发布 `audit.ConfigChanged` 到 `audit.events`（进入 ClickHouse `audit_logs`）。

## 已知开关

| 键 | 作用 | 阶段 |
|---|---|---|
| `auth.sms` | 手机号注册/登录验证码通道（高风险地区码保持仅邮箱） | 1 |
| `account.transfer` | 现货 ↔ 合约账户划转 | 1 |
| `ledger.manual_adjustment` | 运营给测试账户记入模拟余额（`MANUAL_ADJUSTMENT`） | 1 |
| `ledger.welcome_credit` | 新注册用户自动获得模拟演示资金，仅测试环境 | 1 |
| `wallet.withdraw` | 提现 | 2 |
| `derivatives.trading` | 永续合约交易 | 3 |
| `market.reference_kline` | 图表显示参考行情（币安）的 K 线而不是平台的（按交易对；合约用指数交易对；测试服对全部交易对打开，ETH-BTC 也跟随 ETHBTC，见 [market-data.md](market-data.md#参考-k-线marketreference_kline测试环境)） | 2 |
| `market.reference_ticker` | 最新价、24h 统计、买一卖一显示参考行情（币安）的 ticker（按交易对；合约用指数交易对；测试服打开，ADR-0010，见 [market-data.md](market-data.md#参考-tickermarketreference_ticker)） | 4 |
| `market.halt_on_feed_loss` | 币安行情中断 5 分钟时暂停跟随它的交易对，恢复 30 秒后放开（测试服打开，见 [market-data.md](market-data.md#行情中断保护markethalt_on_feed_loss)） | 4 |
| `admin.login_without_totp` | 管理后台只凭邮箱与密码登录，不要求也不校验身份验证器验证码（用户 2026-09-30 决定暂不启用验证码；测试服打开，见 [admin.md](admin.md#登录与会话)） | 4 |
| `admin.two_person_approval` | 管理后台的资金操作（调账、保险基金注资、需两人审核的提现）要另一位管理员批准；关闭（单人模式）时一位管理员在限额内直接执行，超过限额仍需两人（测试服关闭；后台「设置」可改，只有 ADMIN；见 [admin.md](admin.md#资金操作单人与双人审批)） | 4 |
| `market.reference_feed` | 接入参考行情（币安公开数据，仅测试环境）：参考价、K 线、ticker、盘口，指数价与标记价都依赖它（测试服打开，见 [market-data.md](market-data.md)） | 2 |
| `market.reference_depth` | 公共盘口与成交显示参考市场（币安）的而不是平台的（按交易对；测试服对全部跟随的交易对打开，见 [market-data.md](market-data.md#参考盘口与成交marketreference_depth阶段-4-b4)） | 4 |
| `market.house_liquidity` | HOUSE 按参考盘口当对手方（虚拟流动性，按交易对/合约；测试服允许 `deploy/instruments/test.json` 的全部交易对与合约：所有交易都与 HOUSE 成交，用户决定 2026-10-02，`scripts/ops/house.sh flags`，见 [market-maker.md](market-maker.md)） | 4 |
| `market.internal_matching` | 有 HOUSE 流动性的交易对上，用户订单也互相成交（关闭时一律只和 HOUSE 成交；测试服关闭，ADR-0015） | 4 |
| `market.maker` | 已退役（ADR-0015）：阶段 2 的挂单做市机器人，由 `market.house_liquidity` 取代；表里的行保留，不再有服务读取 | 2 |
| `sim.enabled` | 平台币的模拟市场：market-sim 的机器人在 ASTRA-USDT 上报价与交易（按交易对；关闭时撤掉做市商挂单；测试服对 ASTRA-USDT 打开，`scripts/ops/astra.sh on`，见 [market-sim.md](market-sim.md)） | 4 |
| `sim.events` | 模拟市场的运营价格事件（跳涨跳跌、目标价、趋势、波动、暂停、停牌、重新锚定；按交易对；测试服对 ASTRA-USDT 打开，`scripts/ops/astra.sh events-on`，见 [market-sim.md](market-sim.md#价格事件设计-62a3)） | 4 |
| `sim.perp` | 机器人在平台币永续 ASTRA-USDT-PERP 上做市与交易（按合约；关闭时撤掉做市商在永续上的挂单；测试服对 ASTRA-USDT-PERP 打开，`astra.sh perp-on`，见 [market-sim.md](market-sim.md#永续-astra-usdt-perp设计-52a4)） | 4 |
| `sim.halt_on_loss` | 模拟市场 1 分钟没有心跳（market-sim 宕机或卡住）时，market-data-service 把交易对与它的永续置 `HALT`，心跳恢复 30 秒后放开（ASTRA 设计 §9；测试服打开，见 [market-sim.md](market-sim.md#心跳与停牌设计-9a5)） | 4 |
| `margin.enabled` | 杠杆交易总开关：划入杠杆账户、向 HOUSE 借币、还币与杠杆账户下单；关闭时这些接口返回 `MARGIN_DISABLED`（杠杆设计 2026-10-06；E0 只登记键，margin-service 从 E1 读取） | 杠杆 E1 |
| `margin.liquidation` | 杠杆强平：风险率到强平线的账户被冻结、对 HOUSE 平仓、归还负债；关闭时只预警（杠杆设计 §4.5，演练时打开） | 杠杆 E3 |
| `margin.auto_borrow` | 杠杆下单 `side_effect=AUTO_BORROW` 时自动借入可用余额的差额（杠杆设计 §5.1） | 杠杆 E2 |
| `market.flat_minutes` | 不跟随参考市场的交易对与合约（平台币 ASTRA-USDT、ASTRA-USDT-PERP）在下一笔成交被应用时，把与上一根 1m K 线之间没有成交的分钟存成平盘 K 线并发到 `market.candle.flats`（ClickHouse `candles_1m`），图表与读模型都连续（按交易对；默认关；测试服对这两个打开，见 [market-data.md](market-data.md#规则)） | 4 |
| `risk.enforce` | 执行风控规则的动作（评分为 REVIEW 的 ACTIVE 账户置为 `RISK_REVIEW`）；关闭时只记分。测试服只对地区 `AQ` 打开（[risk.md](risk.md)） | 2 |
| `wallet.test_assets` | 隐藏测试资产（ADR-0017，TUSD）的网络、充值地址、地址簿与提现只对这些规则放行的用户开放（资格 `TEST_ASSETS`），其他人一律当作没有这个网络（404）；关闭时谁都没有。wallet-service 按用户缓存资格结果 1 分钟（改开关后最多 1 分钟生效）；user-service 答不上来时按没有资格处理，隐藏网络不可见、其他网络照常列出（审查 AQ）。测试服只对地区 `AQ` 打开：`exchangectl flags set wallet.test_assets --on --allow-regions AQ --reason "..."`，端到端用 `AQ` 注册（[custody.md](custody.md)） | 4 |

## 规则维度

`set` 只修改给出的选项，其余保持不变：`--on/--off` 是总开关；`--allow-<维度>` 与 `--deny-<维度>` 取逗号分隔的值，传空值清除该维度。维度为 `regions`（ISO 国家码）、`statuses`（账户状态）、`assets`、`symbols`、`users`（用户 ID 白名单）。判定：总开关打开且每个有约束的维度都放行才算开；调用方没给出某个有约束维度的值时按关闭处理（失败即关闭）。deny 优先于 allow。

紧急开关（需求 §5.12）用 deny 表达：全站暂停某能力 `--off`；单资产 `--deny-assets USDT`；单交易对 `--deny-symbols BTC-USDT`。

## 常用命令

本机（读仓库根目录 `.env`，直连测试服数据库）：

```bash
go run ./cmd/exchangectl flags list
go run ./cmd/exchangectl flags set account.transfer --on --reason "开放划转联调"
go run ./cmd/exchangectl flags set account.transfer --deny-regions KP,IR --reason "地区限制演示"
go run ./cmd/exchangectl flags history account.transfer
```

测试服（任一应用容器里都有 `/app/exchangectl`，环境变量来自 `apps.env`）：

```bash
ssh exchange 'sudo docker exec exchange-infra-user-service-1 /app/exchangectl flags list'
```

必须带 `--reason`，操作者记为 `cli:<系统用户名>`。Kafka 暂时不可用时修改照常生效，审计事件留在 `config.outbox`，下次运行 `exchangectl` 时补发。
