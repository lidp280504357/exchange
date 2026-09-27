# ADR-0007：本机只调试，测试服拉取源码构建镜像部署

- 状态：已接受（2026-09-29）
- 关联：决策 #15、#16、#27；runbook `docs/runbook/server-deploy.md`

## 背景

用户本机不安装 Docker；只有一台 t2.medium 测试服；观测平台暂缓。

## 决策

1. 本机：`go run`/`vite dev` 直连测试服的 PostgreSQL、Redis、Redpanda、ClickHouse（安全组只放行本机 IP）；本机不起 compose。
2. 代码推送 GitHub 后，测试服执行 `deploy/server-update.sh`：拉取指定版本、同步 `deploy/compose/` 到 `/opt/exchange/infra`（不覆盖 `.env`、证书、生成的 IP 列表）、幂等核对 topic、`docker compose up -d --build` 用多阶段 Dockerfile 在服务器上构建镜像。
3. 入口：Cloudflare 代理 → nginx 容器（源站证书）→ `api-gateway:8080`；H5 构建产物由 nginx 静态托管。
4. 应用密钥放服务器 `/opt/exchange/infra/apps.env`（600，不入库），compose 用 `env_file` 注入；基础设施凭据在同目录 `.env`。
5. CI（GitHub Actions）只做质量门禁，不做部署；部署由人在服务器触发，回滚即指定旧提交重跑脚本。
6. 生产环境预留 Kubernetes，阶段 4 前不做。

## 理由

最少的移动部件；服务器构建避免本机安装 Docker；配置以仓库为准，服务器状态可重建。

## 后果

- 首次构建在 2 vCPU 上较慢；Docker 层缓存与 Go 模块缓存挂载缓解。
- 单机同时跑基础设施与应用，内存吃紧时需升配或拆分应用机。

## 备选

本机构建镜像推仓库（需本机 Docker）；CI 构建并推送镜像（需镜像仓库与部署凭据，阶段 4 再考虑）。
