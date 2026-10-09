# 上线手册：一套部署、后台改配置即上线

依据：`docs/设计-后台可配置与上线开关-2026-10-04.md`（用户 2026-10-04 需求）。目标：正式上线用的是**同一套镜像与编排**，不改代码；上线前的差别分两类——**部署侧**（环境、密钥、第三方账号、参考数据，§1）和**后台侧**（品牌、文案、开关，§2）。后台「上线检查清单」页只覆盖 §2 的项，§1 由本手册负责。

## 0. 先说清楚"同一套部署"是什么

- 同一套：`deploy/compose/*`、GHCR 镜像、`deploy/server-update.sh`、迁移、nginx 配置、`exchangectl`。
- 不同的：`apps.env`/`.env` 里的值、`deploy/instruments/<环境>.json` 参考数据、Cloudflare 与域名、托管方商户、数据库里的数据。
- **数据不迁移**：正式环境从一台新服务器、空数据库开始（`server-deploy.md`「从零部署一台新服务器」）。测试服上有机器人账户、端到端账户、模拟资金与模拟地址，不能当正式数据用。测试模式下注册送的资金在正式库里根本不存在，不需要"清零"。

## 1. 部署侧（按顺序）

| 步 | 做什么 | 说明 |
|---|---|---|
| 1 | 新服务器与域名 | 按 `server-deploy.md` 从零部署；`PUBLIC_DOMAIN` 设为正式域名；Cloudflare DNS 三个主机名（PC、`m.`、`admin.`）与源站证书按 `cloudflare-nginx-tls.md`；nginx 三个 server 块的 `server_name` 改为正式域名（配置文件在仓库，按域名出一份正式版）；Cloudflare 该域名的 IPv6 Compatibility 关掉（登录记录优先 IPv4，`cloudflare-nginx-tls.md` 第一节） |
| 2 | 密钥全部重新生成 | `OTP_HMAC_KEY`、`JWT_SIGNING_KEY`/`JWT_KEY_ID`、`TOTP_SECRET_KEY`、`ADMIN_SECRET_KEY`、`SIM_API_SECRET`、`SIM_ADMIN_API_SECRET`、Postgres/Redis/ClickHouse 口令；**`CAPTCHA_BYPASS_TOKEN` 留空**（正式环境不得有绕过）。变量名清单见 `.env.example`；值只进服务器 `apps.env`/`.env` |
| 3 | 第三方账号 | Turnstile 正式站点密钥与允许主机名（`turnstile.md`）；邮件服务密钥与已验证的发件域（`MAIL_FROM`）；优盾正式商户（`UDUN_*`，见步 6）；Alchemy 只服务 Sepolia 测试网，正式环境**不配** |
| 4 | 参考数据 | 复制 `deploy/instruments/test.json` 为正式版：去掉 `ETH-SEPOLIA` 网络、去掉隐藏资产 `TUSD` 与 `UDUNMOCK` 托管方（它们只为端到端存在）、`provider_coin` 按优盾正式商户的 `support-coins` 核对（`custody.md`「直接问网关」）、费率与最小充提额按运营口径；`exchangectl instruments apply` |
| 5 | 开关（`exchangectl flags`，见 `feature-flags.md`） | `admin.login_without_totp` **关**；`wallet.test_assets` **关**（并删掉地区规则）；`admin.two_person_approval` 开；`risk.enforce` 开；`market.house_liquidity` 开；`market.flat_minutes` 对 ASTRA 现货与永续开；`sim.enabled`/`sim.events`/`sim.perp` **开**（平台币的做市与控价必须有机器人交易，用户 2026-10-09 叮嘱；机器人账户见步 12）；`ledger.welcome_credit` 可保留开（数额由后台配 0 即不发）；`wallet.withdraw` 在步 6 完成前**关** |
| 6 | 托管方切换与主网小额测试 | 按 `custody.md`「真网关联调」步骤 4、5（账本基线、退役替身地址、`apps.env` 并入 `UDUN_*`、只重启 wallet-service、核对；用户 TRC20 充值 ≥ 12 USDT、提现 10 USDT、确认手续费单位、收紧回调来源 IP）；正式环境没有 `udun-mock` 容器，compose 里去掉它 |
| 7 | HOUSE 库存与上限 | HOUSE 的 USDT/BTC/ETH 是"背书资产"，正式环境必须是托管方真实持有的资金：先向商户钱包充入库存，再 `scripts/ops/house.sh seed` 到相应水平、`exchangectl wallet reconcile` 无短缺；HOUSE 的六项额度（每档、单资产、总、单合约、安全边际、合约杠杆）存在 market-maker 的库里、运行时可改（`market-maker.md`「运行时额度」有每项的用途与改动影响；compose 的 `HOUSE_*` 只是首次默认），正式环境按真实库存与风险承受设（测试服的 500,000,000 是放大过的，只为让市价单整单成交） |
| 8 | 构建时的品牌 | `web/apps/{pc,m}/index.html` 的 `<title>` 与 `description`（供爬虫与分享卡片）在构建时写死，运行时注入只改脚本执行后的页面——上线前把这两个文件里的名称与描述改成正式值再部署（已知限制，见 §4） |
| 9 | 告警与值班 | `deploy/observability/alerts.yml` 的接收端指向正式的通知渠道（`observability.md`）；确认 `CustodyShortfall`、`WalletWithdrawalsSuspended`、`MarketSimBandDeadlock` 等有人接 |
| 10 | 杠杆参数与保险基金（杠杆设计 2026-10-06，见 §2.1） | 复制 `deploy/instruments/margin.json` 为正式版：借贷池上限按 HOUSE 真实持有的可充提资产定（USDT/BTC/ETH 是背书资产，借出去的币用户可以卖成别的币再划回现货）、利率与保证金折扣按运营口径，`exchangectl margin apply`；保险基金 `INSURANCE_FUND` 按**每种可借资产**分别注资（`exchangectl ledger insurance-fund --asset <资产>`，正式环境走后台双人划转）——基金不允许为负，某资产不够时强平停在 `SHORTFALL`、负债留着并告警；告警 `InsuranceFundShort`、`MarginLiquidationStuck`、`MarginLiquidationDue`、`MarginMonitorStalled`、`MarginInterestStalled`、`MarginReconciliationMismatch`、`TradingOrdersPendingFreeze`、`TradingOrderReleasesStuck` 要有人接（`margin.md`） |
| 11 | 币本位永续与合约数据（币本位设计 2026-10-06，见 §2.2） | 参考数据：正式版 `test.json` 只留要开盘的 U 本位/币本位永续（`deploy/instruments/gen-contracts.go` 按币安列表生成，`-offline` 按快照 `binance-contracts.json` 重放；新合约一律 `PREPARE`；ASTRA 的两个永续是平台自己的，没有币安参考）。风险阶梯与最高杠杆按币安的真实分档（B168/B171：每个合约各自的档位，最高杠杆是它第一档的，BTC/ETH U 本位 150 倍；上线前重跑一次取当时的档位，不合规则的阶梯生成时就报错），档位放大以后单个用户能开的名义价值实际由 HOUSE 的单合约上限决定（`HOUSE_CONTRACT_CAP`，测试服每方向 5,000,000 USDT）——正式环境按 HOUSE 真实能承受的金额定，并按它核对保险基金（每个结算币 > 0，测试服 USDT 约 100 万）。HOUSE 按结算币注资合约账户（`scripts/ops/house.sh seed`；BTC/ETH 是背书资产，必须真实持有、不能为负，币本位的对手方容量就是这些库存）；保险基金 `INSURANCE_FUND` 每个结算币一行且 > 0（`exchangectl ledger insurance-fund --asset <币>`，正式环境走后台双人划转；币本位穿仓由同一币的基金补，不够时整笔拒绝、不入账）。开关：`derivatives.coin_m`（币本位开仓资格）按用户或地区开，**测试服对所有人开着、正式不能照搬**；`market.reference_mark` 按合约开（标记价、指数价、资金费率跟币安，自算后备；打开那一刻标记价会跳到币安的，按合约在差距小、行情平稳时逐个开，见 `market-data.md`「标记价跟随币安」）；`market.futures_data`（合约数据板块）全局开；`market.house_liquidity` 的名单由 `house.sh flags` 取当时上架的全部跟随币安的交易对与合约。币安连接只走公开接口（现货、U 本位、币本位各一组），按主机有权重预算（现货 80%、合约 50%，90% 保护），不需要账号。开盘：`house.sh open-contracts [N]` 分批转 `TRADING`（没有标记价的合约留在 `PREPARE`），每批确认标记价、盘口与 K 线都在更新再放下一批。告警 `MarkPriceSourceDegraded`、`FuturesDataFailing`、`HouseCoinContractUnpriced`、`HouseCoinContractOverLeveraged`、`HouseCoinContractEquityGone`、`HousePublishFailing` 要有人接 |
| 12 | 平台币机器人（正式功能，不是测试环节） | 新服务器的数据库是空的，机器人要重新建：`scripts/ops/astra.sh seed`（注册 24 个机器人并交给 market-sim，最后 `astra.sh mark` 把它们标为 BOT、HOUSE 标为 SYSTEM——只是后台显示用的类型，L0，见 [accounts.md](accounts.md) 账户类型一节；单独补标也可以跑 `astra.sh mark`，幂等）→ `astra.sh mint`（机器人的 ASTRA 与报价资产，数额由运营定）→ `astra.sh open`/`on`、`perp-open`/`perp-on`，`astra.sh status` 核对在报价；`market.flat_minutes` 对 ASTRA 现货与永续开（步 5）。上线后机器人照常买卖 ASTRA，控价（价格事件）必须有机器人在交易，任何时候都不要为了"上线"关掉 `sim.enabled`；后台用户列表默认只看真人只是显示上的筛选，模拟市场页照旧显示全部机器人。 |

