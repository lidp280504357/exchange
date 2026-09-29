# CLAUDE.md — 项目工作约定（新会话先读）

Go 微服务虚拟资产交易所，**学习项目**，1 人（用户）+ Claude 开发。用户确认：假定各国牌照齐备，不限制地区，合规项只作上线前检查，不阻塞开发。

## 文档地图（先读前三份）

| 文件 | 用途 |
|---|---|
| [docs/阶段1验收报告.md](docs/阶段1验收报告.md) | 阶段 1 全景：交付内容、12 条验收标准的证据、测试与故障注入结果、资源评估、已知限制与阶段 2 依赖 |
| [docs/交接总结-2026-09-29.md](docs/交接总结-2026-09-29.md) | 阶段 1 编码开始前的交接：环境、账号、决策与风险 |
| [实施计划.md](实施计划.md) | 阶段计划；§10 是阶段 1 任务清单，§6.3 是阶段 2 任务清单与用户决策；§13 是真实进度（只记录已合入且可运行的内容） |
| [需求文档-v0.2.md](需求文档-v0.2.md) | 需求；§7 API 约定、§8 事件、§11 核心规则与公式、附录 B 状态机、附录 C 错误码 |
| [文档评审与待决策-2026-09-27.md](文档评审与待决策-2026-09-27.md) | §8 是用户逐项确认的 27 项决策结论，§9 外部账号 |
| [准备工作清单.md](准备工作清单.md) | 环境与准备状态、后续账号 |
| [docs/adr/](docs/adr/README.md) | 9 条架构决策记录，改决策要新增 ADR |
| [docs/runbook/](docs/runbook/README.md) | 运维手册（README 是索引）：local-dev（本机开发栈）、observability（指标、告警、日志、trace、回滚）、testing、events、各服务手册（含 wallet、admin 管理后台）、服务器部署、Cloudflare+nginx TLS、Turnstile |
| `环境配置.md` | **只在本地**（已被 git 忽略），含测试服凭据与实测状态 |

## 当前状态（2026-09-28）

