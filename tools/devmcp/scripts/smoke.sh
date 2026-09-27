#!/usr/bin/env bash
# 冒烟测试：按 MCP 协议顺序调用每个工具一次，打印文本结果。
# 用法：bash tools/devmcp/scripts/smoke.sh
set -euo pipefail
cd "$(dirname "$0")/.."

msgs=(
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
  '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remote_exec","arguments":{"command":"uptime; df -h / | tail -1; free -m | head -2"}}}'
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"compose","arguments":{"action":"ps"}}}'
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pg_query","arguments":{"sql":"select version(); \\dt"}}}'
  '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"redis_cmd","arguments":{"command":"INFO server"}}}'
  '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"ch_query","arguments":{"sql":"select version(), currentDatabase()"}}}'
)

(printf '%s\n' "${msgs[@]}"; sleep 40) | go run . 2>&1 | python3 -c '
import sys, json
for line in sys.stdin:
    try:
        m = json.loads(line)
    except Exception:
        continue
    r = m.get("result")
    if r and "content" in r:
        print("=== id", m["id"], "isError =", r.get("isError", False))
        print(r["content"][0]["text"].rstrip())
'