## 2. 后台侧（按「上线检查清单」的顺序）

登录 `admin.<域名>`，按下面的顺序改；每一步都有审计。

1. **平台设置**：名称、简称、浅/深色 logo、favicon、apple 图标、主题色、页脚（版权、合规文案、联系邮箱、客服链接、社交链接）、默认语言、`domain`（PC 站主机名）。保存后 1 分钟内三端生效，邮件署名最多 10 分钟后生效。
2. **平台币资料**：ASTRA 的显示名、图标与简介（代码 `ASTRA` 不改）。简介不得再提"模拟/学习"（演练会检查；运维脚本 `scripts/ops/astra.sh profile` 种的是中性文案，后台改过的以后台为准）。
3. **固定页面**：条款、隐私、风险提示、费率说明、关于、联系方式——用编辑过的正式稿发布，或「以默认稿发布」；首页横幅 `home-hero`。
4. **公告与帮助**：不需要逐篇下架或覆盖——第 6 步关掉测试模式后，`modes: TEST` 的「测试环境」公告自动不再出现，各稿件里的 `:::test` 段落自动隐藏、`:::formal` 段落自动显示（2026-10-05 起，设计稿 §4.4）。在后台「内容」页用正式模式预览费率、充值、FAQ 几篇，确认没有测试说法，再发上线公告。
5. **注册赠送**：全部配 **0**（降低与清零单人即可；以后要提高须第二位 ADMIN 并有硬上限）。
6. **测试模式**：关（检查清单项 `test_mode`）。横幅与「测试模式」徽标随之消失，公告与各稿件的测试段落随之隐藏、正式段落显示（第 4 步）；切换后约 1 分钟内三端一致。
7. **注册方式**：开放（或按运营节奏先关闭）。
8. **管理员**：至少两名**真实**、已启用验证器的 ADMIN（端到端与演练创建的临时管理员会在结束时停用，不算数；检查清单的「管理员」项只在两名以上时为绿）；停用端到端留下的临时管理员；`admin.login_without_totp` 已在部署侧关掉。
9. **交易参数**：要开盘的交易对与合约置 `TRADING`，其余 `HALT`/`PREPARE`；费率、风险阶梯、杠杆按运营口径；提现限额与审批限额、延迟。
10. **上线检查清单**：全部绿（含「HOUSE 报价与资金」）；红的按链接去改。清单只看后台能改的项，部署侧以 §1 为准。第 19 项「App 下载」只作信息：显示两站当前提供的 Android/iOS 安装方式（外部链接或上传的包），未配置也算 OK，不影响"可上线"；在 系统 → App 下载 配置（App 下载页设计 2026-10-07，H4）。
11. **开放提现**：步 6 完成且清单全绿后 `wallet.withdraw` 开。
12. **开盘**：恢复各市场、确认 HOUSE 在报价、看 24 小时。

