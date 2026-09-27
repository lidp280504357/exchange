# CLAUDE.md — 项目工作约定（新会话先读）

Go 微服务虚拟资产交易所，**学习项目**，1 人（用户）+ Claude 开发。用户确认：假定各国牌照齐备，不限制地区，合规项只作上线前检查，不阻塞开发。

## 文档地图（先读前三份）

| 文件 | 用途 |
|---|---|
| [docs/交接总结-2026-09-29.md](docs/交接总结-2026-09-29.md) | 上一轮会话的完整交接：现状、已完成、下一步、风险 |
| [实施计划.md](实施计划.md) | 阶段计划；§10 是阶段 1 任务清单；§13 是真实进度（只记录已合入且可运行的内容） |
| [需求文档-v0.2.md](需求文档-v0.2.md) | 需求；§7 API 约定、§8 事件、§11 核心规则与公式、附录 B 状态机、附录 C 错误码 |
| [文档评审与待决策-2026-09-27.md](文档评审与待决策-2026-09-27.md) | §8 是用户逐项确认的 27 项决策结论，§9 外部账号 |
| [准备工作清单.md](准备工作清单.md) | 环境与准备状态、后续账号 |
| [docs/adr/](docs/adr/README.md) | 9 条架构决策记录，改决策要新增 ADR |
| [docs/runbook/](docs/runbook/) | Turnstile、Cloudflare+nginx TLS、服务器部署、Grafana Cloud（暂缓） |
| `环境配置.md` | **只在本地**（已被 git 忽略），含测试服凭据与实测状态 |

## 当前状态（2026-09-28）

- 阶段 0 完成，除 M0.2 契约初稿；环境、账号、CI、部署链路全部就绪。
- §10 任务 2–3 完成：`internal/platform/{config,logging,pii,tracing,apperr,httpx,health,app}` 与 `cmd/api-gateway`（`GET /v1/time`），网关已部署在测试服（`https://astras.vip/v1/time`）。**下一步从实施计划 §10 任务 4 开始**：PostgreSQL/Redis/gRPC → 事件 Envelope 与 Outbox/Inbox → …，用户已授权每完成一个任务即提交、推送并部署，连续做完阶段 1。
- 新服务照 `cmd/api-gateway` 写：`main` 调 `app.Main(name, setup, app.WithDefaultOpsAddr(":90xx"))`；`setup` 里 `a.LoadConfig(&cfg)`（`koanf` 标签 = 小写环境变量名）、`a.NewRouter()` 建路由、`a.Add` 注册组件、`a.Cleanup` 注册资源释放、`a.Health().Add` 注册就绪检查。端口表见 `docs/runbook/server-deploy.md`。
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

- 本机不装 Docker。本机 `go run` / `vite dev` 直连测试服基础设施，地址与凭据在本地 `.env`。
- 推送 GitHub 后在测试服更新：`task deploy`（等价 `ssh exchange 'bash /opt/exchange/src/deploy/server-update.sh'`），带提交号回滚。
- 入口 `https://astras.vip`：Cloudflare → nginx 容器 → `api-gateway:8080`（尚未存在，`/v1/*` 返回 502）。
- 应用容器编排文件 `deploy/compose/docker-compose.apps.yml` 尚未创建，服务器 `apps.env` 已备好供 `env_file` 注入。

## Claude Code 会话提示

- Bash 沙箱无网络、不能绑定端口、不能创建 `.git`：git、curl、`go install`、`go get`/`go mod tidy`、绑端口的测试和 `task ci` 用终端面板（`git --no-pager`，命令里别在 URL 后紧跟 `;`，别用 `!`；终端 PATH 不含工具目录，先 `export PATH=$HOME/go/bin:$PATH`）；沙箱里跑 go 命令加 `GOCACHE=$TMPDIR/gocache GOLANGCI_LINT_CACHE=$TMPDIR/golangci-cache GOPROXY=off`；工具在 `~/go/bin`。
- 测试服操作用 MCP `exchange-dev`（`remote_exec`、`remote_put_file`、`compose`、`pg_query`、`redis_cmd`、`ch_query`）。
- GitHub 远程用 `git@github-ldp:lidp280504357/exchange.git`（本机 ssh 别名）。
- 可能有并行会话在同一目录，改共享文档前先看 `git status`。
