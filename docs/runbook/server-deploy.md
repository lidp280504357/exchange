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

脚本做四件事：拉代码并重置到目标版本；把 `deploy/compose/` 同步到 `/opt/exchange/infra`（不碰 `.env`、证书、Cloudflare IP 列表）；幂等核对 Redpanda topic；`docker compose up -d --build` 构建并更新容器，最后清理悬空镜像。

## 首次克隆（部署密钥加到 GitHub 之后）

```bash
ssh exchange
ssh -T git@github.com            # 预期输出：Hi lidp280504357/exchange! You've successfully authenticated ...
git clone git@github.com:lidp280504357/exchange.git /opt/exchange/src
bash /opt/exchange/src/deploy/server-update.sh
```

## 应用环境变量

基础设施凭据在 `/opt/exchange/infra/.env`。应用服务的变量（Turnstile、Resend、Alchemy、JWT 密钥等）阶段 1 起放在 `/opt/exchange/infra/apps.env`（权限 600，不入库），由 `docker-compose.apps.yml` 通过 `env_file` 注入；本地开发用仓库根目录的 `.env`。

## 本机调试

本机 `go run ./cmd/<service>` 或 `vite dev`，直连测试服的数据库、Redis、Redpanda、ClickHouse（安全组已放行本机 IP）。本机不需要 Docker。
