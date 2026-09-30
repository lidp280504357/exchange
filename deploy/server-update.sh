#!/usr/bin/env bash
# 测试服更新脚本：拉取仓库指定版本，同步基础设施配置，构建应用镜像并滚动更新。
# 用法（在测试服上）：bash /opt/exchange/src/deploy/server-update.sh [git-ref]
#   不带参数更新到 origin/main；回滚：bash server-update.sh <commit>
set -euo pipefail

# 全部逻辑放在函数里：bash 先读完整个函数再执行，脚本在执行中被 git 更新也不会读到半新半旧的内容。

# apply_instruments 以仓库文件为准幂等同步参考数据（资产、网络、交易对、费率）；已存在交易对的状态不受影响
apply_instruments() {
  [ -f deploy/instruments/test.json ] || return 0
  sudo docker compose "${COMPOSE[@]}" exec -T instrument-service /app/exchangectl instruments apply \
    --file - --reason "deploy $APP_VERSION" < deploy/instruments/test.json | tail -1 | sed 's/^/== 参考数据：/'
}

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
  rsync -a --exclude '.env' --exclude 'apps.env' --exclude 'ssl/' --exclude '00-cloudflare-real-ip.conf' --exclude 'nginx/html/' \
    --exclude 'nginx/admin/' --exclude 'nginx/sites/' deploy/compose/ "$INFRA"/
  cp deploy/redpanda/topics.sh "$INFRA/redpanda/topics.sh"
  mkdir -p "$INFRA/backup" && cp deploy/backup/pg-backup.sh "$INFRA/backup/pg-backup.sh"

  # 2. Topic 幂等核对
  bash "$INFRA/redpanda/topics.sh" >/dev/null && echo "== topic 核对完成"

  # 3. 构建并更新容器：应用 compose 文件存在时一起构建（镜像内版本号 = 当前提交），否则只更新基础设施。
  #    先起 instrument-service 并同步参考数据（资产、网络、交易对、费率），再起其余服务：它们启动时就读交易对与
  #    参考行情映射（market-data 只跟随设了 reference_symbol 的交易对，映射晚到会让合约因没有标记价而降级）
  COMPOSE=(-f "$INFRA/docker-compose.yml")
  if [ -f "$INFRA/docker-compose.apps.yml" ]; then
    COMPOSE+=(-f "$INFRA/docker-compose.apps.yml")
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" build
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --wait --wait-timeout 300 instrument-service
    apply_instruments
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 600
  else
    sudo docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 180
  fi
  sudo docker image prune -f >/dev/null
  # 构建缓存每次部署都在长（2026-09-28 已近 9 GB，磁盘 82%）：保留 3 GB，够 Go 模块与编译缓存和最近的层
  sudo docker builder prune -f --keep-storage 3gb >/dev/null 2>&1 || echo "== 构建缓存清理失败（不影响部署）"
  # nginx 配置是挂载进容器的文件，内容变了 compose 不会重启它：校验后热加载（校验失败则部署失败，旧配置继续服务）
  sudo docker compose "${COMPOSE[@]}" exec -T nginx sh -c 'nginx -t -q && nginx -s reload' && echo "== nginx 配置已重新加载"

  # 4. 参考数据已在第 3 步随 instrument-service 同步（apply_instruments）
  # 5. 前端（web/ 的 pnpm workspace，ADR-0012）：在 node 容器里装一次依赖（glibc 镜像，打包器与 Tailwind 的原生模块
  #    都有对应二进制；pnpm 缓存放命名卷），构建三个站点、旧后台与 Storybook，全部成功才替换 nginx 的
  #    静态目录。Turnstile 站点密钥是公开值。挂整个仓库：API 参考页要读 api/openapi
  if [ -f web/pnpm-workspace.yaml ]; then
    local site_key
    site_key="$(sudo grep -E '^TURNSTILE_SITE_KEY=' "$INFRA/apps.env" | cut -d= -f2- | tr -d '"' || true)"
    sudo docker run --rm -e CI=true -e TURNSTILE_SITE_KEY="$site_key" -v "$SRC:/src" -v exchange-pnpm-store:/pnpm-store \
      -w /src/web node:24-slim sh -c 'npm install -g pnpm@11 --silent >/dev/null && pnpm config set store-dir /pnpm-store >/dev/null \
        && pnpm install --frozen-lockfile --silent \
        && { { pnpm build && pnpm --filter @exchange/ui build-storybook; } >/tmp/build.log 2>&1 || { cat /tmp/build.log; exit 1; }; }'
    sudo mkdir -p "$INFRA/nginx/sites" "$INFRA/nginx/admin"
    local site
    for site in pc m admin; do
      sudo rsync -a --delete "web/apps/$site/dist/" "$INFRA/nginx/sites/$site/"
    done
    # 旧 H5（阶段 1–3）已由手机站取代（B3），/h5/ 由 nginx 301 到首页
    sudo rm -rf "$INFRA/nginx/sites/h5"
    sudo rsync -a --delete web/packages/ui/storybook-static/ "$INFRA/nginx/sites/storybook/"
    sudo rsync -a --delete web/admin/dist/ "$INFRA/nginx/admin/"
    echo "== 前端已构建：PC $(ls web/apps/pc/dist/static | wc -l)、手机 $(ls web/apps/m/dist/static | wc -l)、后台 $(ls web/apps/admin/dist/assets | wc -l) 个资源文件"
  fi
  echo "== 服务状态"
  sudo docker compose "${COMPOSE[@]}" ps --format 'table {{.Service}}\t{{.Status}}'
}

main "$@"
