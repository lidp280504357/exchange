# 测试与故障注入

实施计划 §9.1 的测试层级在阶段 1 的落地方式；与验收标准的逐条对应见阶段验收报告。

| 层级 | 在哪里 | 怎么跑 |
|---|---|---|
| 单元测试 | 各包 `*_test.go`（状态机全部非法转移、金额与精度、幂等、错误码映射、令牌、限流、调度重试与熔断）；前端 Vitest（`web/packages/*/src`、`web/apps/*/src` 的 `*.test.ts(x)`） | `task test`、`task web:check`（都在 `task ci` 里） |
| 属性测试 | `internal/ledger/domain`：固定种子的随机分录序列下每资产零和、受限账户不为负 | 同上 |
| 契约测试 | Protobuf：`buf lint`、生成代码一致、`buf breaking`（CI 对比上一个提交，本机 `task proto:breaking` 对比 main）；OpenAPI：前端类型由契约生成且一致（`packages/core/src/api/gen`），浏览器冒烟测试把看到的每个 API 响应按契约校验（`web/e2e/contract.mjs`，Ajv/JSON Schema 2020-12）；gRPC 错误码跨服务保持（`grpcx` 测试） | `task ci`、`task e2e` |
| 集成测试 | 连测试服的 `exchange_test` 库、Redis DB 15、临时 topic 与 ClickHouse 库（`internal/platform/testenv`，未配置则跳过）：仓储、迁移与回滚、outbox/inbox、Kafka 重试/死信/重放、账本并发与对账、CLI | `task test:integration`（约 8 分钟） |
| 端到端 | `scripts/e2e/*.sh` 对已部署环境：`auth`（注册、令牌轮换与重放、会话、step-up）、`identity`（手机号注册登录、绑定、换绑）、`account`（资料、资格、冻结与令牌刷新、通知）、`gateway`（限流头、幂等重放、WebSocket）、`ledger`（欢迎资金、划转）、`market`、`web`（PC 站、手机站、后台三站的页面与 SPA 回退、缓存头与 gzip、设备分流与 `site_pref`、手机站 manifest、service worker 与离线页、`/h5/` 跳首页、后台安全头、API 参考页与 Storybook，最后用无头 Chrome 跑 PC 站与手机站的浏览器冒烟测试 `web/e2e/{pc,m}-smoke.mjs`，见 [web.md](web.md#性能与检查)）、`ops`（trace ID、健康与指标、账本与 ClickHouse 核对为 0）、`pii`（ClickHouse 与日志里没有明文邮箱与验证码）、`risk`（新设备登录记分、同设备注册爆发进入风控审核）、`trading`（下单冻结、校验、余额不足被拒、client_order_id 重试、撤单请求）、`matching`（两个用户实际成交：挂单与吃单、IOC、POST_ONLY 与自成交被拒、市价单、限价买单差额、撤单与解冻、结算后的余额与流水）、`marketdata`（行情 REST 的校验；参考行情：交易对映射、币安 ticker、概览、K 线翻页与 `tickers` 频道；一笔成交在 WebSocket 深度、成交、ticker、K 线与 orders/fills 频道上的推送，以及 REST 成交、ticker、K 线、深度）、`totp`、`deposit`/`withdraw`（Sepolia 真实转账）、`admin`（管理后台：页面与安全头、密码 + TOTP 登录与 Cookie、CSRF、角色、冻结/解冻、交易对状态、强制撤单、开关、双人调账、合约状态与只减仓、强平监控、双人保险基金注资、报表、审计查询、退出与停用）；阶段 3：`contracts`（合约规格、标记价、资金费率与推送）、`derivatives`（两个用户在 ETH-USDT-PERP 上开仓、平仓、转回、对账）、`funding`（常驻对冲仓位的每次资金费，状态文件在本机 `~/.cache/exchange-e2e/`，见 [derivatives.md](derivatives.md#测试服设置)）；下单与成交类脚本在没有做市的 ETH-BTC 上进行（BTC-USDT 有做市报价），`ops` 还检查做市与参考行情的指标 | `task e2e`（约 15 分钟，需 ssh 到测试服、本机 Chrome、`.env` 的 `CAPTCHA_BYPASS_TOKEN`） |
| 故障注入 | `scripts/fault/*.sh`：Redpanda 停机（API 可用、恢复后事件补发且只处理一次、无新死信）、ClickHouse 停机（核心状态不受影响、恢复后补齐）、Redis 停机（登录态请求放行、发验证码与密码登录 503 拒绝、恢复后正常）、PostgreSQL 重启（连接池自愈、同一幂等键至多一笔划转、账本核对通过）；阶段 2 任务 12 新增：撮合引擎主备切换 `matching-failover`（起第二个实例作备、`kill -9` 主进程，备机接管租约、由快照与 WAL 重建订单簿，崩溃前挂的单在接管后恰好成交一次、账本结清；Docker 把崩溃的实例拉起为新备机，最后优雅停掉第二个实例、第一个再次接管）、链节点不可用 `chain-outage`（主机防火墙丢弃 wallet-service 出网流量：扫描停在原处、服务仍就绪、分配地址与充提查询正常，恢复后追上链高）、参考行情中断 `reference-outage`（丢弃 market-data-service 出网流量：参考价数秒内过期、做市撤掉 BTC-USDT 报价、平台行情照常，30 秒无数据断开重连，恢复后参考价新鲜、做市重新报价）；阶段 3 新增合约降级 `contract-degrade`（同样丢弃 market-data-service 出网流量：BTC-USDT-PERP 标记价报 degraded、合约进入只减仓、开仓单被拒；恢复后仍只减仓，`exchangectl derivatives resume` 后开仓单恢复） | `task fault`（旧四项每项中断约半分钟；新三项各约三到四分钟，结束时恢复；防火墙规则带 `exchange-fault-<服务>` 注释，退出时删除） |

## 约定

- 端到端脚本只依赖 bash 3.2、curl、jq（`web` 另需 Node 与 Chrome），共用 `scripts/e2e/lib/common.sh`；需要看服务器内部的用 `lib/remote.sh`（一条复用的 ssh 连接）。
- 每个脚本自己注册新用户（邮箱 `e2e-*@example.com` 走模拟邮件通道，手机号走模拟短信），不依赖历史数据，可反复运行（`funding` 例外：它有意保留一对跨运行的用户与仓位）；同一目标的验证码要间隔 60 秒，所以 `auth`、`identity` 各有一两次等待。
- 带测试绕过令牌的验证码请求不计入同 IP 每小时额度，整套端到端测试可以在一小时内反复运行。
- 脚本也能对本机开发栈跑：`BASE=http://localhost:8080 bash scripts/e2e/ledger.sh`（[local-dev.md](local-dev.md)）。
- 故障注入脚本在 EXIT 时总会把停掉的组件启动回来；不要与端到端测试同时运行。
- 压测：`cmd/loadgen`（`users`、`orders`、`ws`，在服务器的 compose 网络里以容器运行，绕过 Cloudflare 与网关每 IP 配额），用法、结果与升级后的复测计划见 [../压测报告-阶段2.md](../压测报告-阶段2.md)；撮合订单簿基准 `go test -bench BenchmarkBook ./internal/matching/domain`。压测会产生大量订单与成交，不要与端到端测试同时运行，跑完核对账本（`exchangectl ledger reconcile`）。
