#!/usr/bin/env bash
# Perpetual contracts end to end (implementation plan §7.3): the contract
# specifications (task 1), the index price, mark price and funding (task
# 3) over REST and WebSocket, and every contract's public book: Binance
# futures' (ADR-0015), which HOUSE offers. Every linear contract goes to
# 125x on the same seven-tier ladder; the coin-margined ones (design
# 2026-10-06 §2.1, listed with ?margin_type=COIN) carry Binance COIN-M's
# face values and ladders. The mark price needs the reference feed (flag
# market.reference_feed, on in the test environment); the books need
# market.reference_depth to allow the contracts.
#
#   scripts/e2e/contracts.sh
set -euo pipefail

# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"

echo "== specifications"
call GET /v1/market/contracts ""
expect 200 - "contracts"
check '[.contracts[].symbol] | contains(["BTC-USDT-PERP","ETH-USDT-PERP"])' "seeded contracts listed"
check 'all(.contracts[]; .max_leverage == 125 and .risk_tiers[0].max_leverage == 125)' "every contract goes to 125x"
call GET /v1/market/contracts/btc-usdt-perp ""
expect 200 - "one contract (symbol is case-insensitive)"
check '.index_symbol == "BTC-USDT" and .funding_interval_hours == 8' "its index and funding interval"
check '[.risk_tiers[] | [.max_notional, .max_leverage, .mmr]] == [["50000",125,"0.004"],["250000",100,"0.005"],["1000000",50,"0.01"],["5000000",20,"0.025"],["20000000",10,"0.05"],["50000000",5,"0.1"],["100000000",2,"0.125"]]' "the risk limit ladder: 125x to 50,000 USDT, down to 2x"
call GET /v1/market/contracts/BTC-USDT ""
expect 404 COMMON_NOT_FOUND "a pair is not a contract"

echo "== coin-margined contracts (design 2026-10-06 §2.1, G0)"
call GET /v1/market/contracts ""
check 'all(.contracts[]; .margin_type == "USDT" and .settle_asset == .quote_asset and .contract_size == "0")' "the default list keeps to the linear contracts, settled in their quote asset"
call GET "/v1/market/contracts?margin_type=COIN" ""
expect 200 - "the coin-margined contracts"
check '[.contracts[].symbol] | sort == ["ASTRA-USD-PERP","BTC-USD-PERP","ETH-USD-PERP"]' "BTC, ETH and ASTRA, as the linear ones"
check 'all(.contracts[]; .margin_type == "COIN" and .quote_asset == "USD" and .settle_asset == .base_asset and .lot_size == "1" and .index_symbol == (.base_asset + "-USDT"))' "quoted in USD, settled in their coin, whole contracts, the USDT index"
check '[.contracts[] | [.symbol, .contract_size]] | sort == [["ASTRA-USD-PERP","10"],["BTC-USD-PERP","100"],["ETH-USD-PERP","10"]]' "face values of 100 and 10 USD"
call GET "/v1/market/contracts?margin_type=ALL" ""
check '(.contracts | length) >= 6 and ([.contracts[].margin_type] | unique == ["COIN","USDT"])' "ALL lists both kinds"
call GET "/v1/market/contracts?margin_type=USDC" ""
expect 400 COMMON_INVALID_ARGUMENT "an unknown margin type"
call GET /v1/market/contracts/btc-usd-perp ""
expect 200 - "one coin-margined contract by its symbol"
check '.reference_symbol == "BTCUSD_PERP" and (.risk_tiers | length) == 10 and .max_leverage == 125 and .risk_tiers[0].max_notional == "5"' "Binance COIN-M's ladder: 125x to 5 BTC"
call GET /v1/market/contracts/ETH-USD-PERP ""
check '.max_leverage == 100 and .risk_tiers[0].max_notional == "15" and .tick_size == "0.01"' "ETH's: 100x to 15 ETH, the linear contract's tick"

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

echo "== the contracts show the reference market's book (docs/runbook/market-data.md)"
for c in BTC-USDT-PERP ETH-USDT-PERP; do
  quoted() {
    call GET "/v1/market/$c/depth?limit=5" "" && [[ $STATUS == 200 ]] &&
      jq -e '(.bids | length) > 0 and (.asks | length) > 0' <<<"$BODY"
  }
  eventually 30 "bids and asks on $c's book" quoted
  check '((.asks[0][0] | tonumber) - (.bids[0][0] | tonumber)) / (.bids[0][0] | tonumber) | . > 0 and . < 0.01' "a spread under 1% around the mark"
done

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