### 2.1 杠杆交易（杠杆设计 2026-10-06，上线检查清单的 margin 项）

测试服上 `margin.enabled` 与 `margin.auto_borrow` 是对所有人打开的（为了端到端与演练），正式环境**不能照搬**：

1. **强平开关先开**：`margin.liquidation` 必须开着——关着时风险率跌到强平线的账户只会被记为待强平（告警 `MarginLiquidationDue`），负债越滚越大。确认 §1 步 10 的保险基金已按每种可借资产注资。
2. **杠杆开关按人或按地区开**：`margin.enabled`（划入、借币、杠杆下单）与 `margin.auto_borrow`（自动借款）从"对所有人"改为按用户（`--allow-users`）或按地区（`--allow-regions`）的规则开放，或在上线时先关着、按运营节奏逐步放开（`exchangectl flags set`，见 `feature-flags.md`）。还币与划回现货不受开关影响：关掉后用户仍能用账户里的币还清负债、把资产划回现货（杠杆账户上下单则要开关开着）。
3. **参数**：后台「杠杆参数」核对各资产的可借与保证金资格、借贷池与单用户上限、利率模型与利率、折扣，各交易对的逐仓倍数与预警/强平线，全仓倍数与阈值（提高风险的改动走双人审批，只收紧的单人即时生效）。
4. **上线检查清单的 margin 项**：杠杆关着（`margin.enabled` 对任何人都不开）为 OK；开着时要求 `margin.liquidation` 也开，且 `margin.enabled`、`margin.auto_borrow` 都不是对所有人全局打开（开关带任何规则——按用户、地区等——就不算全局），否则为 FAIL——测试服现在 FAIL 是对的。
5. **站点**：行情列表里逐仓交易对的倍数标记对所有访客显示（按公开的 `/v1/margin/pairs`，与币安一致，B108）；交易页的账户切换、资产页的杠杆账户只对 `MARGIN_TRADE` 资格开放的人显示（关掉后还有杠杆资产或负债的人仍看得到入口，用来还币与划出）。帮助中心有《杠杆交易入门》（`help/margin-trading`，两种语言）。

