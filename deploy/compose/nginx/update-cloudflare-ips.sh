#!/usr/bin/env bash
# 在测试服上执行：拉取 Cloudflare 出口 IP 段，生成 nginx 的 set_real_ip_from 列表并热加载。
# 用法：bash /opt/exchange/infra/nginx/update-cloudflare-ips.sh   （可放 cron 每周一次）
set -euo pipefail
INFRA_DIR="${INFRA_DIR:-/opt/exchange/infra}"
OUT="$INFRA_DIR/nginx/conf.d/00-cloudflare-real-ip.conf"
tmp="$(mktemp)"
{
  echo "# generated $(date -u +%FT%TZ) by update-cloudflare-ips.sh; do not edit by hand"
  for u in https://www.cloudflare.com/ips-v4 https://www.cloudflare.com/ips-v6; do
    curl -fsS --max-time 20 "$u" | awk 'NF {print "set_real_ip_from " $1 ";"}'
  done
  echo "real_ip_header CF-Connecting-IP;"
} > "$tmp"
grep -q 'set_real_ip_from' "$tmp"
mv "$tmp" "$OUT"
chmod 644 "$OUT"
echo "wrote $OUT ($(grep -c set_real_ip_from "$OUT") ranges)"
if sudo docker compose -f "$INFRA_DIR/docker-compose.yml" ps --status running nginx 2>/dev/null | grep -q nginx; then
  sudo docker compose -f "$INFRA_DIR/docker-compose.yml" exec -T nginx nginx -t && sudo docker compose -f "$INFRA_DIR/docker-compose.yml" exec -T nginx nginx -s reload
  echo "nginx reloaded"
fi
