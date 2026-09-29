# 运行手册索引

测试环境入口 `https://astras.vip`（H5）、`https://astras.vip/docs/`（API 参考）、`https://astras.vip/admin/`（管理后台）；服务器操作见 [server-deploy.md](server-deploy.md)。凭据只在本地 `.env` 与服务器 `/opt/exchange/infra/{.env,apps.env}`。

## 开发、部署与测试

| 手册 | 内容 |
|---|---|
| [local-dev.md](local-dev.md) | 本机开发栈 `task dev`：本机跑全部服务，连测试服基础设施的 dev 命名空间 |
| [server-deploy.md](server-deploy.md) | 测试服部署与回滚、端口表、应用环境变量 |
| [testing.md](testing.md) | 测试层级、端到端（`task e2e`）与故障注入（`task fault`） |
| [h5.md](h5.md) | H5 前端：开发、构建、发布、浏览器冒烟测试 |
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
| [matching.md](matching.md) | 撮合引擎：规则、WAL 与快照、恢复、主备租约 |
| [market-data.md](market-data.md) | 平台行情：K 线、ticker、深度、最近成交，WebSocket 公共频道与 orders/fills 私有频道 |
| [market-maker.md](market-maker.md) | 参考行情（币安公开数据，仅测试环境）与做市机器人：参数、撤单条件、测试服设置 |
| [instruments.md](instruments.md) | 资产、网络、交易对、费率，`exchangectl instruments` |
| [ledger.md](ledger.md) | 账本、划转、对账，`exchangectl ledger` |
| [wallet.md](wallet.md) | 充值、signer 与 keystore、归集、提现审批与签名、链上对账，`exchangectl wallet` |
| [admin.md](admin.md) | 管理后台（`/admin/`）：管理员与 TOTP、角色、提现审批、用户处置、交易对与开关、双人调账、审计查询，`exchangectl admin` |
| [grafana-cloud.md](grafana-cloud.md) | Grafana Cloud 接入（暂缓） |

## API 文档

`https://astras.vip/docs/` 由 `web/h5/scripts/build-docs.mjs` 在 H5 构建时生成：把 `api/openapi/*.yaml`（每个服务一份）合并成一个 OpenAPI 文档 `docs/openapi.json`，用 Redoc 展示（固定版本，浏览器按 SRI 哈希校验）。合并时组件同名但内容不同、路径重复、引用无法解析都会让构建失败。事件契约在 `api/proto`（`buf lint`、`buf breaking`）。
