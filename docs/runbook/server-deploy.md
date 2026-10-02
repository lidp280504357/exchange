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
   - 币种图标：`bash deploy/instruments/fetch-logos.sh upload /opt/exchange/src/deploy/instruments/logos` 给全部资产装上仓库里的默认图标（88 个，来源见同目录 `SOURCES.md`，不需要外网；`all` 改为从网上重新抓取再上传）。已有图标的资产（后台上传的）跳过，`FORCE=1` 才覆盖；平台币 ASTRA 用 `scripts/ops/astra.sh profile`。
8. 验证：`https://astras.vip/v1/time`、全部容器 healthy、`task test:integration`、`task e2e`。

## 日常更新

```bash
task deploy                  # 本机：经运维锁更新到 origin/main
task deploy -- 305e2a7       # 回滚/切换到指定提交
ssh exchange
bash /opt/exchange/src/deploy/server-update.sh          # 在服务器上直接跑：脚本自己拿运维锁
```

脚本整个读进内存后才执行（`main` 函数），拉取新提交时跑的仍是旧版本；拉取改了脚本本身时，它 exec 新版本重来一遍（日志 `== 部署脚本有更新，改跑新版本`，运维锁随 fd 9 带过去），新加的步骤当次生效。这条逻辑是 2026-10-02 加的：在它之前的版本上部署，脚本里新加的步骤（例如生成新的密钥文件）要到下一次部署才跑，需要的文件得先在服务器上手工准备（530ed59 的 `sim/admin.env` 就是这样补的）。

**镜像在 GitHub Actions 里构建**（2026-10-03 起，用户选私有镜像加服务器只读令牌）：每次推送 main，`.github/workflows/image.yml` 构建 `exchange-app` 并推到 `ghcr.io/lidp280504357/exchange-app:<完整提交号>`（另有 `:main`，保留最近 10 个版本）；部署脚本登录过 ghcr.io 时拉这个镜像，没登录、没等到或拉取失败才在服务器上构建，所以令牌没放好之前一切照旧。本地已有这个版本就直接用；只有 20 分钟内的提交（刚推送，Actions 可能还在构建）才最多等 10 分钟，回滚到旧提交、版本已被清理或令牌被拒都立刻改在服务器构建。拉下来打成 `exchange-app:latest` 后去掉 ghcr 的标签，部署后还会删掉两天没用的镜像，不然每次部署留下一整份（约 600 MB）。

部署只等站点离不开的服务就绪才往下走；`analytics-consumer`（依赖 ClickHouse）、`market-sim`、`udun-mock` 另等 3 分钟，没就绪只打印出来，不中止部署（以前 ClickHouse 一停，部署就卡在这里，nginx 热加载、解除只减仓与前端发布都不跑）。

放令牌：
1. 用户在 GitHub 的 Settings → Developer settings → Personal access tokens (classic) 建一个只勾 `read:packages` 的令牌。
2. 在服务器上 `printf '%s' '<令牌>' | sudo docker login ghcr.io -u lidp280504357 --password-stdin`（只存在 `/root/.docker/config.json`，不进仓库、不打印）；`sudo docker pull ghcr.io/lidp280504357/exchange-app:main` 能拉下来即可。
3. 额度：免费账户的私有包只有 500 MB 存储、每月 1 GB 流出（拉到 Actions 以外的机器都算），一个版本的二进制层约一两百 MB，按现在一天十来次部署，几天就会用完（用完后拉取失败，部署自动退回服务器构建）。仓库本身是公开的，镜像里没有密钥（都在服务器的 env 文件里），把包设为公开就没有这些限制。**用户决定（2026-10-03）**：维持免费层与私有包，额度用完就回退到服务器本地构建（拉取遇到 denied/429 同样快速回退，不重试）；本地构建前检查内存，可用内存不足时拒绝构建并提示升级服务器，由用户处理升级。

部署会重启 market-data-service，合约的标记价断几秒就可能进入只减仓（`MARK_PRICE_STALE`、`INDEX_SOURCES`）：脚本在服务都起来后最多等三分钟，标记价恢复（`degraded` 为 false）就以 `deploy-<提交>` 的名义解除这次部署期间开始的只减仓。部署中途失败时，它的开始时间留在 `infra/deploy.started`，下一次走完的部署从那时算起一起解除，然后删掉这个文件（2026-10-02 一次失败的部署后 ASTRA-USDT-PERP 因此停在只减仓，下一次部署也没解除）。

**运维锁**（2026-10-02 起，两个编码会话共用测试服）：部署、完整端到端（`task e2e`）与故障演练（`task fault`、单独运行的 `scripts/fault/*.sh`）一次只跑一个。`scripts/ops/lock.sh run --owner 说明 -- 命令` 经 ssh 在服务器上 `flock` 持有 `/opt/exchange/infra/ops.lock`，把持有者与开始时间写进 `ops.lock.owner`，命令结束（或本机进程退出、ssh 断开）即释放；最多等 60 分钟，最多持有 2 小时。服务器忙到 5 分钟不回 ssh 保活时连接会断、锁随之释放（2026-10-02 一次端到端中途因此被另一会话的部署插入）：`lock.sh` 立即打印 `lock: LOST`，命令本身通过时也以状态 75 结束，结果不能算数，要重跑。`scripts/ops/lock.sh status` 看谁在持有。被它调起的命令带 `OPS_LOCK_HELD=1`，里面再拿锁的步骤（演练脚本、`server-update.sh`）就不重复等待；不经它直接在服务器上跑的 `server-update.sh` 自己拿同一把锁。

脚本依次执行以下步骤：

