#!/usr/bin/env bash
# Fetches a logo for every asset of the reference data and uploads it as the
# asset's profile logo (design 2026-10-02 §5.3), to be run ON THE TEST
# SERVER (it needs the internet and the instrument-service container):
#
#   bash fetch-logos.sh fetch  [DIR]   download + validate into DIR (default
#                                      /opt/exchange/infra/logos), write
#                                      DIR/report.txt; nothing is uploaded
#   bash fetch-logos.sh upload [DIR]   upload every validated logo in DIR
#                                      through exchangectl (only assets
#                                      without a logo, or whose logo came
#                                      from an earlier run of this script)
#   bash fetch-logos.sh all    [DIR]   both
#
# Sources, in order: the CC0 icon set spothq/cryptocurrency-icons (SVG,
# flat colour), then CoinGecko's coin images (PNG, the project's own
# artwork) matched by symbol among the top 1,000 coins by market cap. The
# platform coin ASTRA is skipped: its logo is deploy/instruments/astra.svg
# (scripts/ops/astra.sh profile).
set -euo pipefail

INFRA=/opt/exchange/infra
DIR=${2:-$INFRA/logos}
COMPOSE="sudo docker compose -f $INFRA/docker-compose.yml -f $INFRA/docker-compose.apps.yml"
ICONS=https://raw.githubusercontent.com/spothq/cryptocurrency-icons/master/svg/color
GECKO=https://api.coingecko.com/api/v3
MAX=204800
REASON="default logos from the CC0 icon set / CoinGecko (deploy/instruments/fetch-logos.sh)"

# icon_name SYMBOL: the icon set's file name for an asset (1000PEPE -> pepe).
icon_name() {
  local s; s=$(printf '%s' "$1" | sed -E 's/^1000+//' | tr '[:upper:]' '[:lower:]')
  case "$s" in
    pol) echo matic ;;      # Polygon's token kept its old icon
    render) echo rndr ;;
    *) echo "$s" ;;
  esac
}

# gecko_symbol SYMBOL: the symbol CoinGecko lists an asset under.
gecko_symbol() { printf '%s' "$1" | sed -E 's/^1000+//' | tr '[:upper:]' '[:lower:]'; }

assets() {
  curl -sS -m 20 https://astras.vip/v1/market/assets | python3 -c '
import json, sys
for a in json.load(sys.stdin)["assets"]:
    if a["asset_code"] != "ASTRA": print(a["asset_code"])' | sort -u
}

