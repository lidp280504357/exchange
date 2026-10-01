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

# lift_deploy_degradations 解除部署期间开始的合约只减仓。部署会重启 market-data-service，标记价短暂中断，
# 合约可能因此进入只减仓（INDEX_SOURCES、MARK_PRICE_STALE）；按阶段 3 的设计只减仓须由人解除，部署者就是这个人：
# 等标记价恢复后解除，解除人记为本次部署。价源若真的断了，10 秒后会再次进入只减仓。
lift_deploy_degradations() {
  local started=$1 symbol
  sleep 20
  sudo docker compose "${COMPOSE[@]}" exec -T derivatives-service /app/exchangectl derivatives states 2>/dev/null |
    awk -v since="$started" 'NR > 1 && $2 == "true" && ($3 == "INDEX_SOURCES" || $3 == "MARK_PRICE_STALE") && $4 >= since {print $1}' |
    while read -r symbol; do
      sudo docker compose "${COMPOSE[@]}" exec -T -e EXCHANGECTL_ACTOR="deploy-$APP_VERSION" derivatives-service /app/exchangectl derivatives resume "$symbol" \
        </dev/null >/dev/null && echo "== $symbol 在部署期间进入只减仓，标记价已恢复，已解除"
    done
}

# prune_build_cache 删除 6 小时内没用过的构建缓存（常用的 Go 模块与编译缓存会留下）。Docker 29 上
# --keep-storage 什么也不删，不带 -a 也只删悬空记录：2026-10-01 缓存涨到 21 GB，一次构建写满磁盘。
prune_build_cache() {
  sudo docker builder prune -a -f --filter until=6h >/dev/null 2>&1 || echo "== 构建缓存清理失败（不影响部署）"
}

# ensure_disk_space 在构建前确认磁盘还有余量：磁盘写满时 Docker 会丢掉运行中容器的网络端点
# （2026-10-01 redpanda 因此对其他服务不可达，各服务就绪检查失败，合约进入只减仓）。宁可不部署。
ensure_disk_space() {
  local free_gb
  free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  if [ "$free_gb" -lt 8 ]; then
    echo "== 磁盘只剩 ${free_gb} GB，停止部署：先清理（见 docs/runbook/server-deploy.md）再重试"
    exit 1
  fi
}

main() {
  DEPLOY_STARTED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
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
    --exclude 'nginx/admin/' --exclude 'nginx/sites/' --exclude 'udun-mock/' deploy/compose/ "$INFRA"/
  cp deploy/redpanda/topics.sh "$INFRA/redpanda/topics.sh"
  mkdir -p "$INFRA/backup" && cp deploy/backup/pg-backup.sh "$INFRA/backup/pg-backup.sh"
  # 托管钱包模拟网关（ADR-0011）的状态目录，容器用户 uid 10001 可写（install -o 不认数字 uid，用 chown）
  sudo mkdir -p "$INFRA/udun-mock" && sudo chown 10001:10001 "$INFRA/udun-mock" && sudo chmod 700 "$INFRA/udun-mock"

  # 2. Topic 幂等核对
  bash "$INFRA/redpanda/topics.sh" >/dev/null && echo "== topic 核对完成"

  # 3. 构建并更新容器：应用 compose 文件存在时一起构建（镜像内版本号 = 当前提交），否则只更新基础设施。
  #    先起 instrument-service 并同步参考数据（资产、网络、交易对、费率），再起其余服务：它们启动时就读交易对与
  #    参考行情映射（market-data 只跟随设了 reference_symbol 的交易对，映射晚到会让合约因没有标记价而降级）
  COMPOSE=(-f "$INFRA/docker-compose.yml")
  if [ -f "$INFRA/docker-compose.apps.yml" ]; then
    COMPOSE+=(-f "$INFRA/docker-compose.apps.yml")
    prune_build_cache
    ensure_disk_space
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" build
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --wait --wait-timeout 300 instrument-service
    apply_instruments
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 600
  else
    sudo docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 180
  fi
  sudo docker image prune -f >/dev/null
  prune_build_cache
  # nginx 配置是挂载进容器的文件，内容变了 compose 不会重启它：校验后热加载（校验失败则部署失败，旧配置继续服务）
  sudo docker compose "${COMPOSE[@]}" exec -T nginx sh -c 'nginx -t -q && nginx -s reload' && echo "== nginx 配置已重新加载"
  if [ -f "$INFRA/docker-compose.apps.yml" ]; then
    lift_deploy_degradations "$DEPLOY_STARTED"
  fi

  # 4. 参考数据已在第 3 步随 instrument-service 同步（apply_instruments）
  # 5. 前端（web/ 的 pnpm workspace，ADR-0012）：在 node 容器里装一次依赖（glibc 镜像，打包器与 Tailwind 的原生模块
  #    都有对应二进制；pnpm 缓存放命名卷），构建三个站点与 Storybook，全部成功才替换 nginx 的
  #    静态目录。Turnstile 站点密钥是公开值。挂整个仓库：API 参考页要读 api/openapi
  if [ -f web/pnpm-workspace.yaml ]; then
    local site_key
    site_key="$(sudo grep -E '^TURNSTILE_SITE_KEY=' "$INFRA/apps.env" | cut -d= -f2- | tr -d '"' || true)"
    sudo docker run --rm -e CI=true -e TURNSTILE_SITE_KEY="$site_key" -v "$SRC:/src" -v exchange-pnpm-store:/pnpm-store \
      -w /src/web node:24-slim sh -c 'npm install -g pnpm@11 --silent >/dev/null && pnpm config set store-dir /pnpm-store >/dev/null \
        && pnpm install --frozen-lockfile --silent \
        && { { pnpm build && pnpm --filter @exchange/ui build-storybook; } >/tmp/build.log 2>&1 || { cat /tmp/build.log; exit 1; }; }'
    sudo mkdir -p "$INFRA/nginx/sites"
    local site
    for site in pc m admin; do
      sudo rsync -a --delete "web/apps/$site/dist/" "$INFRA/nginx/sites/$site/"
    done
    # 旧 H5（阶段 1–3）已由手机站取代（B3），/h5/ 由 nginx 301 到首页
    sudo rm -rf "$INFRA/nginx/sites/h5"
    sudo rsync -a --delete web/packages/ui/storybook-static/ "$INFRA/nginx/sites/storybook/"
    # 旧后台 /admin/（阶段 2 至 4 B4）已由 admin.astras.vip 取代（B5），nginx 把它重定向过去
    sudo rm -rf "$INFRA/nginx/admin"
    echo "== 前端已构建：PC $(ls web/apps/pc/dist/static | wc -l)、手机 $(ls web/apps/m/dist/static | wc -l)、后台 $(ls web/apps/admin/dist/assets | wc -l) 个资源文件"
  fi
  echo "== 服务状态"
  sudo docker compose "${COMPOSE[@]}" ps --format 'table {{.Service}}\t{{.Status}}'
}

main "$@"
