#!/usr/bin/env bash
# 测试服更新脚本：拉取仓库指定版本，同步基础设施配置，构建应用镜像并滚动更新。
# 用法（在测试服上）：bash /opt/exchange/src/deploy/server-update.sh [git-ref]
#   不带参数更新到 origin/main；回滚：bash server-update.sh <commit>
set -euo pipefail

SRC="${SRC:-/opt/exchange/src}"
INFRA="${INFRA:-/opt/exchange/infra}"
REF="${1:-origin/main}"

cd "$SRC"
git fetch --prune origin
git checkout -q main
git reset -q --hard "$REF"
echo "== 代码版本 $(git rev-parse --short HEAD)：$(git log -1 --pretty=%s)"

# 1. 基础设施与 nginx 配置以仓库为准同步到 infra 目录；不覆盖服务器上的 .env、证书和生成的 Cloudflare IP 列表
rsync -a --exclude '.env' --exclude 'ssl/' --exclude '00-cloudflare-real-ip.conf' deploy/compose/ "$INFRA"/
cp deploy/redpanda/topics.sh "$INFRA/redpanda/topics.sh"
mkdir -p "$INFRA/backup" && cp deploy/backup/pg-backup.sh "$INFRA/backup/pg-backup.sh"

# 2. Topic 幂等核对
bash "$INFRA/redpanda/topics.sh" >/dev/null && echo "== topic 核对完成"

# 3. 构建并更新容器：应用 compose 文件（阶段 1 产出）存在时一起构建，否则只更新基础设施
if [ -f "$INFRA/docker-compose.apps.yml" ]; then
  sudo docker compose -f "$INFRA/docker-compose.yml" -f "$INFRA/docker-compose.apps.yml" up -d --build --remove-orphans --wait --wait-timeout 300
else
  sudo docker compose -f "$INFRA/docker-compose.yml" up -d --remove-orphans --wait --wait-timeout 180
fi
sudo docker image prune -f >/dev/null
echo "== 服务状态"
sudo docker compose -f "$INFRA/docker-compose.yml" ps --format 'table {{.Service}}\t{{.Status}}'