### 2.2 币本位永续与合约数据（币本位设计 2026-10-06，上线检查清单的 insurance 与 coin_m 项）

1. **资格开关按人或按地区开**：`derivatives.coin_m` 与 §2.1 的杠杆开关一样，从"对所有人"改为按用户（`--allow-users`）或按地区（`--allow-regions`）的规则，或上线时先关着按节奏放开。开关只管开新仓：关着时已有仓位的用户仍能平仓、撤单与划出。
2. **合约参数**：后台「合约」页核对每个币本位合约的面值、结算币、风险限额阶梯（按币计）与保护带；要开盘的置 `TRADING`。按币种关闭 = 该币未下架的 U 本位与币本位合约都置 `CANCEL_ONLY`（只减仓，HOUSE 照常报价供平仓），重新开放 = 置回 `TRADING`，整组走一条双人审批（`COIN_CONTRACTS_STATUS`）；下架（`DELISTED`）要求没有未平仓位且不可逆。
3. **保险基金按币**：后台「合约」页按资产看余额，每个结算币（USDT、BTC、ETH、ASTRA）都 > 0——检查清单的 insurance 项；注资走双人划转。基金与杠杆交易共用同一行。
4. **HOUSE 额度**：每项的用途、调低/调高的影响与允许范围见 `market-maker.md`「运行时额度」；改动 250 毫秒内生效、不用重启。后台的额度卡接上前由运维按该节改。
5. **上线检查清单**：insurance 项（各结算币基金 > 0）与 coin_m 项（`derivatives.coin_m` 不对所有人全局打开；带任何规则都算）都要绿——测试服现在 coin_m 项 FAIL 是对的。
6. **站点与通知**：两站的合约菜单分 U 本位/币本位，行情列表的合约页签与「合约数据」板块（`/futures/data`：持仓量、多空比、主动买卖比、基差、资金费率历史、爆仓）对所有访客可见；帮助中心《永续合约入门》有币本位一节（简繁英）；强平预警、强平与 ADL 通知以结算币计，资金费结算通知默认不发（用户 2026-10-07 决定）。
7. **演练与端到端**：`scripts/fault/coinm-liquidation.sh`（ASTRA-USD-PERP 上的强平与 ADL，上线前条件）、`coinm-degrade.sh`、`mark-source-outage.sh`（标记价断流约 10 秒后自算顶上、不降级）；端到端 `scripts/e2e/coinm.sh`、`funding.sh`（含币本位）。

