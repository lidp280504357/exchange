# 测试服部署流程（本机不装 Docker）

用户决定（2026-09-28）：本机只写代码和调试；代码推送到 GitHub 后，在测试服拉取、构建、更新。

## 服务器上已就绪

2026-09-30 起是新服务器：AWS 4 vCPU / 7.8 GiB（非突发型），Ubuntu 26.04，48 GB 磁盘，公网 IP `3.107.113.199`（`ssh exchange`）；旧的 t2.medium（`16.176.196.40`）2026-09-29 晚整机无响应后弃用，数据没有迁移。

- git 2.53、Go 1.27.1（`/usr/local/go`，2026-10-01 由 1.26.4 升级；`/etc/profile.d/go.sh` 已加 PATH，重新登录生效；应用在 Docker 里用 `golang:1.27-alpine` 构建，不用它）、2 GB swap（swappiness 10）
- 部署密钥 `~/.ssh/exchange_deploy_ed25519`，`~/.ssh/config` 已为 `github.com` 指定该密钥（目前按账号级 SSH key 添加；改成仓库只读 Deploy key 更安全）
- 源码目录 `/opt/exchange/src`（仓库克隆位置），基础设施目录 `/opt/exchange/infra`
- Docker 29.8 + Compose v5.5（Docker 官方 apt 源），应用镜像用多阶段 Dockerfile 在服务器上构建，不需要本机 Docker
- cron：`backup/pg-backup.sh` 每日 03:30 UTC，`nginx/update-cloudflare-ips.sh` 每周一 04:17

## 从零部署一台新服务器（2026-09-30 实际步骤）

1. 系统：装 Docker 官方源的 `docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin`、Go、swap；建 `/opt/exchange/{src,infra,backups/postgres}`（属主 ubuntu）。
2. 部署密钥：`ssh-keygen -t ed25519 -f ~/.ssh/exchange_deploy_ed25519`，`~/.ssh/config` 为 github.com 指定它，公钥交给用户加到 GitHub；`ssh -T git@github.com` 通过后 `git clone` 到 `/opt/exchange/src`。
3. 密钥文件（在本机生成、`remote_put_file` 上传，不打印）：`infra/.env`（`PUBLIC_IP` 与 PostgreSQL、Redis、ClickHouse 的随机密码）、`infra/apps.env`（600；Turnstile、Resend、Alchemy 沿用本机 `.env`，`OTP_HMAC_KEY`、`JWT_SIGNING_KEY`、`TOTP_SECRET_KEY` 新生成，`CAPTCHA_BYPASS_TOKEN` 与本机 `.env` 相同，端到端脚本要用）、`admin/admin.env`（目录 700 属主 root）、`signer/signer.env`（目录 700 属主 uid 10001；Ubuntu 26.04 的 `install -o 10001` 不认数字 uid，用 `chown`）、`nginx/ssl/origin.{pem,key}`（Cloudflare 源站证书，本机 `deploy/compose/nginx/ssl/` 有一份）。本机 `.env` 的连接串与 `TEST_*` 同步改成新地址与新密码。
4. 基础设施：把 `deploy/compose/` 同步到 `infra/`，`docker compose up -d --wait postgres redis redpanda clickhouse`；`bash redpanda/topics.sh`；`CREATE DATABASE exchange_test`（集成测试用）；`bash nginx/update-cloudflare-ips.sh`；装 cron。
5. 先 `docker compose -f docker-compose.yml -f docker-compose.apps.yml build` 出镜像，再初始化 keystore（[wallet.md](wallet.md)），把打印的 xpub 写进 `apps.env` 的 `WALLET_XPUB`——signer 没有 keystore 起不来，`server-update.sh` 的 `--wait` 会一直等。
6. `bash /opt/exchange/src/deploy/server-update.sh`（后台跑、看日志，全量构建约 10 分钟）。
7. 部署后的一次性设置：
   - 功能开关：`ledger.welcome_credit`、`account.transfer`、`ledger.manual_adjustment`、`auth.sms`、`market.reference_feed`、`wallet.withdraw`、`derivatives.trading` 打开，`risk.enforce --allow-regions AQ`，`market.maker --allow-symbols BTC-USDT,BTC-USDT-PERP`（[feature-flags.md](feature-flags.md)）。
   - 交易对：参考数据文件新建的交易对是 `PREPARE`，`exchangectl instruments pair-status BTC-USDT --to TRADING`，ETH-BTC 同样（端到端在它上面成交）；ETH-USDT 保持 PREPARE。
   - 合约：参考行情打开前算不出标记价，合约 10 秒后自动进入只减仓（`INDEX_SOURCES`）；打开参考行情、确认标记价有了之后 `exchangectl derivatives resume <合约>` 解除。
   - 托管钱包（[custody.md](custody.md)）：`apps.env` 加 `UDUN_GATEWAY_URL=http://udun-mock:8097`、`UDUN_CALLBACK_URL=http://api-gateway:8080/v1/wallet/callbacks/udun`、随机的 `UDUN_MERCHANT_ID` 与 `UDUN_API_KEY`（`openssl rand -hex 16`、`openssl rand -hex 32`，不打印），模拟网关与 wallet-service 共用。
   - 做市账户：注册一个 `@example.com` 用户，`exchangectl ledger adjust` 注入 1 BTC 与 100000 USDT，`apps.env` 加 `MARKET_MAKER_USER_ID`、`MARKET_MAKER_USER_IDS` 后重建 spot-trading-service、derivatives-service、market-maker，再转 20000 USDT 到它的合约账户（[market-maker.md](market-maker.md)）。
   - 保险基金：`exchangectl ledger insurance-fund --amount 1000000 --key insurance-seed-1`。
   - 热钱包：用端到端发送方转一些 Sepolia ETH 到 signer 日志里的 `hot_wallet` 地址，`exchangectl wallet fund --tx <hash>` 记到 GAS_SUPPLY。
