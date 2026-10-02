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
# 等标记价恢复（行情接口 degraded 为 false）后解除，解除人记为本次部署。最多等三分钟、每 15 秒看一次：指数靠平台
# 自己市场的合约（平台币永续）要等机器人重新成交才恢复，太早解除会再次进入只减仓（2026-10-02 因此停了一小时）。
lift_deploy_degradations() {
  local started=$1 symbol left deadline=$((SECONDS + 200))
  sleep 20
  while :; do
    left=$(sudo docker compose "${COMPOSE[@]}" exec -T derivatives-service /app/exchangectl derivatives states 2>/dev/null |
      awk -v since="$started" 'NR > 1 && $2 == "true" && ($3 == "INDEX_SOURCES" || $3 == "MARK_PRICE_STALE") && $4 >= since {print $1}')
    [ -z "$left" ] && return 0
    for symbol in $left; do
      if sudo docker compose "${COMPOSE[@]}" exec -T market-data-service wget -qO- "http://127.0.0.1:8090/v1/market/$symbol/mark-price" 2>/dev/null |
        grep -q '"degraded":false'; then
        sudo docker compose "${COMPOSE[@]}" exec -T -e EXCHANGECTL_ACTOR="deploy-$APP_VERSION" derivatives-service /app/exchangectl derivatives resume "$symbol" \
          </dev/null >/dev/null && echo "== $symbol 在部署期间进入只减仓，标记价已恢复，已解除"
      fi
    done
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "== 部署后仍在只减仓（标记价未恢复）：$(echo "$left" | tr '\n' ' ')；恢复后用 exchangectl derivatives resume 解除"
      return 0
    fi
    sleep 15
  done
}

# prune_build_cache 把构建缓存删到 2 GB 以内（最近用过的留下，通常是 Go 模块与编译缓存）。Docker 29 的 buildx
# 已没有 --keep-storage（对应的是 --max-used-space），不带 -a 只删悬空记录：2026-10-01 缓存涨到 21 GB，一次构建写满磁盘。
# 2026-10-02 的 14 GB 是另一回事：全部记录都算"在用"（Reclaimable 0B），怎么删都删不掉，要重启 dockerd 才释放，
# 见 docs/runbook/server-deploy.md。
prune_build_cache() {
  sudo docker builder prune -a -f --max-used-space 2gb >/dev/null 2>&1 || echo "== 构建缓存清理失败（不影响部署）"
}

# pull_app_image 拉 GitHub Actions 为本提交构建的镜像（.github/workflows/image.yml，
# ghcr.io/lidp280504357/exchange-app:<完整提交号>）并标成 exchange-app:latest；服务器要登录过 ghcr.io（只读令牌，
# 见 docs/runbook/server-deploy.md）。本地已有就直接用。提交是 20 分钟内的（刚推送，Actions 可能还在构建）时，
# "还没有这个版本"最多等 10 分钟；更早的提交（回滚、image.yml 之前的、已被清掉的版本）与令牌被拒都不等。
# 拉不到就返回非 0，由调用方在服务器上构建。打完标签就去掉 ghcr 的标签：否则 image prune 不删它，每次部署都留下
# 一整份镜像（约 600 MB）。
pull_app_image() {
  local image out fresh deadline=$((SECONDS + 600))
  image="ghcr.io/lidp280504357/exchange-app:$(git rev-parse HEAD)"
  sudo grep -qs '"ghcr.io"' /root/.docker/config.json || return 1
  fresh=$(($(date +%s) - $(git log -1 --format=%ct HEAD) < 1200))
  until sudo docker image inspect "$image" >/dev/null 2>&1 || out=$(sudo docker pull -q "$image" 2>&1); do
    if grep -qiE 'denied|unauthorized|429|too many requests' <<<"$out"; then
      echo "== ghcr.io 拒绝了拉取（令牌过期、没有 read:packages，或免费额度用完），改在服务器上构建"
      return 1
    fi
    if grep -qiE 'manifest unknown|not found' <<<"$out" && [ "$fresh" != 1 ]; then
      echo "== ghcr.io 上没有 $image（提交早于 image.yml 或版本已清理），改在服务器上构建"
      return 1
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "== 等了 10 分钟没拉到 $image（Actions 的 image 任务失败或还没跑完），改在服务器上构建"
      return 1
    fi
    sleep 20
  done
  sudo docker tag "$image" exchange-app:latest
  sudo docker rmi "$image" >/dev/null
  echo "== 用 Actions 构建的镜像 $image"
}

