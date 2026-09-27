#!/usr/bin/env bash
# PostgreSQL 逻辑备份（测试服，每日一次，保留 7 天）。
# 用法：bash /opt/exchange/infra/backup/pg-backup.sh   （cron 每日 03:30 UTC）
set -euo pipefail
INFRA_DIR="${INFRA_DIR:-/opt/exchange/infra}"
BACKUP_DIR="${BACKUP_DIR:-/opt/exchange/backups/postgres}"
KEEP_DAYS="${KEEP_DAYS:-7}"
mkdir -p "$BACKUP_DIR"
# shellcheck disable=SC1091
set -a; . "$INFRA_DIR/.env"; set +a
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
out="$BACKUP_DIR/${POSTGRES_DB}-${stamp}.dump"
sudo docker compose -f "$INFRA_DIR/docker-compose.yml" --env-file "$INFRA_DIR/.env" exec -T \
  -e PGPASSWORD="$POSTGRES_PASSWORD" postgres \
  pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc --no-owner > "$out"
chmod 600 "$out"
find "$BACKUP_DIR" -name '*.dump' -mtime +"$KEEP_DAYS" -delete
echo "backup written: $out ($(du -h "$out" | cut -f1)); kept: $(ls "$BACKUP_DIR" | wc -l)"