- 阶段 0 完成，除 M0.2 契约初稿；环境、账号、CI、部署链路全部就绪。
- §10 任务 2–8 完成：`internal/platform/{config,logging,pii,tracing,apperr,httpx,health,app,pg,migrate,redisx,grpcx,bootstrap,testenv,event,kafka,outbox,inbox,idempotency,chx,flags}`、`internal/analytics`；`cmd/` 下网关、六个服务骨架、analytics-consumer 与运维 CLI `exchangectl` 已部署在测试服（`https://astras.vip/v1/time`）。任务 9 的 auth/users/notify 迁移与契约、任务 10 的 OTP（`internal/auth`、`internal/notification`、网关 `/v1/auth/*` 代理，见 `docs/runbook/otp.md`）、任务 11 的注册/登录/令牌/会话/step-up/换绑（`internal/auth`、`internal/user`、`internal/platform/authtoken`、网关鉴权 `internal/gateway/auth.go` 与路由表 `routes.go`，见 `docs/runbook/auth.md`）、任务 12 的账户状态/eligibility/用户通知（`internal/user`、`internal/notification` 的站内信与安全邮件、`exchangectl users`，见 `docs/runbook/accounts.md`）、任务 13 的 instrument-service（资产/网络/交易对/费率、`exchangectl instruments apply` 幂等同步 `deploy/instruments/test.json`，见 `docs/runbook/instruments.md`）、任务 14 的账本（`internal/ledger`，余额只能经 `Post` 写分录，见 `docs/runbook/ledger.md`）、任务 15 的网关限流/幂等键/WebSocket（见 `docs/runbook/gateway.md`）、任务 16 的 H5（`web/h5`，见 `docs/runbook/h5.md`）、任务 17 的测试补齐与故障注入（`scripts/e2e`、`scripts/fault`、`exchangectl dlq`，见 `docs/runbook/testing.md`、`events.md`）、任务 18 的本机开发栈、API 参考页、告警规则与验收报告（`scripts/dev.sh`、`https://astras.vip/docs/`、`deploy/observability`、`scripts/trace.sh`、`docs/阶段1验收报告.md`）已完成，**阶段 1 完成**。阶段 2 的范围与顺序用户已于 2026-09-28 确认，见实施计划 §6.3（交易优先：风控遗留 → 订单 → 撮合 → 结算 → 行情 → 交易页 → 外部行情与做市 → TOTP → 钱包 → 签名与提现 → 管理后台 → 读模型、压测与 PC 端；压测前再升级测试服；外部行情只接币安公开接口）。用户授权照阶段 1 的方式继续：每个任务 `task ci` → 提交 → 推送 → 部署 → 验证，不等回复，契约与表结构在进度消息里展示，每个里程碑汇报。§6.3 任务 1 的风控已完成（`internal/risk`、`risk.events`、开关 `risk.enforce`、`exchangectl risk`，见 `docs/runbook/risk.md`），任务 2 的现货订单已完成（spot-trading-service、`internal/trading`、`/v1/orders`、`order.events`/`order.commands`，见 `docs/runbook/trading.md`），任务 3 的撮合引擎已完成（matching-engine、`internal/matching`、`trade.events`、`pg.AcquireLease` 主备，见 `docs/runbook/matching.md`），任务 4 的结算已完成（ledger-service 消费 `trade.events`，`TRADE_SETTLE`/`TRADE_FEE`/价差 `ORDER_UNFREEZE`，不变量 5 对账，见 `docs/runbook/ledger.md`），任务 5 的平台行情已完成（market-data-service、`market.depth`/`market.candle.events`、`/v1/market/{symbol}/*`、WebSocket 公共频道与 orders/fills，见 `docs/runbook/market-data.md`），任务 6 的 H5 行情与交易页已完成（`/markets`、`/trade/:symbol`，见 `docs/runbook/h5.md`），任务 7 的参考行情与做市已完成（开关 `market.reference_feed`、`market.maker`，market-maker 服务，见 `docs/runbook/market-maker.md`；测试服 BTC-USDT 有做市报价，**端到端脚本在 ETH-BTC 上成交**），任务 8 的身份验证器已完成（TOTP 绑定/step-up/解绑，`TOTP_SECRET_KEY`，见 `docs/runbook/auth.md`），任务 9 的钱包充值已完成（signer keystore 只导出 xpub，wallet-service 派生地址并扫描 Sepolia，账本 `DEPOSIT_CREDIT`，`/v1/wallet/*`、频道 `deposits`，见 `docs/runbook/wallet.md`；Alchemy URL 含 API key，只经 `evm.Client`，错误里不带 URL；端到端 `deposit.sh` 用 `.env` 的 `E2E_SEPOLIA_SENDER_KEY` 真实转账，余额不足 0.003 ETH 时跳过链上部分），任务 10 的签名服务与提现已完成（`signer serve` 容器、提现地址簿/冷却/限额/风控/`exchangectl wallet approve`、归集 `wallet sweep`、GAS_SUPPLY 注资 `wallet fund`、不变量 4 链上对账，见 `docs/runbook/wallet.md`；go-ethereum 纯 Go 构建要求私钥用 `crypto.ToECDSA` 生成；端到端 `withdraw.sh` 需要测试服开关 `wallet.withdraw` 开着），任务 11 的管理后台 MVP 已完成（admin-service + `web/admin`，nginx 直连 `/admin/v1/` 与 `/admin/`、不经网关；管理员用 admin-service 容器里的 `exchangectl admin create` 建，`ADMIN_SECRET_KEY` 只在服务器 `admin/admin.env`；见 `docs/runbook/admin.md`；契约 `api/admin/admin.yaml`，改完 `task admin:types`；端到端 `admin.sh` 每次建 4 个随机管理员、结束停用）。任务 12 已做：ClickHouse 读模型（`trades`、`orders_current`、`wallet_*`、`candles_1m`/`candles()`，启动时回填一次，见 `docs/runbook/analytics.md`）与管理后台报表页；故障注入 `scripts/fault/{matching-failover,chain-outage,reference-outage}.sh`（用主机 `DOCKER-USER` 链丢包，规则带 `exchange-fault-<服务>` 注释）；压测工具 `cmd/loadgen` 与升配前基线（`docs/压测报告-阶段2.md`：WebSocket 5,000 达标，单交易对 5,000 命令/秒未达标，t2.medium 上引擎约 3,000/秒）。**下一步：任务 12 剩余——升配后复测命令吞吐（需用户在 AWS 升级实例或开压测机）、PC 端 Tauri 壳（本机无 Rust，只能写工程文件）。** 每次改动后跑 `task e2e`（`scripts/e2e/*.sh`，对 https://astras.vip），动到基础设施行为时再跑 `task fault`。
- 服务隔离：`.golangci.yml` 的 depguard 规则禁止 `internal/<服务>` 互相 import，新服务要在那里补一组规则。消费事件用 `bootstrap.Consumer` + 应用层经 inbox 去重（auth 的 `Store.Once`、notification 的 `inbox.ProcessID`）。
- 鉴权：网关验 JWT 后把身份写进 `X-User-Id`/`X-Session-Id`/`X-Auth-Scope` 头转发（客户端同名头会被剥掉），服务端用 `httpx.UserID(r)`/`httpx.SessionID(r)` 读取；新的公开接口要加进 `internal/gateway/routes.go`，否则默认必须登录。敏感操作读 `X-Step-Up-Token`，跨服务用 auth-service gRPC `ConsumeStepUp` 兑换。
- 功能开关：`bootstrap.Flags` 拿 `*flags.Client`，`Enabled(key, flags.Subject{...})`；改开关用 `exchangectl flags set`（见 `docs/runbook/feature-flags.md`）。
- 事件：契约在 `api/proto`，改完 `task proto`（buf，生成到 `api/gen/go` 并提交）；发事件 = `bootstrap.Events` 拿 `event.Factory` → 业务事务里 `outbox.Add`；消费 = `bootstrap.Consumer` + 处理函数里 `inbox.Process` 去重。
- 加依赖后必须 `go mod tidy`（本机 macOS，Linux 专用依赖只有 tidy 才会写进 go.sum；`task ci` 含 `go mod tidy -diff`）。集成测试：`task test:integration`（读 `.env` 的 `TEST_*`，测试服 `exchange_test` 库、Redis DB 15）。
- 新服务照 `cmd/auth-service` 写：`main` 调 `app.Main(name, setup, app.WithDefaultOpsAddr(":90xx"))`；`setup` 里 `a.LoadConfig(&cfg)`（`koanf` 标签 = 小写环境变量名，嵌套配置用 `koanf:",squash"`），用 `bootstrap.Postgres/Redis/GRPCServer/HTTPServer` 接基础设施（自动登记就绪检查、指标、清理），`a.NewRouter()` 建路由。端口表见 `docs/runbook/server-deploy.md`。
- 本机 `/usr/bin/python3` 是 3.9，源码里超长的中文行会误报 "Non-UTF-8 code"；改文档用 Edit 工具，别用内联 Python 长字符串。
- 契约初稿（OpenAPI、Protobuf）和数据库初始化脚本开始前先给用户过目（用户 2026-09-28 说不必等回复，可在进度消息里展示后继续）。

