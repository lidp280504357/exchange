#!/usr/bin/env bash
# 测试服更新脚本：拉取仓库指定版本，同步基础设施配置，构建应用镜像并滚动更新。
# 用法（在测试服上）：bash /opt/exchange/src/deploy/server-update.sh [git-ref]
#   不带参数更新到 origin/main；回滚：bash server-update.sh <commit>
set -euo pipefail

# 全部逻辑放在函数里：bash 先读完整个函数再执行，脚本在执行中被 git 更新也不会读到半新半旧的内容。

# apply_instruments 以仓库文件为准幂等同步参考数据（资产、网络、交易对、费率）；已存在交易对的状态不受影响。
# 改了的（changed）与因后台改过而保留的（kept：后台的改动优先，--force 才按文件覆盖）逐条列出，最后一行是汇总
# （以前只留汇总，看不到哪些项被保留，审查 C3c）。
apply_instruments() {
  [ -f deploy/instruments/test.json ] || return 0
  local out
  out=$(sudo docker compose "${COMPOSE[@]}" exec -T instrument-service /app/exchangectl instruments apply \
    --file - --reason "deploy $APP_VERSION" < deploy/instruments/test.json)
  grep -E '^(changed|kept) ' <<<"$out" | sed 's/^/   /' || true
  tail -1 <<<"$out" | sed 's/^/== 参考数据：/'
}

