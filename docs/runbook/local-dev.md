# 本机开发栈

实施计划 §10 任务 18 的"本地启动脚本"。本机不装 Docker（CLAUDE.md），服务在本机编译运行，基础设施用测试服的；为了不和已部署的测试环境混在一起，本机栈使用单独的 **dev 命名空间**：

| 组件 | 测试环境（容器） | 本机栈 |
|---|---|---|
| PostgreSQL | 库 `exchange` | 库 `exchange_dev`（各服务启动时自己跑迁移） |
| Redis | DB 0 | DB 1 |
| ClickHouse | 库 `exchange` | 库 `exchange_dev` |
| Redpanda | topic `auth.events`… 与消费组 `ledger-service`…（`topics.sh` 建） | 前缀 `dev.`：`dev.auth.events`…、`dev.ledger-service`…（Schema Registry 主题随 topic 名分开）；topic 由服务启动时自建、每个 1 个分区（B205，见下） |

Kafka 前缀由配置 `KAFKA_NAMESPACE` 实现（`internal/platform/kafka`）：生产者、消费者、Tail、死信工具在连接 broker 时统一加前缀，业务代码与 outbox 里仍是逻辑名（`auth.events`），ClickHouse 里记录的也是逻辑名。生产与测试环境不设置它。

## 用法

需要：根目录 `.env`（基础设施地址与凭据，`.env.example` 有清单）、`ssh exchange` 可用、Go、curl。

```bash
task dev                                    # = scripts/dev.sh：全部 8 个服务，Ctrl-C 全部停止
API_ORIGIN=http://localhost:8080 task web:dev   # 另开终端：PC 站连本机网关（-- m 手机站）
```

`scripts/dev.sh` 依次：

1. 读 `.env`，把 `POSTGRES_DSN`、`REDIS_URL`、`CLICKHOUSE_DB` 改到 dev 命名空间，设 `KAFKA_NAMESPACE=dev.`（进程环境变量优先于 `.env`）。
2. 经 ssh 在测试服幂等建库 `exchange_dev`（PostgreSQL、ClickHouse）。Kafka 的 `dev.*` topic 不在这一步建，而是每个服务启动时由 `internal/platform/kafka`（`local.go`）建它用到的那几个：消费者的 topic 连同 `.retry`、`.dlq`，Tail 与 ReadToEnd 读的 topic，生产者第一次发往的 topic——每个 **1 个分区**、保留 1 天、每分区至多 256 MiB；已存在的跳过，分区数不同只在日志里提示（`a local topic has another partition count`）、不改。所以 `task run -- auth-service` 只建 auth-service 用到的，`task dev` 建全部服务用到的（每个 1 个分区，合计几十个）。哪个服务用哪些 topic 只有代码一处（各服务的消费组与发布），不另抄一份；只有设了 `KAFKA_NAMESPACE` 才会这样建，测试与生产环境不设，它们的 topic 仍只由 `deploy/redpanda/topics.sh` 建，`topics.sh` 带 `KAFKA_NAMESPACE` 运行会拒绝。
   原因（B205，用户 2026-10-11 决定"改小"）：测试服的 Redpanda 限 2 GB 内存（`--memory 2G`，不动），按内存限分区总数（`topic_memory_per_partition` 4 MiB）；以前这一步照 `topics.sh` 的分区数建一整套 `dev.` topic，和正式命名空间加在一起超限，答 `INVALID_PARTITIONS`（再早一次是打开文件数上限，审查 C67 ①，compose 已放宽到 65536，C71）。正式命名空间的 topic 与分区数都不变。
   `dev.*` topic 不会被任何脚本删除，要删只手动：先确认没有数据（`rpk topic describe <topic> -p` 的高水位都是 0），再 `rpk topic delete -r '^dev\..*'`；以前按旧方式建的多分区 `dev.*` topic 留着也能用，只是日志会提示分区数，删掉后下次启动按 1 个分区重建。
3. `go build` 到 `.dev/bin/`（已被 git 忽略）。命名空间是这次新建的，就打开测试环境也打开的开关：`ledger.welcome_credit`、`account.transfer`、`ledger.manual_adjustment`、`auth.sms`。
4. 同时启动全部服务（gRPC 按需连接、网关会重试取签名公钥，互不依赖启动顺序），逐个等运维端口的 `/readyz`；instrument-service 就绪后同步参考数据 `deploy/instruments/test.json`。
5. 打印入口后守护：任一服务退出则打印其日志末尾并停止全部。日志在 `.dev/logs/<服务>.log`（本机默认文本格式，`LOG_FORMAT=json` 可改）。

耗时：本机到测试服往返约 380 ms。首次（建 topic、跑全部迁移、设开关）约 5 分钟，之后每次约 1 分钟（编译与连接），服务就绪后每个请求仍明显比测试环境慢（查询余额约 2.5 秒）。

端口与容器里相同（[server-deploy.md](server-deploy.md)），网关 `http://localhost:8080`。

### 只改一个服务时

```bash
DEV_SKIP=auth-service task dev     # 终端 1：除 auth-service 外全部启动（多个用逗号分隔）
task run -- auth-service           # 终端 2：= scripts/dev.sh run auth-service，前台 go run，改完 Ctrl-C 重跑
```

### 运维命令

```bash
scripts/dev.sh ctl flags list                       # exchangectl 指向 dev 命名空间
scripts/dev.sh ctl ledger balances <user_id>
scripts/dev.sh ctl dlq list user.events
```

## 注意

- dev 命名空间的功能开关与测试环境各自独立：新建时打开上面四个，之后的改动只影响本机栈（`scripts/dev.sh ctl flags set ...`）；其余开关默认关闭（ADR-0005）。
- 本机栈可以跑端到端脚本：`BASE=http://localhost:8080 bash scripts/e2e/ledger.sh`（验证码从本机网关的开发收件箱读取）。
- 验证码：`example.com` 等测试域名走模拟通道，经本机网关 `GET /v1/dev/messages?target=...` 查看；其他邮箱会经 Resend 真实发送（与测试环境相同）。
- 人机验证：本机 H5 仍加载 Turnstile；直接调接口时用 `.env` 的 `CAPTCHA_BYPASS_TOKEN`。
- `OTP_HMAC_KEY`、`JWT_SIGNING_KEY` 在 `.env` 里留空时每次启动随机生成，重启后已登录会话与未用的验证码失效。
- 多人或多台机器同时跑本机栈会共用同一个 dev 命名空间（同一个库和消费组），此时各自改 `scripts/dev.sh` 里的 `DEV_DB`、`NS`。
- `task run` 不再直连测试环境的 `exchange` 库；需要看测试环境数据用 MCP `exchange-dev` 或 `exchangectl`（在服务容器里）。

## 清理

```bash
ssh exchange 'cd /opt/exchange/infra && set -a && . ./.env && set +a && sudo docker compose exec -T postgres dropdb -U "$POSTGRES_USER" exchange_dev'
```

ClickHouse 用 `DROP DATABASE exchange_dev`，Redis `SELECT 1` 后 `FLUSHDB`，topic 用 `rpk topic delete -r '^dev\..*'`；下次 `task dev` 会重新创建。
