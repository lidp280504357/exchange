#!/usr/bin/env bash
# 测试服更新脚本：拉取仓库指定版本，同步基础设施配置，构建应用镜像并滚动更新。
# 用法（在测试服上）：bash /opt/exchange/src/deploy/server-update.sh [git-ref]
#   不带参数更新到 origin/main；回滚：bash server-update.sh <commit>
set -euo pipefail

# 全部逻辑放在函数里：bash 先读完整个函数再执行，脚本在执行中被 git 更新也不会读到半新半旧的内容。
main() {
  SRC="${SRC:-/opt/exchange/src}"
  INFRA="${INFRA:-/opt/exchange/infra}"
  REF="${1:-origin/main}"

  cd "$SRC"
  git fetch --prune origin
  git checkout -q main
  git reset -q --hard "$REF"
  APP_VERSION="$(git rev-parse --short HEAD)"
  echo "== 代码版本 $APP_VERSION：$(git log -1 --pretty=%s)"

  # 1. 基础设施与 nginx 配置以仓库为准同步到 infra 目录；不覆盖服务器上的 .env、证书和生成的 Cloudflare IP 列表
  rsync -a --exclude '.env' --exclude 'apps.env' --exclude 'ssl/' --exclude '00-cloudflare-real-ip.conf' deploy/compose/ "$INFRA"/
  cp deploy/redpanda/topics.sh "$INFRA/redpanda/topics.sh"
  mkdir -p "$INFRA/backup" && cp deploy/backup/pg-backup.sh "$INFRA/backup/pg-backup.sh"

  # 2. Topic 幂等核对
  bash "$INFRA/redpanda/topics.sh" >/dev/null && echo "== topic 核对完成"

  # 3. 构建并更新容器：应用 compose 文件存在时一起构建（镜像内版本号 = 当前提交），否则只更新基础设施
  COMPOSE=(-f "$INFRA/docker-compose.yml")
  if [ -f "$INFRA/docker-compose.apps.yml" ]; then
    COMPOSE+=(-f "$INFRA/docker-compose.apps.yml")
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --build --remove-orphans --wait --wait-timeout 600
  else
    sudo docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 180
  fi
  sudo docker image prune -f >/dev/null
  echo "== 服务状态"
  sudo docker compose "${COMPOSE[@]}" ps --format 'table {{.Service}}\t{{.Status}}'
}

main "$@"
