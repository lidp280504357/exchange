#!/usr/bin/env bash
# Perpetual contracts end to end (implementation plan §7.3): the contract
# specifications (task 1), the index price, mark price and funding (task
# 3) over REST and WebSocket, and the market maker's quotes on
# BTC-USDT-PERP (requirements §11.10). The mark price needs the reference
# feed (flag market.reference_feed, on in the test environment); the
# quotes need market.maker to allow BTC-USDT-PERP and USDT in the market
# maker's FUTURES account.
#
#   scripts/e2e/contracts.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

echo "== specifications"
call GET /v1/market/contracts ""
expect 200 - "contracts"
check '[.contracts[].symbol] | contains(["BTC-USDT-PERP","ETH-USDT-PERP"])' "seeded contracts listed"
call GET /v1/market/contracts/btc-usdt-perp ""
expect 200 - "one contract (symbol is case-insensitive)"
check '.index_symbol == "BTC-USDT" and .max_leverage == 50 and (.risk_tiers | length) == 4 and .risk_tiers[0].mmr == "0.004" and .funding_interval_hours == 8' "risk limit tiers and funding interval"
call GET /v1/market/contracts/BTC-USDT ""
expect 404 COMMON_NOT_FOUND "a pair is not a contract"

echo "== mark price and funding"
marked() {
  call GET /v1/market/BTC-USDT-PERP/mark-price "" && [[ $STATUS == 200 ]] &&
    jq -e '.mark_price != null and .degraded == false' <<<"$BODY"
}
eventually 30 "BTC-USDT-PERP has a mark price" marked
check '.index_symbol == "BTC-USDT" and (.next_funding_time | test("T(00|08|16):00:00Z$"))' "the next settlement is on the 8-hour grid"
check '((.mark_price | tonumber) - (.index_price | tonumber)) / (.index_price | tonumber) | . >= -0.01 and . <= 0.01' "the mark price is within 1% of the index"
check '(.funding_rate | tonumber) | . >= -0.0075 and . <= 0.0075' "the funding estimate is within the cap"
call GET /v1/market/BTC-USDT/mark-price ""
expect 404 COMMON_NOT_FOUND "a pair has no mark price"
call GET "/v1/market/BTC-USDT-PERP/funding-rates?limit=5" ""
expect 200 - "settled funding rates"
check '(.funding_rates | length) <= 5 and all(.funding_rates[]; (.funding_time | test(":00:00Z$")) and .samples >= 0)' "one per period"
call GET "/v1/market/BTC-USDT-PERP/funding-rates?from=soon" ""
expect 400 COMMON_INVALID_ARGUMENT "a time that is not RFC 3339"
call GET /v1/market/BTC-USDT-PERP/ticker ""
expect 200 - "contracts have tickers"
call GET /v1/market/tickers ""
check '[.tickers[].symbol] | index("BTC-USDT-PERP") != null' "the tickers include the contracts"

echo "== the market maker quotes BTC-USDT-PERP (docs/runbook/market-maker.md)"
quoted() {
  call GET "/v1/market/BTC-USDT-PERP/depth?limit=5" "" && [[ $STATUS == 200 ]] &&
    jq -e '(.bids | length) > 0 and (.asks | length) > 0' <<<"$BODY"
}
eventually 30 "bids and asks on the contract's book" quoted
check '((.asks[0][0] | tonumber) - (.bids[0][0] | tonumber)) / (.bids[0][0] | tonumber) | . > 0 and . < 0.01' "a spread under 1% around the mark"

echo "== WebSocket"
node - "$BASE" <<'EOF'
const [base] = process.argv.slice(2);
const ws = new WebSocket(base.replace(/^http/, "ws") + "/v1/ws");
const got = [];
const timeout = setTimeout(() => { console.error("FAIL no mark price and funding estimate within 15 s: " + JSON.stringify(got)); process.exit(1); }, 15000);
ws.onopen = () => ws.send(JSON.stringify({ op: "subscribe", args: ["mark-price:BTC-USDT-PERP", "funding:BTC-USDT-PERP"] }));
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.op === "ping") { ws.send(JSON.stringify({ op: "pong" })); return; }
  got.push(m);
  if (m.op === "subscribe" && !m.ok) { console.error("FAIL subscribe: " + ev.data); process.exit(1); }
  const mark = got.find((x) => x.channel === "mark-price:BTC-USDT-PERP" && Number(x.data.mark_price) > 0);
  const funding = got.find((x) => x.channel === "funding:BTC-USDT-PERP" && x.type === "estimate" && x.data.funding_rate !== undefined);
  if (mark && funding) {
    console.log("ok   mark-price: and funding: push without sign-in");
    clearTimeout(timeout);
    ws.close();
  }
};
EOF

echo "all contract checks passed"
