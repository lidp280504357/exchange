# 测试服部署流程（本机不装 Docker）

用户决定（2026-09-28）：本机只写代码和调试；代码推送到 GitHub 后，在测试服拉取、构建、更新。

## 服务器上已就绪

- git 2.53、Go 1.26.4（`/usr/local/go`，`/etc/profile.d/go.sh` 已加 PATH，重新登录生效）
- 只读部署密钥 `~/.ssh/exchange_deploy_ed25519`，`~/.ssh/config` 已为 `github.com` 指定该密钥；公钥需加到仓库 Deploy keys
- 源码目录 `/opt/exchange/src`（仓库克隆位置），基础设施目录 `/opt/exchange/infra`
- Docker 29 + Compose 2.40，应用镜像用多阶段 Dockerfile 在服务器上构建，不需要本机 Docker

## 日常更新

```bash
ssh exchange
bash /opt/exchange/src/deploy/server-update.sh          # 更新到 origin/main
bash /opt/exchange/src/deploy/server-update.sh 305e2a7  # 回滚/切换到指定提交
```

脚本依次：拉代码并重置到目标版本；把 `deploy/compose/` 同步到 `/opt/exchange/infra`（不碰 `.env`、`apps.env`、证书、Cloudflare IP 列表、`nginx/html/`）；幂等核对 Redpanda topic；`docker compose up -d --build` 构建并更新容器、清理悬空镜像并把构建缓存压到 3 GB；校验并热加载 nginx 配置；按 `deploy/instruments/test.json` 幂等同步参考数据（[instruments.md](instruments.md)）；在 node 容器里构建 H5 并发布到 nginx 静态目录（[h5.md](h5.md)）。

## 首次克隆（部署密钥加到 GitHub 之后）

```bash
ssh exchange
ssh -T git@github.com            # 预期输出：Hi lidp280504357/exchange! You've successfully authenticated ...
git clone git@github.com:lidp280504357/exchange.git /opt/exchange/src
bash /opt/exchange/src/deploy/server-update.sh
```

## 应用环境变量

基础设施凭据在 `/opt/exchange/infra/.env`。应用服务的变量（Turnstile、Resend、Alchemy、JWT 密钥等）阶段 1 起放在 `/opt/exchange/infra/apps.env`（权限 600，不入库），由 `docker-compose.apps.yml` 通过 `env_file` 注入；本地开发用仓库根目录的 `.env`。

服务配置由 `internal/platform/config` 加载：代码默认值 < `.env`（仅本地；进程环境变量 `APP_ENV` 非 local 时不读取）< 进程环境变量。容器里只有 `apps.env` 注入的环境变量生效，镜像构建时不要把 `.env` 拷进去。

编写 `docker-compose.apps.yml` 时，每个应用服务的 `stop_grace_period` 要大于 `SHUTDOWN_TIMEOUT`（默认 10s），建议 15s；否则 Docker 会在优雅退出完成前发 SIGKILL，在途请求与清理会被打断。

## 应用服务与端口

`deploy/docker/Dockerfile` 一次编译 `cmd/` 下全部服务，得到同一个镜像 `exchange-app`；`deploy/compose/docker-compose.apps.yml` 为每个服务起一个容器（`command` 选择二进制），与基础设施同在 `exchange` 网络。`server-update.sh` 把当前提交号作为 `APP_VERSION` 传入构建，服务启动日志与 `/metrics` 的 `exchange_build_info` 都带这个版本号。

端口约定（容器内与本机 `task run` 相同；只有 nginx 的 80/443 对外）：

| 服务 | REST | gRPC | 运维（`/healthz` `/readyz` `/metrics` `/debug/pprof`） |
|---|---|---|---|
| api-gateway | 8080（nginx 转发 `/v1/`） | — | 9080 |
| auth-service | 8081 | 9181 | 9081 |
| user-service | 8082 | 9182 | 9082 |
| notification-service | 8083 | 9183 | 9083 |
| instrument-service | 8084 | 9184 | 9084 |
| ledger-service | 8085 | 9185 | 9085 |
| spot-trading-service | 8088（网关转发 `/v1/orders`） | — | 9088 |
| matching-engine | — | — | 9089 |
| market-data-service | 8090（网关转发 `/v1/market/tickers`、`/v1/market/{symbol}/*`） | — | 9090 |
| market-maker | — | — | 9091 |
| risk-service | — | 9186 | 9086 |
| analytics-consumer | — | — | 9087 |

compose 健康检查请求运维端口的 `/readyz`：启动完成且依赖可用才返回 200，收到 SIGTERM 后立即变为 503（draining）。部署验证：

```bash
task deploy:status                         # 全部容器 healthy
curl -s https://astras.vip/v1/time         # 网关经 Cloudflare 与 nginx 可达
ssh exchange 'sudo docker exec exchange-infra-api-gateway-1 wget -qO- http://127.0.0.1:9080/readyz'
```

## 小机器调优（2026-09-28 资源评估）

测试服是 2 vCPU / 3.8 GiB 的突发型实例，基础设施与应用共用。评估与数据见 [阶段 1 验收报告](../阶段1验收报告.md) §6：

- ClickHouse：`deploy/compose/clickhouse/config.d/small-server.xml` 去掉诊断用的系统日志表（trace_log、metric_log 等，保留 query_log、part_log），服务日志 warning 级、100 MB × 3，内存上限为物理内存 30%。改了这个文件，部署时 compose 会重建 ClickHouse 容器（约半分钟，analytics-consumer 自动重试）。
- Redpanda：`topics.sh` 把 `segment_fallocation_step` 设为 4 MiB（默认 32 MiB，每个分区的活动段都会预分配）。
- 构建缓存：每次部署后压到 3 GB。

## 本机调试

本机开发栈 `task dev`、单个服务 `task run -- <service>`：服务在本机运行，连测试服基础设施里单独的 dev 命名空间（库 `exchange_dev`、Redis DB 1、Kafka 前缀 `dev.`），不碰测试环境的数据，见 [local-dev.md](local-dev.md)。`task web:dev` 的 H5 默认代理到测试服，`API_ORIGIN=http://localhost:8080` 改连本机网关。安全组已放行本机 IP，本机不需要 Docker。Ctrl-C 触发优雅退出。