1. 拉代码并重置到目标版本。
2. 把 `deploy/compose/` 同步到 `/opt/exchange/infra`。不碰 `.env`、`apps.env`、证书、Cloudflare IP 列表、`nginx/html/`、`nginx/admin/`、`nginx/sites/`、`udun-mock/`（托管钱包模拟网关的状态，属主 uid 10001，脚本在这里创建）；`signer/`、`admin/` 两个密钥目录不在仓库里，也不受影响。
3. 幂等核对 Redpanda topic。
4. 删掉 6 小时内没用过的构建缓存，确认根分区至少还有 8 GB，不够就停止部署；然后构建应用镜像 `exchange-app:latest`。所有应用服务共用这一个镜像，只构建一次（`docker compose build api-gateway`），之后 `up --no-build` 用它重建全部应用容器：按服务逐个构建会把同一镜像导出二十多次、每次解出全部二进制，2026-10-02 在导出时写满了磁盘。
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
| market-sim（平台币 ASTRA 的模拟市场，见 market-sim.md） | 8098（内网管理接口） | — | 9098 |

compose 健康检查请求运维端口的 `/readyz`：启动完成且依赖可用才返回 200，收到 SIGTERM 后立即变为 503（draining）。部署验证：

```bash
task deploy:status                         # 全部容器 healthy
curl -s https://astras.vip/v1/time         # 网关经 Cloudflare 与 nginx 可达
ssh exchange 'sudo docker exec exchange-infra-api-gateway-1 wget -qO- http://127.0.0.1:9080/readyz'
```

## 小机器调优（2026-09-28 资源评估）

以下调优是在旧测试服（t2.medium，2 vCPU / 3.8 GiB 突发型）上做的；2026-09-30 起的 c5a.xlarge（4 vCPU / 7.8 GiB）沿用了这些设置，可以按需放宽（例如 ClickHouse 的内存上限）。评估与数据见 [阶段 1 验收报告](../阶段1验收报告.md) §6：

- ClickHouse：`deploy/compose/clickhouse/config.d/small-server.xml` 去掉诊断用的系统日志表（trace_log、metric_log 等，保留 query_log、part_log），服务日志 warning 级、100 MB × 3，内存上限为物理内存 30%。改了这个文件，部署时 compose 会重建 ClickHouse 容器（约半分钟，analytics-consumer 自动重试）。
- Redpanda：`topics.sh` 把 `segment_fallocation_step` 设为 4 MiB（默认 32 MiB，每个分区的活动段都会预分配）。内存上限 `--memory 2G`（2026-10-02 加）：不设时 Seastar 用多少占多少、不还给系统，当天涨到 3.4–4.3 GB，交换区写满，构建时整机几分钟无响应；2 GB 足够 93 个分区（`topic_memory_per_partition` 4 MiB 下最多 512 个）。改这个命令行时 compose 会重建 redpanda 容器（Kafka 中断十几秒，应用服务自动重连，outbox 随后补发）。
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

## 磁盘去哪了（2026-10-02 从 86% 降到 45%）

先看 `df -h /`、`sudo docker system df`、`sudo du -sh /var/lib/docker/volumes/*/_data`，再看库里最大的表（`pg_stat_user_tables` 按 `pg_total_relation_size` 排序）与 Redpanda 各 topic（`rpk cluster logdirs describe`）。当时的四处与现在的设置：

| 来源 | 当时 | 处理与设置 |
|---|---|---|
| 构建缓存 | 14 GB，全部记录"在用"（`docker buildx du` 的 Reclaimable 为 0），`docker builder prune -af` 删不掉 | 重启 dockerd（`sudo systemctl restart docker`，全部容器停约 40 秒后按重启策略自动起来）后 `docker builder prune -af` 回收 13.7 GB。部署前后各把缓存删到 2 GB 以内（`--max-used-space 2gb`；Docker 29 已没有 `--keep-storage`）。又攒到删不掉时重复这一步，并按 [运维锁](#日常更新) 先拿锁 |
| `matching.wal` | 5.8 GB（约 90 个交易对的参考簿每秒约 220 条，保留两三个小时） | 现货引擎保留 30 分钟、每 10 分钟清理（[matching.md](matching.md)）；已有的大表要 `VACUUM FULL matching.wal` 才把空间还给磁盘 |
| 各服务 outbox | 账本 540 MB 等，共约 1 GB | 已发布的行测试服保留 6 小时（`OUTBOX_RETENTION`，[events.md](events.md)）；同样要 `VACUUM FULL <schema>.outbox` 才变小 |
| Redpanda | 6.8 GB，其中 `market.candle.events` 4 GB（7 天）、`market.depth` 约 1 GB | 派生行情流保留 1 小时、K 线流 1 天，`topics.sh` 每次部署都重设（[events.md](events.md)） |

做完后 `matching.wal` 482 MB、库 1.5 GB、根分区 45%（27 GB 空闲）。`VACUUM FULL` 独占整张表：WAL 表几秒到一分钟，期间引擎排队；要在运维锁里做。

## 本机调试

本机开发栈 `task dev`、单个服务 `task run -- <service>`：服务在本机运行，连测试服基础设施里单独的 dev 命名空间（库 `exchange_dev`、Redis DB 1、Kafka 前缀 `dev.`），不碰测试环境的数据，见 [local-dev.md](local-dev.md)。`task web:dev`（PC 站，`-- m`、`-- admin` 为手机站、后台）默认代理到测试服，`API_ORIGIN=http://localhost:8080` 改连本机网关。安全组已放行本机 IP，本机不需要 Docker。Ctrl-C 触发优雅退出。