# ensure_build_memory 在服务器上构建（Go 镜像或前端）前确认可用内存（MemAvailable）够：不够就停止部署、提示升级
# 服务器，不让构建把内存与交换区吃光（2026-10-02 两次整机无响应）。用户决定（2026-10-03）：GHCR 额度用完时回退
# 到本地构建，内存不够就拒绝，升级由用户处理。BUILD_MIN_MEMORY_MB 可改门槛（默认 2000）。
ensure_build_memory() {
  local avail need=${BUILD_MIN_MEMORY_MB:-2000}
  avail=$(awk '/^MemAvailable:/ {print int($2 / 1024)}' /proc/meminfo)
  if [ "$avail" -lt "$need" ]; then
    echo "== 可用内存只有 ${avail} MB，在服务器上$1至少要 ${need} MB：停止部署。请升级服务器（或等 GitHub Actions 的镜像，见 docs/runbook/server-deploy.md）"
    exit 1
  fi
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

# take_ops_lock 在没有经 scripts/ops/lock.sh 调用时（OPS_LOCK_HELD 未设）自己拿运维锁：部署、完整端到端与
# 故障演练轮流进行，两个会话不会同时部署。锁随本进程结束释放；最多等一小时。
take_ops_lock() {
  [ -z "${OPS_LOCK_HELD:-}" ] || return 0
  exec 9>"$INFRA/ops.lock"
  if ! flock -n 9; then
    echo "== 等待运维锁：$(cat "$INFRA/ops.lock.owner" 2>/dev/null || echo ?)"
    flock -w 3600 9 || { echo "== 运维锁一小时内没有释放，放弃部署"; exit 1; }
  fi
  echo "server-update.sh ${1:-origin/main} since $(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$INFRA/ops.lock.owner"
}

main() {
  DEPLOY_STARTED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  SRC="${SRC:-/opt/exchange/src}"
  INFRA="${INFRA:-/opt/exchange/infra}"
  REF="${1:-origin/main}"
  take_ops_lock "$REF"
  # 上一次部署没有走完（例如某个服务起不来）时，它开始后进入的只减仓也算"部署期间"：从它的开始时间算起，成功后才清掉
  if [ -s "$INFRA/deploy.started" ]; then
    DEPLOY_STARTED="$(cat "$INFRA/deploy.started")"
  else
    echo "$DEPLOY_STARTED" >"$INFRA/deploy.started"
  fi

  cd "$SRC"
  local before
  before="$(git hash-object deploy/server-update.sh 2>/dev/null || true)"
  git fetch --prune origin
  git checkout -q main
  git reset -q --hard "$REF"
  # bash 已把本脚本旧版的 main 读进内存：拉取改了脚本时改跑新版本，新加的步骤这次就生效（fd 9 上的运维锁随 exec 带过去）
  if [ -z "${SERVER_UPDATE_REEXEC:-}" ] && [ "$before" != "$(git hash-object deploy/server-update.sh)" ]; then
    echo "== 部署脚本有更新，改跑新版本"
    exec env SERVER_UPDATE_REEXEC=1 OPS_LOCK_HELD=1 bash "$SRC/deploy/server-update.sh" "$@"
  fi
  APP_VERSION="$(git rev-parse --short HEAD)"
  echo "== 代码版本 $APP_VERSION：$(git log -1 --pretty=%s)"

  # 1. 基础设施与 nginx 配置以仓库为准同步到 infra 目录；不覆盖服务器上的 .env、证书和生成的 Cloudflare IP 列表
  rsync -a --exclude '.env' --exclude 'apps.env' --exclude 'ssl/' --exclude '00-cloudflare-real-ip.conf' --exclude 'nginx/html/' \
    --exclude 'nginx/admin/' --exclude 'nginx/sites/' --exclude 'udun-mock/' deploy/compose/ "$INFRA"/
  cp deploy/redpanda/topics.sh "$INFRA/redpanda/topics.sh"
  mkdir -p "$INFRA/backup" && cp deploy/backup/pg-backup.sh "$INFRA/backup/pg-backup.sh"
  # 托管钱包模拟网关（ADR-0011）的状态目录，容器用户 uid 10001 可写（install -o 不认数字 uid，用 chown）
  sudo mkdir -p "$INFRA/udun-mock" && sudo chown 10001:10001 "$INFRA/udun-mock" && sudo chmod 700 "$INFRA/udun-mock"
  # market-sim 管理接口的签名密钥（ASTRA 设计 §6.2：审批人身份来自调用方凭据），每个调用方一把：
  # sim/sim.env 的 SIM_API_SECRET 只给 market-sim（容器里的 exchangectl sim 用），sim/admin.env 的
  # SIM_ADMIN_API_SECRET 给 market-sim 与 admin-service（只有它能填批准人）。第一次部署时生成，之后不变；值不打印
  sudo mkdir -p "$INFRA/sim" && sudo chmod 700 "$INFRA/sim"
  for pair in sim.env:SIM_API_SECRET admin.env:SIM_ADMIN_API_SECRET; do
    if ! sudo test -s "$INFRA/sim/${pair%%:*}"; then
      sudo sh -c "umask 077 && printf '%s=%s\n' '${pair#*:}' \"\$(openssl rand -hex 32)\" >'$INFRA/sim/${pair%%:*}'"
      echo "== 已生成 sim/${pair%%:*}（${pair#*:}）"
    fi
  done

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
    # 所有应用服务运行同一个镜像 exchange-app:latest：优先拉 Actions 构建好的，拉不到才在这里构建，且只构建一次
    # （按服务逐个构建会把同一镜像导出二十多次、每次解出全部二进制，2026-10-02 因此在构建中写满磁盘）；
    # up 用 --no-build 直接用这个镜像重建容器。
    if ! pull_app_image; then
      ensure_build_memory "构建镜像"
      sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" build api-gateway
    fi
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --wait --wait-timeout 300 instrument-service
    apply_instruments
    # 先全部起来，部署只等站点离不开的服务；ClickHouse 停着时 analytics-consumer 不就绪之类的（批量消费者的下游
    # 一直失败时就绪检查不通过，这是对的）只报出来，不让部署在热加载 nginx、解除只减仓与发布前端之前中止
    local optional=(analytics-consumer market-sim udun-mock) core
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --remove-orphans
    mapfile -t core < <(sudo docker compose "${COMPOSE[@]}" config --services | grep -vxF -f <(printf '%s\n' "${optional[@]}"))
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --wait --wait-timeout 600 "${core[@]}"
    if ! sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --wait --wait-timeout 180 "${optional[@]}"; then
      echo "== 这些服务还没就绪（不影响部署，稍后看）：$(sudo docker compose "${COMPOSE[@]}" ps --format '{{.Service}} {{.Status}}' | grep -v '(healthy)' | tr '\n' ';')"
    fi
  else
    sudo docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 180
  fi
  sudo docker image prune -f >/dev/null
  # 两天没用的镜像也删（构建前端用的 node、拉过的旧版本），下次要用时再拉
  sudo docker image prune -af --filter "until=48h" >/dev/null
  prune_build_cache
  # nginx 配置是挂载进容器的文件，内容变了 compose 不会重启它：校验后热加载（校验失败则部署失败，旧配置继续服务）
  sudo docker compose "${COMPOSE[@]}" exec -T nginx sh -c 'nginx -t -q && nginx -s reload' && echo "== nginx 配置已重新加载"
  if [ -f "$INFRA/docker-compose.apps.yml" ]; then
    lift_deploy_degradations "$DEPLOY_STARTED"
  fi

  # 4. 参考数据已在第 3 步随 instrument-service 同步（apply_instruments）
  # 5. 前端（web/ 的 pnpm workspace，ADR-0012）：在 node 容器里装一次依赖（glibc 镜像，打包器与 Tailwind 的原生模块
  #    都有对应二进制；pnpm 缓存放命名卷），构建三个站点与 Storybook，全部成功才替换 nginx 的
  #    静态目录。Turnstile 站点密钥是公开值；VITE_APP_VERSION 是手机站「关于」里显示的版本。挂整个仓库：API 参考页要读 api/openapi
  #    三个站点逐个构建（每个是 tsc 加 vite，各要 1 GB 以上）：并行构建在 7.8 GB 的测试服上把内存与交换区用尽，
  #    2026-10-02 两次让整机几分钟无响应（负载 139、ssh 连不上、运维锁的连接断开）
  if [ -f web/pnpm-workspace.yaml ]; then
    ensure_build_memory "构建前端"
    local site_key
    site_key="$(sudo grep -E '^TURNSTILE_SITE_KEY=' "$INFRA/apps.env" | cut -d= -f2- | tr -d '"' || true)"
    sudo docker run --rm -e CI=true -e TURNSTILE_SITE_KEY="$site_key" -e VITE_APP_VERSION="$APP_VERSION" -v "$SRC:/src" -v exchange-pnpm-store:/pnpm-store \
      -w /src/web node:24-slim sh -c 'npm install -g pnpm@11 --silent >/dev/null && pnpm config set store-dir /pnpm-store >/dev/null \
        && pnpm install --frozen-lockfile --silent \
        && { { pnpm -r --workspace-concurrency=1 --filter "./apps/*" build && pnpm --filter @exchange/ui build-storybook; } >/tmp/build.log 2>&1 || { cat /tmp/build.log; exit 1; }; }'
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
  rm -f "$INFRA/deploy.started"
}

main "$@"