## 硬性约定

1. 密钥只在本地 `.env` 与服务器 `/opt/exchange/infra/{.env,apps.env}`，永不写进仓库、文档、日志。变量名清单见 `.env.example`。
2. 金额一律 `shopspring/decimal`，JSON 字符串，禁止 float；对外 ID UUIDv7；详见 ADR-0008。
3. 余额只能由账本分录改变（ADR-0001）；撮合不持有余额（ADR-0002）；私钥只在 signer（ADR-0003）。
4. 仓库单 module：`cmd/<svc>`、`internal/<svc>/{domain,application,ports,adapters,transport}`、`internal/platform` 共享；服务间禁止互相 import（ADR-0006）。
5. 登录模型：密码 + 验证码注册；密码登录，7 天无成功登录追加验证码；不做 IP 地理判断（ADR-0009）。
6. 高风险能力走功能开关，默认关闭（ADR-0005）。
7. 每次修改需求/计划/评审文档，在该文档 §0 变更记录加一行；进度只写已合入、可运行、有测试的内容。
8. 提交前本地 `task ci` 必须通过；Conventional Commits（`feat:`、`fix:`、`docs:`、`chore:`、`ci:`）。
9. Go 代码与注释用英文，文档用中文。

## 开发与部署

- 本机不装 Docker。本机开发栈 `task dev`（全部服务）/ `task run -- <服务>` 在本机运行服务，连测试服基础设施里单独的 dev 命名空间（库 `exchange_dev`、Redis DB 1、Kafka 前缀 `dev.`，见 `docs/runbook/local-dev.md`），不碰测试环境数据；地址与凭据在本地 `.env`。`task web:dev` 的 H5 默认代理到测试服，`API_ORIGIN=http://localhost:8080` 改连本机网关。
- 推送 GitHub 后在测试服更新：`task deploy`（等价 `ssh exchange 'bash /opt/exchange/src/deploy/server-update.sh'`），带提交号回滚；脚本同时同步参考数据、热加载 nginx、构建并发布 H5。
- 入口 `https://astras.vip`：Cloudflare → nginx 容器 → `/v1/*` 到 `api-gateway:8080`，其余为 H5 静态文件。
- 应用容器编排在 `deploy/compose/docker-compose.apps.yml`，密钥由服务器 `apps.env` 经 `env_file` 注入。
- 前端：`web/h5`（pnpm 11、Node 24），`task web:check` 在 `task ci` 里；改 OpenAPI 后 `task web:types` 并提交生成文件。Claude Code 预览用 `.claude/launch.json` 的 `h5`（端口必须是 5173，刷新 Cookie 与 WebSocket 的来源白名单写的就是它）。

