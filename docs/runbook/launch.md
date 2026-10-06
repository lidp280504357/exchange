# 上线手册：一套部署、后台改配置即上线

依据：`docs/设计-后台可配置与上线开关-2026-10-04.md`（用户 2026-10-04 需求）。目标：正式上线用的是**同一套镜像与编排**，不改代码；上线前的差别分两类——**部署侧**（环境、密钥、第三方账号、参考数据，§1）和**后台侧**（品牌、文案、开关，§2）。后台「上线检查清单」页只覆盖 §2 的项，§1 由本手册负责。

## 0. 先说清楚"同一套部署"是什么

- 同一套：`deploy/compose/*`、GHCR 镜像、`deploy/server-update.sh`、迁移、nginx 配置、`exchangectl`。
- 不同的：`apps.env`/`.env` 里的值、`deploy/instruments/<环境>.json` 参考数据、Cloudflare 与域名、托管方商户、数据库里的数据。
- **数据不迁移**：正式环境从一台新服务器、空数据库开始（`server-deploy.md`「从零部署一台新服务器」）。测试服上有机器人账户、端到端账户、模拟资金与模拟地址，不能当正式数据用。测试模式下注册送的资金在正式库里根本不存在，不需要"清零"。

## 1. 部署侧（按顺序）

| 步 | 做什么 | 说明 |
|---|---|---|
| 1 | 新服务器与域名 | 按 `server-deploy.md` 从零部署；`PUBLIC_DOMAIN` 设为正式域名；Cloudflare DNS 三个主机名（PC、`m.`、`admin.`）与源站证书按 `cloudflare-nginx-tls.md`；nginx 三个 server 块的 `server_name` 改为正式域名（配置文件在仓库，按域名出一份正式版） |
| 2 | 密钥全部重新生成 | `OTP_HMAC_KEY`、`JWT_SIGNING_KEY`/`JWT_KEY_ID`、`TOTP_SECRET_KEY`、`ADMIN_SECRET_KEY`、`SIM_API_SECRET`、`SIM_ADMIN_API_SECRET`、Postgres/Redis/ClickHouse 口令；**`CAPTCHA_BYPASS_TOKEN` 留空**（正式环境不得有绕过）。变量名清单见 `.env.example`；值只进服务器 `apps.env`/`.env` |
| 3 | 第三方账号 | Turnstile 正式站点密钥与允许主机名（`turnstile.md`）；邮件服务密钥与已验证的发件域（`MAIL_FROM`）；优盾正式商户（`UDUN_*`，见步 6）；Alchemy 只服务 Sepolia 测试网，正式环境**不配** |
| 4 | 参考数据 | 复制 `deploy/instruments/test.json` 为正式版：去掉 `ETH-SEPOLIA` 网络、去掉隐藏资产 `TUSD` 与 `UDUNMOCK` 托管方（它们只为端到端存在）、`provider_coin` 按优盾正式商户的 `support-coins` 核对（`custody.md`「直接问网关」）、费率与最小充提额按运营口径；`exchangectl instruments apply` |
| 5 | 开关（`exchangectl flags`，见 `feature-flags.md`） | `admin.login_without_totp` **关**；`wallet.test_assets` **关**（并删掉地区规则）；`admin.two_person_approval` 开；`risk.enforce` 开；`market.house_liquidity` 开；`market.flat_minutes` 对 ASTRA 现货与永续开；`sim.enabled`/`sim.events`/`sim.perp` 按运营决定；`ledger.welcome_credit` 可保留开（数额由后台配 0 即不发）；`wallet.withdraw` 在步 6 完成前**关** |
| 6 | 托管方切换与主网小额测试 | 按 `custody.md`「真网关联调」步骤 4、5（账本基线、退役替身地址、`apps.env` 并入 `UDUN_*`、只重启 wallet-service、核对；用户 TRC20 充值 ≥ 12 USDT、提现 10 USDT、确认手续费单位、收紧回调来源 IP）；正式环境没有 `udun-mock` 容器，compose 里去掉它 |
| 7 | HOUSE 库存与上限 | HOUSE 的 USDT/BTC/ETH 是"背书资产"，正式环境必须是托管方真实持有的资金：先向商户钱包充入库存，再 `scripts/ops/house.sh seed` 到相应水平、`exchangectl wallet reconcile` 无短缺；compose 里的单资产/单合约上限按真实库存设（测试服的 2,000,000 / 5,000,000 是放大过的） |
| 8 | 构建时的品牌 | `web/apps/{pc,m}/index.html` 的 `<title>` 与 `description`（供爬虫与分享卡片）在构建时写死，运行时注入只改脚本执行后的页面——上线前把这两个文件里的名称与描述改成正式值再部署（已知限制，见 §4） |
| 9 | 告警与值班 | `deploy/observability/alerts.yml` 的接收端指向正式的通知渠道（`observability.md`）；确认 `CustodyShortfall`、`WalletWithdrawalsSuspended`、`MarketSimBandDeadlock` 等有人接 |
| 10 | 杠杆参数与保险基金（杠杆设计 2026-10-06，见 §2.1） | 复制 `deploy/instruments/margin.json` 为正式版：借贷池上限按 HOUSE 真实持有的可充提资产定（USDT/BTC/ETH 是背书资产，借出去的币用户可以卖成别的币再划回现货）、利率与保证金折扣按运营口径，`exchangectl margin apply`；保险基金 `INSURANCE_FUND` 按**每种可借资产**分别注资（`exchangectl ledger insurance-fund --asset <资产>`，正式环境走后台双人划转）——基金不允许为负，某资产不够时强平停在 `SHORTFALL`、负债留着并告警；告警 `InsuranceFundShort`、`MarginLiquidationStuck`、`MarginLiquidationDue`、`MarginMonitorStalled`、`MarginInterestStalled`、`MarginReconciliationMismatch`、`TradingOrdersPendingFreeze` 要有人接（`margin.md`） |

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
10. **上线检查清单**：全部绿（含「HOUSE 报价与资金」）；红的按链接去改。清单只看后台能改的项，部署侧以 §1 为准。
11. **开放提现**：步 6 完成且清单全绿后 `wallet.withdraw` 开。
12. **开盘**：恢复各市场、确认 HOUSE 在报价、看 24 小时。

