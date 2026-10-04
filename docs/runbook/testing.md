# 测试与故障注入

实施计划 §9.1 的测试层级在阶段 1 的落地方式；与验收标准的逐条对应见阶段验收报告。

| 层级 | 在哪里 | 怎么跑 |
|---|---|---|
| 单元测试 | 各包 `*_test.go`（状态机全部非法转移、金额与精度、幂等、错误码映射、令牌、限流、调度重试与熔断）；前端 Vitest（`web/packages/*/src`、`web/apps/*/src` 的 `*.test.ts(x)`） | `task test`、`task web:check`（都在 `task ci` 里） |
| 属性测试 | `internal/ledger/domain`：固定种子的随机分录序列下每资产零和、受限账户不为负 | 同上 |
| 契约测试 | Protobuf：`buf lint`、生成代码一致、`buf breaking`（CI 对比上一个提交，本机 `task proto:breaking` 对比 main）；OpenAPI：前端类型由契约生成且一致（`packages/core/src/api/gen`），浏览器冒烟测试把看到的每个 API 响应按契约校验（`web/e2e/contract.mjs`，Ajv/JSON Schema 2020-12）；gRPC 错误码跨服务保持（`grpcx` 测试） | `task ci`、`task e2e` |
| 集成测试 | 连测试服的 `exchange_test` 库、Redis DB 15、临时 topic 与 ClickHouse 库（`internal/platform/testenv`，未配置则跳过）：仓储、迁移与回滚、outbox/inbox、Kafka 重试/死信/重放、账本并发与对账、CLI | `task test:integration`（本机到测试服约 25 分钟；ClickHouse 要经 SSH 隧道，见下文 CI 一节）；CI 每次推送都跑（约 5 分钟） |
| 端到端 | `scripts/e2e/*.sh` 对已部署环境：`auth`（注册、令牌轮换与重放、会话、step-up）、`identity`（手机号注册登录、绑定、换绑）、`account`（资料、资格、冻结与令牌刷新、通知）、`gateway`（限流头、幂等重放、WebSocket）、`ledger`（欢迎资金、划转）、`market`、`web`（PC 站、手机站、后台三站的页面与 SPA 回退、缓存头与 gzip、设备分流与 `site_pref`、手机站 manifest、service worker 与离线页、`/h5/` 跳首页、后台安全头、API 参考页与 Storybook，最后用无头 Chrome 跑 PC 站与手机站的浏览器冒烟测试 `web/e2e/{pc,m}-smoke.mjs`，见 [web.md](web.md#性能与检查)）、`ops`（trace ID、健康与指标、账本与 ClickHouse 核对为 0）、`pii`（ClickHouse 与日志里没有明文邮箱与验证码）、`risk`（新设备登录记分、同设备注册爆发进入风控审核）、`trading`（下单冻结、校验、余额不足被拒、client_order_id 重试、撤单请求）、`matching`（ETH-BTC 上与 HOUSE 成交：越过盘口的限价单按 HOUSE 的价成交、用户之间不成交、POST_ONLY 越价被拒与 IOC、市价单、限价买单差额、撤单与解冻、按成交核对结算后的余额与流水）、`marketdata`（行情 REST 的校验；参考行情：交易对映射（ETH-BTC 跟随 ETHBTC）、币安 ticker、概览、K 线翻页与 `tickers` 频道（只推变化的、约每秒一次）；ETH-BTC 公共频道的深度快照与连续增量、ticker、K 线，BTC-USDT 的实时成交；一笔与 HOUSE 的市价买单在 orders/fills 频道上的推送，以及 REST 成交、ticker、K 线、深度）、`totp`、`deposit`/`withdraw`（Sepolia 真实转账）、`custody`（托管钱包与测试服模拟网关：托管方地址、充值入账一次、伪造与过期回调被拒、经公网的回调在 nginx 被拒、提现 `SUBMITTED` → `CONFIRMED`/`FAILED`、网关丢应答与重交被拒时停在 `UNCERTAIN` 再被回调了结、按最小单位报的手续费不入账、托管方对账，见 [custody.md](custody.md)）、`astra`（平台币的模拟市场：ASTRA-USDT 每侧至少 8 档、价差不超过 0.3%、有自己的成交与 K 线，新用户向机器人市价买入再卖出并在账本结算，限价单挂着再撤单；交易对不在交易或机器人没开时跳过，见 [market-sim.md](market-sim.md)）、`house`（HOUSE 虚拟流动性：每个交易中的交易对与合约都有 HOUSE 报价，SOL-USDT 与 ETH-BTC 按 HOUSE 的盘口价成交，两个合约各开平一次）、`admin`（管理后台：页面与安全头、密码 + TOTP 登录与 Cookie、CSRF、角色、冻结/解冻、交易对状态、强制撤单、开关、双人调账、合约状态与只减仓、强平监控、双人保险基金注资、报表、审计查询、退出与停用）；阶段 3：`contracts`（合约规格与 125 倍风险限额阶梯、标记价、资金费率与推送、两个合约的盘口）、`derivatives`（两个用户在 ETH-USDT-PERP 上各自与 HOUSE 开多、开空，平仓、转回、对账）、`funding`（常驻对冲仓位的每次资金费，状态文件在本机 `~/.cache/exchange-e2e/`，见 [derivatives.md](derivatives.md#测试服设置)）；所有订单都与 HOUSE 成交（用户决定 2026-10-02），脚本的价格从当时的盘口或标记价推出，不写死；`ops` 还检查 HOUSE 与参考行情的指标 | `task e2e`（约 15 分钟，需 ssh 到测试服、本机 Chrome、`.env` 的 `CAPTCHA_BYPASS_TOKEN`） |
| 故障注入 | `scripts/fault/*.sh`：Redpanda 停机（停一分钟以上，超过消费组会话超时：API 可用、恢复后事件补发且只处理一次、无新死信；批量消费者——两个引擎、行情的成交、分析——重新入组追上积压，市价单进引擎成交。2026-10-02 曾因批量消费者在轮询期间一直阻止再平衡而在 Redpanda 重建后全部停住，见 `internal/platform/kafka/batch.go`）、ClickHouse 停机（核心状态不受影响、恢复后补齐）、Redis 停机（登录态请求放行、发验证码与密码登录 503 拒绝、恢复后正常）、PostgreSQL 重启（连接池自愈、同一幂等键至多一笔划转、账本核对通过）；阶段 2 任务 12 新增：撮合引擎主备切换 `matching-failover`（起第二个实例作备、`kill -9` 主进程，备机接管租约、由快照与 WAL 重建订单簿，崩溃期间发出的市价单在接管后与 HOUSE 恰好成交一次、崩溃前挂的单还在且能撤、没有遗留冻结；Docker 把崩溃的实例拉起为新备机，最后优雅停掉第二个实例、第一个再次接管）、链节点不可用 `chain-outage`（主机防火墙丢弃 wallet-service 出网流量：扫描停在原处、服务仍就绪、分配地址与充提查询正常，恢复后追上链高）、参考行情中断 `reference-outage`（丢弃 market-data-service 出网流量：参考价数秒内过期、HOUSE 撤出 BTC-USDT 的流动性、平台行情照常，30 秒无数据断开重连，恢复后参考价新鲜、HOUSE 重新报价；合约在断流期间进入只减仓，演练结束时等标记价恢复后解除它造成的只减仓）；阶段 3 新增合约降级 `contract-degrade`（同样丢弃 market-data-service 出网流量：BTC-USDT-PERP 标记价报 degraded、合约进入只减仓、开仓单被拒；恢复后仍只减仓，`exchangectl derivatives resume` 后开仓单恢复）；阶段 4 新增托管方回调 `custody-callbacks`（2026-10-04 起在模拟网关的第二个商户 `UDUNMOCK` 与隐藏资产 TUSD 上，用地区 `AQ` 的账户，ADR-0017；模拟网关把回调延后两分钟：充值等到回调才入账、只入账一次，三次重放不变；wallet-service 停机时托管方退避重试，恢复后入账一次；网关停机时新地址返回 `WALLET_UNAVAILABLE`、已有地址照常、对账失败且 `wallet_custody_up` 为 0，恢复后对账短缺为 0。见 [custody.md](custody.md)）；平台币模拟市场停机 `market-sim-down`（停掉 market-sim：做市商挂单留在簿上，1 分钟无心跳后 ASTRA-USDT 与永续停牌；重启 30 秒后恢复交易、每侧 8 档以上，见 [market-sim.md](market-sim.md)），`reference-outage` 另查断流时 ASTRA-USDT 照常成交 | `task fault`（旧四项每项中断约半分钟；新三项各约三到四分钟，结束时恢复；防火墙规则带 `exchange-fault-<服务>` 注释，退出时删除） |

## CI（GitHub Actions）

`.github/workflows/ci.yml` 有三个任务：`go`、`web`、`deploy config`（只做质量门禁，不部署，ADR-0007）。

- `go` 任务比本机 `task ci` 多跑集成测试：CI 设了 `TEST_*`，`task ci` 没设，所以集成测试只在 CI 里失败的情况是有的。
  - 改迁移或表约束时，提交前至少跑相关包与 `./migrations/` 的集成测试。B6 删了充值地址的 0x 约束，却没改 `TestWalletSchema`，CI 因此连红了五次。
- 模块路径是 `github.com/skill/exchange`（2026-10-04 起不带 GitHub 账号）。`scripts/ci/module-path.sh` 在代码、配置与文档里见到别的 `github.com/<账号>/exchange` 就失败，网页地址不算；CI 的 `module path` 步骤与 `task ci` 的 `modpath:check` 都跑它。
  - 旧路径的残留有的不报错：depguard 规则什么也匹配不到，`-X` 什么也设不上。
  - 以后再改模块路径，`api/gen` 要用 `task proto` 重新生成，不能文本替换：描述符里的 go_package 前面存着长度。
- 集成测试的依赖由 `scripts/ci/services.sh` 起成容器（PostgreSQL、Redis、ClickHouse、Redpanda，与测试服同版本，每次都是空库）。
  - 拉镜像失败会退避重试最多六次，每次失败的原因写进注解。
  - ClickHouse 取自 `mirror.gcr.io`：Docker Hub 对匿名拉取限流，GitHub 自带的服务容器只在几秒内重试三次，曾让任务在 `Initialize containers` 失败。
- Go 版本是 1.27 的最新补丁版（`setup-go` 的 `1.27.x` + `check-latest`），与应用镜像 `golang:1.27-alpine` 一致。项目 2026-10-01 从 1.26 升到 1.27。
  - go.mod 的 `go 1.27.0` 只表示能构建本模块的最低版本。
  - govulncheck 按运行它的 Go 判断标准库漏洞。按 go.mod 装 `.0` 版会一直报标准库漏洞，所以 CI 取最新补丁版。
  - 换 Go 小版本时要一起改四处：go.mod（含 `tools/devmcp`）、CI 工作流 `.github/workflows/ci.yml`、`deploy/docker/Dockerfile` 的 `GO_IMAGE`、本机工具。本机工具指 golangci-lint 与 govulncheck，要用新 Go 重新 `go install`，否则它们按旧标准库分析。
  - 有漏洞但还没有修复版的依赖，govulncheck 也会失败。GO-2026-6443 就是这样：gRPC 的 1.84 线没有修复版，所以退到已修复的 v1.83.2。
- 运行日志要登录且有仓库权限才能看，注解不用。
  - `go test` 或 govulncheck 失败时，「name the failures」步骤把失败的测试、竞态、编译错误或漏洞编号写成一条注解。
  - 打开运行页面就能看到这条注解，接口是 `GET /repos/{owner}/{repo}/check-runs/{job_id}/annotations`。
- 本机复现 CI 的测试步骤：先 `ssh -f -N -L 19000:127.0.0.1:9000 exchange` 建隧道（本机连不上测试服 ClickHouse 的 9000 端口）。
  - 再把 `.env` 的 `TEST_CLICKHOUSE_ADDR` 设为 `127.0.0.1:19000`，然后跑 `task test:integration`（它从 `.env` 读 `TEST_*`，会覆盖命令行上设的值）。

## 约定

- 端到端脚本只依赖 bash 3.2、curl、jq（`web` 另需 Node 与 Chrome），共用 `scripts/e2e/lib/common.sh`；需要看服务器内部的用 `lib/remote.sh`（一条复用的 ssh 连接）。
- 每个脚本自己注册新用户（邮箱 `e2e-*@example.com` 走模拟邮件通道，手机号走模拟短信），不依赖历史数据，可反复运行（`funding` 例外：它有意保留一对跨运行的用户与仓位）；同一目标的验证码要间隔 60 秒，所以 `auth`、`identity` 各有一两次等待。
- 带测试绕过令牌的验证码请求不计入同 IP 每小时额度，整套端到端测试可以在一小时内反复运行。
- 脚本也能对本机开发栈跑：`BASE=http://localhost:8080 bash scripts/e2e/ledger.sh`（[local-dev.md](local-dev.md)）。
- 故障注入脚本在 EXIT 时总会把停掉的组件启动回来；不要与端到端测试同时运行。`task e2e`、`task fault` 与单独运行的演练脚本都经运维锁（`scripts/ops/lock.sh`，见 [server-deploy.md](server-deploy.md#日常更新)），与另一个会话的部署、完整端到端、演练轮流进行；单个端到端脚本不拿锁。
- 压测：`cmd/loadgen`（`users`、`orders`、`ws`，在服务器的 compose 网络里以容器运行，绕过 Cloudflare 与网关每 IP 配额），用法、结果与升级后的复测计划见 [../压测报告-阶段2.md](../压测报告-阶段2.md)；撮合订单簿基准 `go test -bench BenchmarkBook ./internal/matching/domain`。压测会产生大量订单与成交，不要与端到端测试同时运行，跑完核对账本（`exchangectl ledger reconcile`）。