## Claude Code 会话提示

- Bash 沙箱无网络、不能绑定端口、不能创建 `.git`：git、curl、`go install`、`go get`/`go mod tidy`、绑端口的测试和 `task ci` 用终端面板（`git --no-pager`，命令里别在 URL 后紧跟 `;`，别用 `!`；终端 PATH 不含工具目录，先 `export PATH=$HOME/go/bin:$PATH`）；沙箱里跑 go 命令加 `GOCACHE=$TMPDIR/gocache GOLANGCI_LINT_CACHE=$TMPDIR/golangci-cache GOPROXY=off`；工具在 `~/go/bin`。
- 测试服操作用 MCP `exchange-dev`（`remote_exec`、`remote_put_file`、`compose`、`pg_query`、`redis_cmd`、`ch_query`）。
- GitHub 远程用 `git@github-ldp:lidp280504357/exchange.git`（本机 ssh 别名）。
- 可能有并行会话在同一目录，改共享文档前先看 `git status`。
- 终端面板的命令只能是 ASCII，也不能有以 `#` 开头的词。批量替换别用 `perl -pi -e 's|…|…|'` 且模式里含 `\|`：它会变成空分支，把替换文本插到文件开头；含 `|` 的替换用 Edit 工具。
- 运行中的 bash 脚本（如 `task dev`）别改：bash 按偏移逐段读脚本，改了会读到错位内容；先停掉再改。
- 本机到测试服往返约 380 ms：连测试服的集成测试与本机栈都慢，等待时给足超时（`kafka.ReadDLQ` 这类每次新建客户端的操作用 20 秒以上）。