## 3. 演练

`scripts/e2e/launch-drill.sh`（`task e2e` 的一步）在测试服上只改后台项：把 §2 的 1–7 设成上线值 → 断言清单里后台项全绿、部署侧项（托管方仍是替身等）按预期红 → 断言三端品牌生效、横幅与"注册即送"消失、法律页可访问、新注册余额为 0、正式模式下费率/充值/FAQ 不含测试说法 → 恢复测试模式与测试值并断言恢复。它证明的是"后台改就能上线"这一半；§1 在正式服务器上按本手册走一遍，`custody.md` 的步骤 4/5 由用户与协调会话一起做。


首次全绿：2026-10-04，测试服 172a5d8，提交 7c6bbba，124 秒：后台项 7 项全部 OK（赠送、横幅、注册方式、品牌、域名、平台币资料、法律页），部署侧项按预期红（后台免验证码、双人审批关、测试资产、替身托管方）；两站一分钟内换名换图标、无横幅与徽标、无"注册即送"、条款页为后台稿；新注册余额 0；结束恢复测试值（当时叫学习值）并核对通过。
## 4. 已知限制

- `index.html` 的 `<title>`/`description`、手机站的 `offline.html` 与静态 `manifest.webmanifest` 回落文件是构建时的（§1 步 8），上线前改仓库里的这几个文件；运行时注入对爬虫无效。
- 邮件署名缓存 10 分钟；站点与后台 1 分钟。
- 站点里与测试网相关的文案（Sepolia、测试网 ETH）随 `ETH-SEPOLIA` 网络一起在正式参考数据里消失；文案本身在站点代码里，去掉网络后不再显示。
- 平台币代码 `ASTRA` 与交易对代码不能在后台改；改名需要新资产（不在范围）。
- 「上线检查清单」的"可上线"只覆盖后台项。
- 币本位的对手方容量受 HOUSE 真实持有的 BTC/ETH 限制（可充提资产不能为负），超出时下单被拒；上线前按预期规模注资。
- 标记价跟币安：币安的流停更超过 10 秒自算顶上（合约不降级），资金费按币安已结算的费率、最多等 2 分钟；币安把某合约停牌或下架时，平台应及时按币种关闭该合约。
- 合约数据板块的币本位持仓量：币安的实时接口与 5 分钟统计接口给出的张数相差约 4 倍（2026-10-07 实测 BTCUSD_PERP 12,589,877 对 3,336,618），总览目前取前者、面板取后者；已定改为同取统计接口的最新点（A71，后台会话实施中），与币安交易页面的实时数字仍可能不同。
