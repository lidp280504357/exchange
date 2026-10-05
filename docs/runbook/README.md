# 运行手册索引

测试环境入口 `https://astras.vip`（PC 站）、`https://m.astras.vip`（手机站）、`https://admin.astras.vip`（管理后台）、`https://astras.vip/docs/`（API 参考）、`https://astras.vip/storybook/`（设计系统目录）；过渡期旧后台在 `https://astras.vip/admin/`（旧 H5 已在手机站完成后下线，`/h5/` 301 到首页）。服务器操作见 [server-deploy.md](server-deploy.md)。凭据只在本地 `.env` 与服务器 `/opt/exchange/infra/{.env,apps.env}`。

## 开发、部署与测试

| 手册 | 内容 |
|---|---|
| [local-dev.md](local-dev.md) | 本机开发栈 `task dev`：本机跑全部服务，连测试服基础设施的 dev 命名空间 |
| [server-deploy.md](server-deploy.md) | 测试服部署与回滚、端口表、应用环境变量 |
| [testing.md](testing.md) | 测试层级、端到端（`task e2e`）与故障注入（`task fault`） |
| [web.md](web.md) | 前端：PC 站、手机站、管理后台与共享包（数据层、设计系统），开发、构建、部署、设备分流、性能检查 |
| [ui-checklist.md](ui-checklist.md) | 前端人工检查清单（每批前端验收前逐项勾选） |
| [cloudflare-nginx-tls.md](cloudflare-nginx-tls.md) | 域名、Cloudflare 代理、nginx 与源站证书 |
| [turnstile.md](turnstile.md) | Cloudflare Turnstile 人机验证 |

## 运行与排障

| 手册 | 内容 |
|---|---|
| [observability.md](observability.md) | 指标与告警清单、日志字段、按 trace 查询（`scripts/trace.sh`）、回滚步骤 |
| [events.md](events.md) | outbox、消费者、重试与死信，`exchangectl dlq` 重放 |
| [feature-flags.md](feature-flags.md) | 功能开关（ADR-0005），`exchangectl flags` |
| [gateway.md](gateway.md) | 网关鉴权、限流、幂等键、WebSocket |
| [otp.md](otp.md) | 验证码频控、服务商链、开发收件箱、指标 |
| [auth.md](auth.md) | 注册、登录、令牌、会话、step-up、换绑 |
| [accounts.md](accounts.md) | 账户状态、资格、用户通知，`exchangectl users` |
| [risk.md](risk.md) | 风控规则、评估记录与自动审核（`risk.enforce`），`exchangectl risk` |
| [trading.md](trading.md) | 现货下单、冻结、撤单与开放交易对，`/v1/orders` |
| [matching.md](matching.md) | 撮合引擎：规则、WAL 与快照、恢复、主备租约；现货与合约两个分片 |
| [derivatives.md](derivatives.md) | USDT 永续合约：设置、下单与保证金预留、成交结算、仓位、转出保护、只减仓、对账（不变量 6），`exchangectl derivatives` |
| [margin.md](margin.md) | 杠杆交易：账本三行记法、条款与种子（`margin.json`）、借币与还币、整点计息、估值、对账（不变量 7–9），`exchangectl margin` |
| [market-data.md](market-data.md) | 平台行情：K 线、ticker、深度、最近成交，合约的指数价、标记价与资金费率，WebSocket 公共频道与 orders/fills 私有频道 |
| [market-maker.md](market-maker.md) | 参考行情（币安公开数据，仅测试环境）与做市机器人：参数、撤单条件、测试服设置 |
| [market-sim.md](market-sim.md) | 平台币 ASTRA 的模拟市场：价格模型、机器人角色、节流、设置与管理接口、`astra.sh seed/on/off/mint` |
| [instruments.md](instruments.md) | 资产、网络、交易对、费率，`exchangectl instruments` |
| [ledger.md](ledger.md) | 账本、划转、对账，`exchangectl ledger` |
| [wallet.md](wallet.md) | 充值、signer 与 keystore、归集、提现审批与签名、链上对账，`exchangectl wallet` |
| [custody.md](custody.md) | 托管钱包（优盾，ADR-0011）：接口映射、回调、`SUBMITTED` 状态、托管方对账、测试服模拟网关 `udun-mock`、换成真网关 |
| [analytics.md](analytics.md) | ClickHouse：事件、审计、分录与订单/成交/钱包/K 线读模型，回填、常用查询与核对 |
| [launch.md](launch.md) | 上线手册：一套部署、后台改配置即上线——部署侧步骤（域名、密钥、第三方、参考数据、开关、托管方切换、HOUSE 库存）与后台侧顺序（平台设置、固定页面、公告、注册赠送 0、横幅、检查清单） |
| [admin.md](admin.md) | 管理后台（`/admin/`）：管理员与 TOTP、角色、提现审批、用户处置、交易对与开关、双人调账、审计查询，`exchangectl admin` |
| [grafana-cloud.md](grafana-cloud.md) | Grafana Cloud 接入（暂缓） |

## API 文档

`https://astras.vip/docs/` 由 `web/apps/pc/scripts/build-docs.mjs` 在 PC 站构建时生成：把 `api/openapi/*.yaml`（每个服务一份）合并成一个 OpenAPI 文档 `docs/openapi.json`，用 Redoc 展示（固定版本，浏览器按 SRI 哈希校验）。合并时组件同名但内容不同、路径重复、引用无法解析都会让构建失败。事件契约在 `api/proto`（`buf lint`、`buf breaking`）。