# normalize IN OUT: any raster image (PNG, JPEG, WebP) becomes a square PNG
# of at most 256 px, padded with transparency (needs python3-pil).
normalize() {
  python3 - "$1" "$2" <<'EOF'
import sys
from PIL import Image
im = Image.open(sys.argv[1]).convert("RGBA")
w, h = im.size
side = max(w, h)
canvas = Image.new("RGBA", (side, side), (0, 0, 0, 0))
canvas.paste(im, ((side - w) // 2, (side - h) // 2))
if side > 256:
    canvas = canvas.resize((256, 256), Image.LANCZOS)
canvas.save(sys.argv[2], "PNG", optimize=True)
print(canvas.size[0], canvas.size[1])
EOF
}

# svg_ok FILE: an <svg> root with a viewBox or width/height, no script.
svg_ok() { grep -q '<svg' "$1" && ! grep -qi '<script' "$1" && grep -qE 'viewBox|width=' "$1"; }

fetch() {
  mkdir -p "$DIR"
  : >"$DIR/report.txt"
  echo "== CoinGecko top 1,000 by market cap (4 pages)"
  : >"$DIR/gecko.json"
  for page in 1 2 3 4; do
    # The free API answers 429 when polled too fast: a few tries, 15 s apart.
    for try in 1 2 3; do
      code=$(curl -sS -m 30 -A 'astras-logo-fetch/1.0' -o "$DIR/gecko.page" -w '%{http_code}' \
        "$GECKO/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=250&page=$page" || echo 000)
      if [ "$code" = 200 ] && head -c 1 "$DIR/gecko.page" | grep -q '\['; then
        cat "$DIR/gecko.page" >>"$DIR/gecko.json"; echo >>"$DIR/gecko.json"; break
      fi
      echo "  page $page: HTTP $code (try $try)"; sleep 15
    done
    sleep 6
  done
  rm -f "$DIR/gecko.page"
  python3 - "$DIR/gecko.json" "$DIR/gecko.tsv" <<'EOF'
import json, sys
seen = {}
with open(sys.argv[1]) as f:
    for line in f:
        line = line.strip()
        if not line: continue
        try:
            coins = json.loads(line)
        except ValueError:
            continue
        if not isinstance(coins, list):
            continue
        for c in coins:
            s = (c.get("symbol") or "").lower()
            if s and s not in seen and c.get("image"):
                seen[s] = (c["id"], c["image"].replace("/thumb/", "/large/").replace("/small/", "/large/"))
with open(sys.argv[2], "w") as out:
    for s, (i, u) in seen.items():
        out.write(f"{s}\t{i}\t{u}\n")
print(len(seen), "symbols")
EOF
  local ok=0 miss=0
  for code in $(assets); do
    local name file src
    name=$(icon_name "$code")
    file="$DIR/$code.svg"
    if curl -sS -m 20 -f -o "$file" "$ICONS/$name.svg" 2>/dev/null && svg_ok "$file" && [ "$(stat -c %s "$file")" -le "$MAX" ]; then
      echo "$code	svg	icons	$(stat -c %s "$file")" >>"$DIR/report.txt"; ok=$((ok+1)); continue
    fi
    rm -f "$file"
    src=$(awk -F'\t' -v s="$(gecko_symbol "$code")" '$1==s {print $3; exit}' "$DIR/gecko.tsv")
    file="$DIR/$code.png"
    local raw="$DIR/$code.src"
    if [ -n "$src" ] && curl -sS -m 20 -f -o "$raw" "$src" 2>/dev/null; then
      local wh; wh=$(normalize "$raw" "$file" 2>&1 | tail -1)
      if [ -s "$file" ] && [ "$(stat -c %s "$file")" -le "$MAX" ]; then
        echo "$code	png	coingecko	$(stat -c %s "$file")	$src" >>"$DIR/report.txt"; ok=$((ok+1)); rm -f "$raw"; sleep 1; continue
      fi
      echo "$code	bad	coingecko	size=$(stat -c %s "$raw") normalize=${wh:-?}	$src" >>"$DIR/report.txt"
    else
      echo "$code	missing	-	-" >>"$DIR/report.txt"
    fi
    rm -f "$file" "$raw"; miss=$((miss+1))
  done
  echo "== fetched $ok, missing/bad $miss; see $DIR/report.txt"
  grep -v -E '	(svg|png)	' "$DIR/report.txt" || true
}

upload() {
  local n=0
  while IFS=$'\t' read -r code kind _rest; do
    case "$kind" in svg|png) ;; *) continue ;; esac
    local file mime
    if [ "$kind" = svg ]; then file="$DIR/$code.svg"; mime=image/svg+xml; else file="$DIR/$code.png"; mime=image/png; fi
    [ -s "$file" ] || continue
    # Keep an existing logo (an operator may have uploaded it in the
    # console) unless FORCE=1 asks to replace every one.
    local cur; cur=$($COMPOSE exec -T instrument-service /app/exchangectl instruments profile "$code" </dev/null 2>/dev/null || true)
    if [ "${FORCE:-}" != 1 ] && printf '%s' "$cur" | grep -q '"logo_size": [1-9]'; then
      echo "skip $code: already has a logo (FORCE=1 replaces)"; continue
    fi
    if $COMPOSE exec -T instrument-service /app/exchangectl instruments profile "$code" --logo - --logo-type "$mime" --reason "$REASON" <"$file" >/dev/null; then
      echo "uploaded $code ($kind)"; n=$((n+1))
    else
      echo "FAILED $code ($kind)"
    fi
  done <"$DIR/report.txt"
  echo "== uploaded $n"
}

case "${1:-}" in
  fetch) fetch ;;
  upload) upload ;;
  all) fetch; upload ;;
  *) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