# apply_margin 把杠杆条款的种子 deploy/instruments/margin.json 写进 margin-service（杠杆设计 2026-10-06 §4，E0 契约 §9）：
# 缺的建、上次由种子写的按文件改，后台改过的保留（--force 才覆盖）；杠杆在开关后面，同步失败只报出来，不中止部署。
apply_margin() {
  [ -f deploy/instruments/margin.json ] || return 0
  local out
  if ! out=$(sudo docker compose "${COMPOSE[@]}" exec -T margin-service /app/exchangectl margin apply --file - \
    < deploy/instruments/margin.json 2>&1); then
    echo "== 杠杆条款同步失败（不影响部署，exchangectl margin apply 重试）：$(tail -1 <<<"$out")"
    return 0
  fi
  grep -E '^(changed|kept) ' <<<"$out" | sed 's/^/   /' || true
  tail -1 <<<"$out" | sed 's/^/== 杠杆条款：/'
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

# prune_build_cache [上限] 把构建缓存删到上限以内（默认 2 GB，最近用过的留下，通常是 Go 模块与编译缓存）。Docker 29 的 buildx
# 已没有 --keep-storage（对应的是 --max-used-space），不带 -a 只删悬空记录：2026-10-01 缓存涨到 21 GB，一次构建写满磁盘。
# 2026-10-02 的 14 GB 是另一回事：全部记录都算"在用"（Reclaimable 0B），怎么删都删不掉，要重启 dockerd 才释放，
# 见 docs/runbook/server-deploy.md。
prune_build_cache() {
  sudo docker builder prune -a -f --max-used-space "${1:-2gb}" >/dev/null 2>&1 || echo "== 构建缓存清理失败（不影响部署）"
}

# pull_app_image 拉 GitHub Actions 为本提交构建的镜像（.github/workflows/image.yml，
# ghcr.io/lidp280504357/exchange-app:<完整提交号>）并标成 exchange-app:next（切换时才改成 latest）；服务器要登录过 ghcr.io（只读令牌，
# 见 docs/runbook/server-deploy.md）。本地已有就直接用。提交是 20 分钟内的（刚推送，Actions 可能还在构建）时，
# "还没有这个版本"最多等 10 分钟；更早的提交（回滚、image.yml 之前的、已被清掉的版本）与令牌被拒都不等。
# 拉不到就返回非 0，由调用方在服务器上构建。打完标签就去掉 ghcr 的标签：否则 image prune 不删它，每次部署都留下
# 一整份镜像（约 600 MB）。
# 本提交没动镜像的构建内容（.dockerignore 排除的文档、网站、compose、工具，image.yml 的 paths-ignore 同一份）时
# Actions 不构建（B89）。镜像按推送的最后一个提交打标签，所以从 HEAD 往回、到最近一个动过构建内容的提交为止，
# 这些提交的镜像内容都一样，按新到旧找第一个 ghcr 上有的。
pull_app_image() {
  local image="" out="" fresh last c deadline=$((SECONDS + 600))
  local -a same
  sudo grep -qs '"ghcr.io"' /root/.docker/config.json || return 1
  last=$(git log -1 --format=%H HEAD -- . ':(exclude)docs' ':(exclude)web' ':(exclude)desktop' ':(exclude).claude' \
    ':(exclude)deploy/compose' ':(exclude)tools' ':(top,glob,exclude)*.md')
  if [ -n "$last" ] && git rev-parse -q --verify "$last^" >/dev/null; then
    mapfile -t same < <(git rev-list HEAD --not "$last^")
  else
    same=("$(git rev-parse HEAD)")
  fi
  # 只有改过镜像内容的那次推送会触发 image 任务：它的提交 20 分钟内才值得等（按 HEAD 算时，一次新的纯文档推送会让
  # 早已失败或被清理的镜像也白等 10 分钟，审查 CX）。
  fresh=$(($(date +%s) - $(git log -1 --format=%ct "${last:-HEAD}") < 1200))
  while :; do
    for c in "${same[@]}"; do
      image="ghcr.io/lidp280504357/exchange-app:$c"
      sudo docker image inspect "$image" >/dev/null 2>&1 && break 2
      out=$(sudo docker pull -q "$image" 2>&1) && break 2
      if grep -qiE 'denied|unauthorized|forbidden|429|toomanyrequests|too many requests|quota|rate limit' <<<"$out"; then
        echo "== ghcr.io 拒绝了拉取（令牌过期、没有 read:packages，或免费额度用完），改在服务器上构建"
        return 1
      fi
    done
    if [ "$fresh" != 1 ]; then
      echo "== ghcr.io 上没有本提交的镜像（提交早于 image.yml 或版本已清理），改在服务器上构建"
      return 1
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "== 等了 10 分钟没拉到本提交的镜像（Actions 的 image 任务失败或还没跑完），改在服务器上构建"
      return 1
    fi
    sleep 20
  done
  [ "${image##*:}" = "$(git rev-parse HEAD)" ] || echo "== 本提交没动镜像的构建内容，用 ${image##*:} 的镜像（同样的内容）"
  sudo docker tag "$image" exchange-app:next
  sudo docker rmi "$image" >/dev/null
  echo "== 用 Actions 构建的镜像 $image"
}

# build_app_image 拉不到时在服务器上构建应用镜像（与 compose 里 x-app 的 build 段相同：仓库根目录为上下文、
# deploy/docker/Dockerfile、VERSION=短提交号），标成 exchange-app:next。基础镜像与 Actions 一样先从 mirror.gcr.io
# 拉（AWS ECR Public 曾对 Actions 回 429，B89）；只有拉基础镜像失败（不是编译失败）才改用 Dockerfile 默认的 ECR Public。
# 带 --pull：基础镜像是浮动标签（golang:1.27-alpine、alpine:3.22），不带时用服务器上缓存的旧版本，例如修了标准库漏洞的
# Go 补丁版出来以后仍用旧的（审查 IC，2026-10-09）。
build_app_image() {
  local log rc=0
  log=$(mktemp)
  sudo docker build --pull -f deploy/docker/Dockerfile --build-arg VERSION="$APP_VERSION" \
    --build-arg GO_IMAGE=mirror.gcr.io/library/golang:1.27-alpine --build-arg RUNTIME_IMAGE=mirror.gcr.io/library/alpine:3.22 \
    -t exchange-app:next . 2>&1 | tee "$log" || rc=$?
  if [ "$rc" != 0 ] && grep -qiE 'mirror\.gcr\.io[^ ]*: (failed to resolve source metadata|.*(429|too ?many ?requests|denied|unauthorized|timeout|no such host|connection reset|EOF|unexpected status))' "$log"; then
    echo "== 从 mirror.gcr.io 拉基础镜像失败，改用 AWS ECR Public 构建"
    rc=0
    sudo docker build --pull -f deploy/docker/Dockerfile --build-arg VERSION="$APP_VERSION" -t exchange-app:next . || rc=$?
  fi
  rm -f "$log"
  return "$rc"
}

# stop_before_changes 结束一次还没动过任何容器与站点的部署：这次写下的开始时间一并删掉，否则下一次部署会把这以后
# 进入的只减仓当成部署造成的而解除（继承自上一次没走完的部署的开始时间保留）。
stop_before_changes() {
  [ -n "${STARTED_HERE:-}" ] && rm -f "$INFRA/deploy.started"
  sudo docker rmi exchange-app:next >/dev/null 2>&1 || true
  exit 1
}

# read_memory 读可用内存（MemAvailable）与交换区已用量，单位 MB，写进 avail、swap。
read_memory() {
  avail=$(awk '/^MemAvailable:/ {print int($2 / 1024)}' /proc/meminfo)
  swap=$(awk '/^SwapTotal:/ {t = $2} /^SwapFree:/ {f = $2} END {print int((t - f) / 1024)}' /proc/meminfo)
}

# ensure_build_memory 在服务器上构建（Go 镜像或前端）前确认内存够，不让构建把内存与交换区吃光（2026-10-02 两次
# 整机无响应）：可用内存至少 3000 MB，且最近 30 秒真正在换页的量（vmstat 的 si+so）不到每秒 1 MB，才放行；不够就
# 停止部署并提示升级服务器（用户决定 2026-10-03：GHCR 不可用时回退到本地构建，内存不够就拒绝，升级由用户处理）。
# 交换区已用多少只打印、不判定（协调会话代用户定，2026-10-03）：Redpanda 预分配的内存里闲着的页会被换出去，
# 已用量一直涨（约 800 MB），并不是内存不够。可用内存比交换区已用量多 3000 MB 以上时，检查前先
# swapoff -a 再按原设备 swapon，把换出去的页收回内存（几十秒、不停服务；失败不影响部署）。
# BUILD_MIN_MEMORY_MB、BUILD_MAX_PAGING_KB 可改门槛。只在动容器之前调用：拒绝时什么都还没换。
ensure_build_memory() {
  local avail swap paging need=${BUILD_MIN_MEMORY_MB:-3000} most=${BUILD_MAX_PAGING_KB:-1024}
  read_memory
  if [ "$swap" -gt 0 ] && [ $((avail - swap)) -gt "$need" ]; then
    # 先记下在用的交换设备，收回后按设备名逐个打开，不依赖 /etc/fstab 里有没有它
    local devices device
    devices=$(awk 'NR > 1 {print $1}' /proc/swaps)
    if sudo swapoff -a; then
      for device in $devices; do sudo swapon "$device" || echo "== 交换区 $device 没能重新打开"; done
      echo "== 交换区里 ${swap} MB 已收回内存"
    else
      for device in $devices; do sudo swapon "$device" 2>/dev/null || true; done
      echo "== 收回交换区失败（不影响部署）"
    fi
    echo "== 交换区：$(swapon --show --noheadings | tr -s ' ' | tr '\n' ';')"
    read_memory
  fi
  # 最近 30 秒换入换出的页数（/proc/vmstat 的 pswpin、pswpout，4 KB 一页），折成 KB/s；不依赖 vmstat 命令。
  # 刚收回交换区、刚拉完镜像时内核会短暂换页（58bf6d8 的部署收回 635 MB 后量到 1872 KB/s）：最多量三次，有一次
  # 低于门槛就放行，三次都高才是一直在换页
  local window
  for window in 1 2 3; do
    paging=$(awk '/^pswpin |^pswpout / {s += $2} END {print s}' /proc/vmstat)
    sleep 30
    paging=$(( ($(awk '/^pswpin |^pswpout / {s += $2} END {print s}' /proc/vmstat) - paging) * 4 / 30 ))
    [ "$paging" -lt "$most" ] && break
    [ "$window" -lt 3 ] && echo "== 最近 30 秒换页 ${paging} KB/s，再量一次"
  done
  read_memory
  if [ "$avail" -lt "$need" ] || [ "$paging" -ge "$most" ]; then
    echo "== 内存不足，需要升级服务器：可用 ${avail} MB（至少 ${need}）、最近 30 秒换页 ${paging} KB/s（不到 ${most}），交换区已用 ${swap} MB；不在服务器上$1，停止部署（什么都没换）"
    stop_before_changes
  fi
  echo "== 内存：可用 ${avail} MB，最近 30 秒换页 ${paging} KB/s，交换区已用 ${swap} MB"
}

# ensure_disk_space 在构建前确认磁盘还有余量：磁盘写满时 Docker 会丢掉运行中容器的网络端点
# （2026-10-01 redpanda 因此对其他服务不可达，各服务就绪检查失败，合约进入只减仓）。宁可不部署。
ensure_disk_space() {
  local free_gb
  free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
  if [ "$free_gb" -lt 8 ]; then
    echo "== 磁盘只剩 ${free_gb} GB，停止部署：先清理（见 docs/runbook/server-deploy.md）再重试"
    stop_before_changes
  fi
}

# sync_infra 把基础设施与 nginx 配置以仓库为准同步到 infra 目录（不覆盖服务器上的 .env、证书和生成的 Cloudflare
# IP 列表），连同运维脚本、托管钱包模拟网关的状态目录与 market-sim 的签名密钥。在检查与构建都通过之后才做：
# 这之前被拒的部署不留下新配置（之后有人 compose up 或重启 nginx 就会混用新旧版本，审查 2026-10-03）。
sync_infra() {
  rsync -a --exclude '.env' --exclude 'apps.env' --exclude 'ssl/' --exclude '00-cloudflare-real-ip.conf' --exclude 'nginx/html/' \
    --exclude 'nginx/admin/' --exclude 'nginx/sites/' --exclude 'udun-mock/' deploy/compose/ "$INFRA"/
  cp deploy/redpanda/topics.sh "$INFRA/redpanda/topics.sh"
  mkdir -p "$INFRA/backup" && cp deploy/backup/pg-backup.sh "$INFRA/backup/pg-backup.sh"
  # 托管钱包模拟网关（ADR-0011）的状态目录，容器用户 uid 10001 可写（install -o 不认数字 uid，用 chown）
  sudo mkdir -p "$INFRA/udun-mock" && sudo chown 10001:10001 "$INFRA/udun-mock" && sudo chmod 700 "$INFRA/udun-mock"
  # App 下载（设计 2026-10-07 App 下载页 §7 #10）：admin-service（uid 10001）写入，downloads 由 nginx 只读挂载、
  # 对外为 /downloads/（目录 755、文件 644，nginx 的用户能读），app-uploads 放上传中的分片（nginx 不挂载）
  sudo mkdir -p "$INFRA/downloads/android" "$INFRA/downloads/ios" "$INFRA/app-uploads"
  sudo chown 10001:10001 "$INFRA/downloads" "$INFRA/downloads/android" "$INFRA/downloads/ios" "$INFRA/app-uploads"
  sudo chmod 755 "$INFRA/downloads" "$INFRA/downloads/android" "$INFRA/downloads/ios" && sudo chmod 700 "$INFRA/app-uploads"
  # 用户上传的头像（设计 2026-10-07 头像与用户名 §1.3）：user-service（uid 10001）写，nginx 只读直出，所以 755
  sudo mkdir -p "$INFRA/uploads/avatars" && sudo chown 10001:10001 "$INFRA/uploads/avatars" && sudo chmod 755 "$INFRA/uploads" "$INFRA/uploads/avatars"
  # market-sim 管理接口的签名密钥（ASTRA 设计 §6.2：审批人身份来自调用方凭据），每个调用方一把：
  # sim/sim.env 的 SIM_API_SECRET 只给 market-sim（容器里的 exchangectl sim 用），sim/admin.env 的
  # SIM_ADMIN_API_SECRET 给 market-sim 与 admin-service（只有它能填批准人）。第一次部署时生成，之后不变；值不打印
  # HOUSE 运行时额度接口（market-maker，审查 FL C47）同样两把：house/caps.env 的 HOUSE_CAPS_API_SECRET 只给
  # market-maker（容器里的 exchangectl house 用），house/admin.env 的 HOUSE_CAPS_ADMIN_API_SECRET 给 market-maker
  # 与 admin-service（只有它能填批准人）
  # 价格叠加（通用价格控制 J1）：market/overlay.env 的 OVERLAY_API_SECRET 给 market-sim（推送）与 market-data（验签）
  sudo mkdir -p "$INFRA/sim" "$INFRA/house" "$INFRA/market" && sudo chmod 700 "$INFRA/sim" "$INFRA/house" "$INFRA/market"
  local pair
  for pair in sim/sim.env:SIM_API_SECRET sim/admin.env:SIM_ADMIN_API_SECRET house/caps.env:HOUSE_CAPS_API_SECRET \
    house/admin.env:HOUSE_CAPS_ADMIN_API_SECRET market/overlay.env:OVERLAY_API_SECRET; do
    if ! sudo test -s "$INFRA/${pair%%:*}"; then
      sudo sh -c "umask 077 && printf '%s=%s\n' '${pair#*:}' \"\$(openssl rand -hex 32)\" >'$INFRA/${pair%%:*}'"
      echo "== 已生成 ${pair%%:*}（${pair#*:}）"
    fi
  done
}

# build_web 构建前端（web/ 的 pnpm workspace，ADR-0012），只写仓库里的 dist 目录，发布（publish_web）在容器更新之后：
# 在 node 容器里装一次依赖（glibc 镜像，打包器与 Tailwind 的原生模块都有对应二进制；pnpm 缓存放命名卷），构建三个
# 站点与 Storybook。Turnstile 站点密钥是公开值；VITE_APP_VERSION 是手机站「关于」里显示的版本。挂整个仓库：API 参考页
# 要读 api/openapi。三个站点逐个构建（每个是 tsc 加 vite，各要 1 GB 以上）：并行构建在 7.8 GB 的测试服上把内存与
# 交换区用尽，2026-10-02 两次让整机几分钟无响应（负载 139、ssh 连不上、运维锁的连接断开）。
build_web() {
  local site_key
  site_key="$(sudo grep -E '^TURNSTILE_SITE_KEY=' "$INFRA/apps.env" | cut -d= -f2- | tr -d '"' || true)"
  sudo docker run --rm -e CI=true -e TURNSTILE_SITE_KEY="$site_key" -e VITE_APP_VERSION="$APP_VERSION" -v "$SRC:/src" -v exchange-pnpm-store:/pnpm-store \
    -w /src/web node:24-slim sh -c 'npm install -g pnpm@11 --silent >/dev/null && pnpm config set store-dir /pnpm-store >/dev/null \
      && pnpm install --frozen-lockfile --silent \
      && { { pnpm -r --workspace-concurrency=1 --filter "./apps/*" build && pnpm --filter @exchange/ui build-storybook; } >/tmp/build.log 2>&1 || { cat /tmp/build.log; exit 1; }; }'
}

# check_nginx 在动任何东西之前校验仓库里的 nginx 新配置：用正在运行的 nginx 镜像、在它的 compose 网络里，
# 配上服务器上的证书与生成的 Cloudflare 地址段跑 nginx -t。不通过就停止部署、什么都没换（以前是容器换完才在
# 热加载时发现，新后端配旧站点，审查 2026-10-03）。nginx 还没起过（新服务器）就跳过，由热加载那一步把关。
check_nginx() {
  local dir image network
  image=$(sudo docker inspect -f '{{.Config.Image}}' exchange-infra-nginx-1 2>/dev/null) || return 0
  network=$(sudo docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' exchange-infra-nginx-1 | awk '{print $1}')
  dir=$(mktemp -d)
  cp -r deploy/compose/nginx/conf.d deploy/compose/nginx/snippets deploy/compose/nginx/nginx.conf "$dir"/
  sudo cat "$INFRA/nginx/conf.d/00-cloudflare-real-ip.conf" >"$dir/conf.d/00-cloudflare-real-ip.conf" 2>/dev/null || true
  if ! sudo docker run --rm --network "$network" -v "$dir/nginx.conf:/etc/nginx/nginx.conf:ro" -v "$dir/conf.d:/etc/nginx/conf.d:ro" \
    -v "$dir/snippets:/etc/nginx/snippets:ro" -v "$INFRA/nginx/ssl:/etc/nginx/ssl:ro" --entrypoint nginx "$image" -t -q; then
    rm -rf "$dir"
    echo "== nginx 新配置校验失败，停止部署（什么都没换）"
    stop_before_changes
  fi
  rm -rf "$dir"
  echo "== nginx 新配置校验通过"
}

# publish_web 把 build_web 构建好的站点换进 nginx 的静态目录。
publish_web() {
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
  echo "== 前端已发布：PC $(ls web/apps/pc/dist/static | wc -l)、手机 $(ls web/apps/m/dist/static | wc -l)、后台 $(ls web/apps/admin/dist/assets | wc -l) 个资源文件"
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
    STARTED_HERE=1
  fi

  cd "$SRC"
  local before
  before="$(git hash-object deploy/server-update.sh 2>/dev/null || true)"
  git fetch --prune origin
  git checkout -q main
  git reset -q --hard "$REF"
  # bash 已把本脚本旧版的 main 读进内存：拉取改了脚本时改跑新版本，新加的步骤这次就生效（fd 9 上的运维锁随 exec 带过去；
  # 开始时间是不是这次写的也带过去，否则新进程把它当成上一次没走完的部署的，被拒时留下它）
  if [ -z "${SERVER_UPDATE_REEXEC:-}" ] && [ "$before" != "$(git hash-object deploy/server-update.sh)" ]; then
    echo "== 部署脚本有更新，改跑新版本"
    exec env SERVER_UPDATE_REEXEC=1 OPS_LOCK_HELD=1 STARTED_HERE="${STARTED_HERE:-}" bash "$SRC/deploy/server-update.sh" "$@"
  fi
  APP_VERSION="$(git rev-parse --short HEAD)"
  echo "== 代码版本 $APP_VERSION：$(git log -1 --pretty=%s)"

  # 1. 先把要发布的都准备好，线上什么都不碰：应用镜像拉 Actions 构建好的、拉不到才在这里构建（都标成
  #    exchange-app:next），前端构建到仓库里的 dist。磁盘不够、内存不够或构建失败都停在这里，什么都没换：
  #    不留下新后端加旧站点，也不留下新配置或新镜像标签（审查 2026-10-03）。
  local apps="" build_image="" web=""
  [ -f deploy/compose/docker-compose.apps.yml ] && apps=1
  [ -f web/pnpm-workspace.yaml ] && web=1
  if [ -n "$apps" ]; then
    prune_build_cache
    ensure_disk_space
    # 所有应用服务运行同一个镜像，只构建一次（按服务逐个构建会把同一镜像导出二十多次、每次解出全部二进制，
    # 2026-10-02 因此在构建中写满磁盘）；up 用 --no-build 直接用这个镜像重建容器。
    pull_app_image || build_image=1
  fi
  if [ -n "$build_image" ] && [ -n "$web" ]; then
    ensure_build_memory "构建镜像与前端"
  elif [ -n "$build_image" ] || [ -n "$web" ]; then
    ensure_build_memory "构建$([ -n "$build_image" ] && echo 镜像 || echo 前端)"
  fi
  if [ -n "$build_image" ]; then
    build_app_image || stop_before_changes
  fi
  if [ -n "$web" ]; then
    build_web || stop_before_changes
  fi
  check_nginx

  # 2. 切换：配置同步到 infra 目录，Topic 幂等核对，准备好的镜像打成 exchange-app:latest
  sync_infra
  bash "$INFRA/redpanda/topics.sh" >/dev/null && echo "== topic 核对完成"
  COMPOSE=(-f "$INFRA/docker-compose.yml")
  if [ -n "$apps" ]; then
    COMPOSE+=(-f "$INFRA/docker-compose.apps.yml")
    sudo docker tag exchange-app:next exchange-app:latest
    sudo docker rmi exchange-app:next >/dev/null
  fi

  # 3. 更新容器：应用 compose 文件存在时用上面的镜像（版本号 = 当前提交），否则只更新基础设施。
  #    先起 instrument-service 并同步参考数据（资产、网络、交易对、费率），再起其余服务：它们启动时就读交易对与
  #    参考行情映射（market-data 只跟随设了 reference_symbol 的交易对，映射晚到会让合约因没有标记价而降级）
  if [ -n "$apps" ]; then
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --wait --wait-timeout 300 instrument-service
    apply_instruments
    # 先全部起来，部署只等站点离不开的服务；ClickHouse 停着时 analytics-consumer 不就绪之类的（批量消费者的下游
    # 一直失败时就绪检查不通过，这是对的）只报出来，不让部署在热加载 nginx、解除只减仓与发布前端之前中止
    local optional=(analytics-consumer market-sim udun-mock) core
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --remove-orphans
    mapfile -t core < <(sudo docker compose "${COMPOSE[@]}" config --services | grep -vxF -f <(printf '%s\n' "${optional[@]}"))
    sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --wait --wait-timeout 600 "${core[@]}"
    apply_margin
    if ! sudo APP_VERSION="$APP_VERSION" docker compose "${COMPOSE[@]}" up -d --no-build --wait --wait-timeout 180 "${optional[@]}"; then
      echo "== 这些服务还没就绪（不影响部署，稍后看）：$(sudo docker compose "${COMPOSE[@]}" ps --format '{{.Service}} {{.Status}}' | grep -v '(healthy)' | tr '\n' ';')"
    fi
  else
    sudo docker compose "${COMPOSE[@]}" up -d --remove-orphans --wait --wait-timeout 180
  fi
  sudo docker image prune -f >/dev/null
  # 两天没用的镜像也删（构建前端用的 node、拉过的旧版本），下次要用时再拉
  sudo docker image prune -af --filter "until=48h" >/dev/null
  # 镜像是从 ghcr.io 拉的：服务器上的构建缓存只在拉不到、回退本地构建时有用，删到 256 MB（2026-10-07 磁盘 86% 时
  # 它占 1.9 GB、全部可回收，B147）；回退时冷缓存建得慢一些，照样能建
  if [ -n "$apps" ] && [ -z "$build_image" ]; then
    prune_build_cache 256mb
  else
    prune_build_cache
  fi
  # nginx 配置是挂载进容器的文件，内容变了 compose 不会重启它：校验后热加载（校验失败则部署失败，旧配置继续服务）
  sudo docker compose "${COMPOSE[@]}" exec -T nginx sh -c 'nginx -t -q && nginx -s reload' && echo "== nginx 配置已重新加载"
  # 4. 发布第 1 步构建好的前端，紧跟新后端（解除只减仓最多要等近四分钟，站点不等它）
  if [ -n "$web" ]; then
    publish_web
  fi
  if [ -n "$apps" ]; then
    lift_deploy_degradations "$DEPLOY_STARTED"
  fi
  echo "== 服务状态"
  sudo docker compose "${COMPOSE[@]}" ps --format 'table {{.Service}}\t{{.Status}}'
  rm -f "$INFRA/deploy.started"
}

main "$@"