8. 验证：`https://astras.vip/v1/time`、全部容器 healthy、`task test:integration`、`task e2e`。

## 日常更新

```bash
ssh exchange
bash /opt/exchange/src/deploy/server-update.sh          # 更新到 origin/main
bash /opt/exchange/src/deploy/server-update.sh 305e2a7  # 回滚/切换到指定提交
```

脚本依次执行以下步骤：

1. 拉代码并重置到目标版本。
2. 把 `deploy/compose/` 同步到 `/opt/exchange/infra`。不碰 `.env`、`apps.env`、证书、Cloudflare IP 列表、`nginx/html/`、`nginx/admin/`、`nginx/sites/`、`udun-mock/`（托管钱包模拟网关的状态，属主 uid 10001，脚本在这里创建）；`signer/`、`admin/` 两个密钥目录不在仓库里，也不受影响。
3. 幂等核对 Redpanda topic。
4. 删掉 6 小时内没用过的构建缓存，确认根分区至少还有 8 GB，不够就停止部署；然后 `docker compose build` 构建全部镜像。
5. 先起 instrument-service，按 `deploy/instruments/test.json` 幂等同步参考数据（[instruments.md](instruments.md)），再 `up -d` 其余服务。其余服务启动时就要读交易对与参考行情映射，所以参考数据必须先到。
6. 清理悬空镜像，再删一次 6 小时内没用过的构建缓存。
7. 校验并热加载 nginx 配置。
8. 在 node 容器里对 `web/` 装一次依赖，构建 PC 站、手机站、管理后台与 Storybook，发布到 `nginx/sites/*`（[web.md](web.md)）。

## 首次克隆（部署密钥加到 GitHub 之后）

```bash
ssh exchange
ssh -T git@github.com            # 预期输出：Hi lidp280504357/exchange! You've successfully authenticated ...
git clone git@github.com:lidp280504357/exchange.git /opt/exchange/src
bash /opt/exchange/src/deploy/server-update.sh
```

## 应用环境变量

基础设施凭据在 `/opt/exchange/infra/.env`。应用服务的变量（Turnstile、Resend、Alchemy、JWT 密钥等）阶段 1 起放在 `/opt/exchange/infra/apps.env`（权限 600，不入库），由 `docker-compose.apps.yml` 通过 `env_file` 注入；本地开发用仓库根目录的 `.env`。只给单个服务的密钥各有目录：signer 的 `signer/signer.env`（keystore 口令，[wallet.md](wallet.md)）、admin-service 的 `admin/admin.env`（`ADMIN_SECRET_KEY`，[admin.md](admin.md)），都是 `required: true`，缺失时部署失败。

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
| derivatives-engine（合约撮合分片，同一二进制 `MATCHING_SHARD=derivatives`） | — | — | 9096 |
| derivatives-service | 8095（网关转发 `/v1/derivatives/*`） | 9195（只给 ledger-service） | 9095 |
| market-data-service | 8090（网关转发 `/v1/market/tickers`、`/v1/market/{symbol}/*`） | — | 9090 |
| market-maker | — | — | 9091 |
| wallet-service | 8092（网关转发 `/v1/wallet/*`） | — | 9092 |
| signer | — | 9193（只给 wallet-service） | 9093 |
| risk-service | — | 9186 | 9086 |
| analytics-consumer | — | — | 9087 |
| admin-service | 8093（nginx 转发 `/admin/v1/`，不经网关） | — | 9094 |
| udun-mock（测试服的托管钱包模拟网关，只在内网，见 custody.md） | 8097（wallet-service 调用） | — | 9097 |

