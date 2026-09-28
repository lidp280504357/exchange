# 测试与故障注入

实施计划 §9.1 的测试层级在阶段 1 的落地方式；与验收标准的逐条对应见阶段验收报告。

| 层级 | 在哪里 | 怎么跑 |
|---|---|---|
| 单元测试 | 各包 `*_test.go`（状态机全部非法转移、金额与精度、幂等、错误码映射、令牌、限流、调度重试与熔断）；H5 的 Vitest（`web/h5/src/**/*.test.ts`） | `task test`、`task web:check`（都在 `task ci` 里） |
| 属性测试 | `internal/ledger/domain`：固定种子的随机分录序列下每资产零和、受限账户不为负 | 同上 |
| 契约测试 | Protobuf：`buf lint`、生成代码一致、`buf breaking`（CI 对比上一个提交，本机 `task proto:breaking` 对比 main）；OpenAPI：H5 类型由契约生成且一致，浏览器冒烟测试把看到的每个 API 响应按契约校验（`web/h5/e2e/contract.mjs`，Ajv/JSON Schema 2020-12）；gRPC 错误码跨服务保持（`grpcx` 测试） | `task ci`、`task e2e` |
| 集成测试 | 连测试服的 `exchange_test` 库、Redis DB 15、临时 topic 与 ClickHouse 库（`internal/platform/testenv`，未配置则跳过）：仓储、迁移与回滚、outbox/inbox、Kafka 重试/死信/重放、账本并发与对账、CLI | `task test:integration`（约 8 分钟） |
| 端到端 | `scripts/e2e/*.sh` 对已部署环境：`auth`（注册、令牌轮换与重放、会话、step-up）、`identity`（手机号注册登录、绑定、换绑）、`account`（资料、资格、冻结与令牌刷新、通知）、`gateway`（限流头、幂等重放、WebSocket）、`ledger`（欢迎资金、划转）、`market`、`h5`（静态站点、API 参考页与无头 Chrome 的手机/桌面流程与表单注册）、`ops`（trace ID、健康与指标、账本与 ClickHouse 核对为 0）、`pii`（ClickHouse 与日志里没有明文邮箱与验证码）、`risk`（新设备登录记分、同设备注册爆发进入风控审核）、`trading`（下单冻结、校验、余额不足被拒、client_order_id 重试、撤单请求） | `task e2e`（约 12 分钟，需 ssh 到测试服、本机 Chrome、`.env` 的 `CAPTCHA_BYPASS_TOKEN`） |
| 故障注入 | `scripts/fault/*.sh`：Redpanda 停机（API 可用、恢复后事件补发且只处理一次、无新死信）、ClickHouse 停机（核心状态不受影响、恢复后补齐）、Redis 停机（登录态请求放行、发验证码与密码登录 503 拒绝、恢复后正常）、PostgreSQL 重启（连接池自愈、同一幂等键至多一笔划转、账本核对通过） | `task fault`（每项中断测试环境约半分钟，结束时恢复） |

## 约定

- 端到端脚本只依赖 bash 3.2、curl、jq（`h5` 另需 Node 与 Chrome），共用 `scripts/e2e/lib/common.sh`；需要看服务器内部的用 `lib/remote.sh`（一条复用的 ssh 连接）。
- 每个脚本自己注册新用户（邮箱 `e2e-*@example.com` 走模拟邮件通道，手机号走模拟短信），不依赖历史数据，可反复运行；同一目标的验证码要间隔 60 秒，所以 `auth`、`identity` 各有一两次等待。
- 带测试绕过令牌的验证码请求不计入同 IP 每小时额度，整套端到端测试可以在一小时内反复运行。
- 脚本也能对本机开发栈跑：`BASE=http://localhost:8080 bash scripts/e2e/ledger.sh`（[local-dev.md](local-dev.md)）。
- 故障注入脚本在 EXIT 时总会把停掉的组件启动回来；不要与端到端测试同时运行。