### 2.1 杠杆交易（杠杆设计 2026-10-06，上线检查清单的 margin 项）

测试服上 `margin.enabled` 与 `margin.auto_borrow` 是对所有人打开的（为了端到端与演练），正式环境**不能照搬**：

1. **强平开关先开**：`margin.liquidation` 必须开着——关着时风险率跌到强平线的账户只会被记为待强平（告警 `MarginLiquidationDue`），负债越滚越大。确认 §1 步 10 的保险基金已按每种可借资产注资。
2. **杠杆开关按人或按地区开**：`margin.enabled`（划入、借币、杠杆下单）与 `margin.auto_borrow`（自动借款）从"对所有人"改为按用户（`--allow-users`）或按地区（`--allow-regions`）的规则开放，或在上线时先关着、按运营节奏逐步放开（`exchangectl flags set`，见 `feature-flags.md`）。还币与划回现货不受开关影响：关掉后用户仍能用账户里的币还清负债、把资产划回现货（杠杆账户上下单则要开关开着）。
3. **参数**：后台「杠杆参数」核对各资产的可借与保证金资格、借贷池与单用户上限、利率模型与利率、折扣，各交易对的逐仓倍数与预警/强平线，全仓倍数与阈值（提高风险的改动走双人审批，只收紧的单人即时生效）。
4. **上线检查清单的 margin 项**：杠杆关着（`margin.enabled` 对任何人都不开）为 OK；开着时要求 `margin.liquidation` 也开，且 `margin.enabled`、`margin.auto_borrow` 都不是对所有人全局打开（开关带任何规则——按用户、地区等——就不算全局），否则为 FAIL——测试服现在 FAIL 是对的。
5. **站点**：行情列表里逐仓交易对的倍数标记对所有访客显示（按公开的 `/v1/margin/pairs`，与币安一致，B108）；交易页的账户切换、资产页的杠杆账户只对 `MARGIN_TRADE` 资格开放的人显示（关掉后还有杠杆资产或负债的人仍看得到入口，用来还币与划出）。帮助中心有《杠杆交易入门》（`help/margin-trading`，两种语言）。

## 3. 演练

`scripts/e2e/launch-drill.sh`（`task e2e` 的一步）在测试服上只改后台项：把 §2 的 1–7 设成上线值 → 断言清单里后台项全绿、部署侧项（托管方仍是替身等）按预期红 → 断言三端品牌生效、横幅与"注册即送"消失、法律页可访问、新注册余额为 0、正式模式下费率/充值/FAQ 不含测试说法 → 恢复测试模式与测试值并断言恢复。它证明的是"后台改就能上线"这一半；§1 在正式服务器上按本手册走一遍，`custody.md` 的步骤 4/5 由用户与协调会话一起做。


首次全绿：2026-10-04，测试服 172a5d8，提交 7c6bbba，124 秒：后台项 7 项全部 OK（赠送、横幅、注册方式、品牌、域名、平台币资料、法律页），部署侧项按预期红（后台免验证码、双人审批关、测试资产、替身托管方）；两站一分钟内换名换图标、无横幅与徽标、无"注册即送"、条款页为后台稿；新注册余额 0；结束恢复测试值（当时叫学习值）并核对通过。
## 4. 已知限制

- `index.html` 的 `<title>`/`description`、手机站的 `offline.html` 与静态 `manifest.webmanifest` 回落文件是构建时的（§1 步 8），上线前改仓库里的这几个文件；运行时注入对爬虫无效。
- 邮件署名缓存 10 分钟；站点与后台 1 分钟。
- 站点里与测试网相关的文案（Sepolia、测试网 ETH）随 `ETH-SEPOLIA` 网络一起在正式参考数据里消失；文案本身在站点代码里，去掉网络后不再显示。
- 平台币代码 `ASTRA` 与交易对代码不能在后台改；改名需要新资产（不在范围）。
- 「上线检查清单」的"可上线"只覆盖后台项。