compose 健康检查请求运维端口的 `/readyz`：启动完成且依赖可用才返回 200，收到 SIGTERM 后立即变为 503（draining）。部署验证：

```bash
task deploy:status                         # 全部容器 healthy
curl -s https://astras.vip/v1/time         # 网关经 Cloudflare 与 nginx 可达
ssh exchange 'sudo docker exec exchange-infra-api-gateway-1 wget -qO- http://127.0.0.1:9080/readyz'
```

## 小机器调优（2026-09-28 资源评估）

以下调优是在旧测试服（t2.medium，2 vCPU / 3.8 GiB 突发型）上做的；2026-09-30 起的 c5a.xlarge（4 vCPU / 7.8 GiB）沿用了这些设置，可以按需放宽（例如 ClickHouse 的内存上限）。评估与数据见 [阶段 1 验收报告](../阶段1验收报告.md) §6：

- ClickHouse：`deploy/compose/clickhouse/config.d/small-server.xml` 去掉诊断用的系统日志表（trace_log、metric_log 等，保留 query_log、part_log），服务日志 warning 级、100 MB × 3，内存上限为物理内存 30%。改了这个文件，部署时 compose 会重建 ClickHouse 容器（约半分钟，analytics-consumer 自动重试）。
- Redpanda：`topics.sh` 把 `segment_fallocation_step` 设为 4 MiB（默认 32 MiB，每个分区的活动段都会预分配）。
- 构建缓存：每次部署前后各删一次 6 小时内没用过的（`docker builder prune -a --filter until=6h`）。
  - Docker 29 上原来的 `--keep-storage 3gb` 什么也不删：不带 `-a` 只删悬空记录，`--keep-storage`/`--max-used-space` 又不计入共享的部分。
  - 2026-10-01 缓存因此涨到 21 GB，一次构建写满了磁盘。

## 磁盘写满之后（2026-10-01 实际处理）

磁盘写满会让 Docker 丢掉运行中容器的网络端点。当时丢的是 redpanda：它自己的健康检查在容器内，仍显示 healthy，但其他服务解析不到它。

- 症状：
  - 应用容器大多 unhealthy。
  - 就绪检查（`/readyz`）报 `kafka: ... lookup redpanda on 127.0.0.11:53: server misbehaving`，PostgreSQL、Redis 正常。
  - `docker inspect exchange-infra-redpanda-1` 的 `NetworkSettings.Networks.*.IPAddress` 为空。
  - 合约因标记价中断进入只减仓（`MARK_PRICE_STALE`）。
- 处理：
  1. 释放空间：`sudo docker builder prune -a -f`，当时回收 14.8 GB。
  2. 重启丢了端点的容器：`docker compose restart redpanda`。应用服务自动重连，outbox 里积压的事件随后发出。
  3. 确认标记价恢复（`/v1/market/<合约>/mark-price` 的 `updated_at` 在走、`degraded` 为 false）后，解除只减仓：`exchangectl derivatives resume <合约>`，用 `EXCHANGECTL_ACTOR` 记下解除人。
- 找出哪个容器不在网络上：比较 `docker network inspect exchange-infra_exchange` 列出的容器与 `docker ps` 的差集。

## 本机调试

本机开发栈 `task dev`、单个服务 `task run -- <service>`：服务在本机运行，连测试服基础设施里单独的 dev 命名空间（库 `exchange_dev`、Redis DB 1、Kafka 前缀 `dev.`），不碰测试环境的数据，见 [local-dev.md](local-dev.md)。`task web:dev`（PC 站，`-- m`、`-- admin` 为手机站、后台）默认代理到测试服，`API_ORIGIN=http://localhost:8080` 改连本机网关。安全组已放行本机 IP，本机不需要 Docker。Ctrl-C 触发优雅退出。
