// Market data end-to-end check, run by scripts/e2e/marketdata.sh with Node
// 22+ (built-in WebSocket and fetch):
//   node md-check.mjs <base URL> <seller token> <buyer token>
// Public channels need no sign-in; the buyer also follows orders and fills.
// The seller rests an ask, the buyer takes it, and every channel and REST
// endpoint must show the trade.
const [base, sellerToken, buyerToken] = process.argv.slice(2);
const symbol = "ETH-BTC";
const price = "0.0402";
const qty = "0.01";
const wsURL = base.replace(/^http/, "ws") + "/v1/ws";
const deadline = (ms, what) => new Promise((_, reject) => setTimeout(() => reject(new Error("timeout: " + what)), ms));
const ok = (what) => console.log("ok   " + what);
const fail = (what, v) => { throw new Error(what + ": " + JSON.stringify(v)); };

const ws = new WebSocket(wsURL);
const inbox = [];
let waiters = [];
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.op === "ping") { ws.send(JSON.stringify({ op: "pong" })); return; }
  inbox.push(m);
  waiters = waiters.filter((w) => !w(m));
};
const next = (pred, what, ms = 15000) => {
  const found = inbox.find(pred);
  if (found) return Promise.resolve(found);
  return Promise.race([new Promise((resolve) => waiters.push((m) => (pred(m) ? (resolve(m), true) : false))), deadline(ms, what)]);
};
const api = async (method, path, token, body) => {
  const headers = { "Content-Type": "application/json" };
  if (token) headers.Authorization = "Bearer " + token;
  const res = await fetch(base + path, { method, headers, body: body && JSON.stringify(body) });
  return { status: res.status, body: await res.json().catch(() => null) };
};
const eventually = async (what, fn, ms = 10000) => {
  const until = Date.now() + ms;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > until) throw new Error("timeout: " + what);
    await new Promise((r) => setTimeout(r, 300));
  }
};
const level = (levels, p) => levels.find((l) => l[0] === p);

await Promise.race([new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; }), deadline(10000, "connect")]);
ws.send(JSON.stringify({ op: "subscribe", args: [`depth:${symbol}`, `trades:${symbol}`, `ticker:${symbol}`, `candles:${symbol}:1m`] }));
let m = await next((m) => m.op === "subscribe", "public subscribe reply");
if (!m.ok) fail("public channels need no sign-in", m);
const snapshot = await next((m) => m.channel === `depth:${symbol}` && m.type === "snapshot", "depth snapshot");
if (!Array.isArray(snapshot.data.bids) || !Array.isArray(snapshot.data.asks)) fail("depth snapshot", snapshot);
ok(`public channels without sign-in; depth snapshot at seq ${snapshot.seq ?? 0}`);

ws.send(JSON.stringify({ op: "auth", token: buyerToken }));
m = await next((m) => m.op === "auth", "auth reply");
if (!m.ok) fail("auth", m);
ws.send(JSON.stringify({ op: "subscribe", args: ["orders", "fills"] }));
await next((m) => m.op === "subscribe" && m.ok && m.args.includes("orders"), "private subscribe");
ok("the buyer follows orders and fills");

let r = await api("POST", "/v1/orders", sellerToken, { symbol, side: "SELL", type: "LIMIT", price, quantity: qty });
if (r.status !== 202) fail("the seller's ask", r);
const ask = await next((m) => m.channel === `depth:${symbol}` && m.type === "update" && level(m.data.asks, price), "depth update with the ask");
if (level(ask.data.asks, price)[1] !== qty || ask.prev_seq !== (ask.seq - 1 || undefined)) fail("depth update", ask);
ok(`the ask shows in a depth update (seq ${ask.seq})`);

r = await api("POST", "/v1/orders", buyerToken, { symbol, side: "BUY", type: "LIMIT", price, quantity: qty });
if (r.status !== 202) fail("the buyer's order", r);
const orderID = r.body.order_id;
await next((m) => m.channel === "orders" && m.data.order_id === orderID && m.data.status === "NEW", "orders: NEW");
const filled = await next((m) => m.channel === "orders" && m.data.order_id === orderID && m.data.status === "FILLED", "orders: FILLED");
if (filled.data.filled_quantity !== qty || !(filled.seq > 0)) fail("orders FILLED", filled);
ok("orders: NEW, then FILLED");
const fill = await next((m) => m.channel === "fills" && m.data.order_id === orderID, "fills");
if (fill.data.role !== "TAKER" || fill.data.price !== price || fill.data.fee !== "0.00001" || fill.data.fee_asset !== "ETH") fail("fill", fill);
ok("fills: taker at 0.0402, fee 0.00001 ETH");
const trade = await next((m) => m.channel === `trades:${symbol}` && m.data.price === price && m.data.taker_side === "BUY", "public trade");
if (!(trade.data.trade_number > 0)) fail("public trade", trade);
ok(`trades: #${trade.data.trade_number} at 0.0402, taker BUY`);
await next((m) => m.channel === `depth:${symbol}` && m.type === "update" && level(m.data.asks, price)?.[1] === "0", "depth update removing the ask");
ok("the taken ask leaves the book (quantity 0)");
await next((m) => m.channel === `ticker:${symbol}` && m.data.last === price && m.data.trade_count > 0, "ticker with the trade");
ok("ticker: last 0.0402");
await next((m) => m.channel === `candles:${symbol}:1m` && m.data.close === price && m.data.trade_count > 0, "1m candle with the trade");
ok("candles:1m: close 0.0402");

const trades = await eventually("REST trades", async () => {
  const r = await api("GET", `/v1/market/${symbol}/trades?limit=1`);
  return r.status === 200 && r.body.trades[0]?.trade_id === trade.data.trade_id ? r.body : null;
});
if (trades.trades[0].trade_number !== trade.data.trade_number || trades.trades[0].quote_quantity !== "0.000402") fail("REST trades", trades);
ok("REST trades: the same trade, 0.000402 BTC");
const ticker = await api("GET", `/v1/market/${symbol}/ticker`);
if (ticker.status !== 200 || ticker.body.last !== price || !(Number(ticker.body.trade_count) > 0)) fail("REST ticker", ticker);
ok("REST ticker: last 0.0402");
const candles = await api("GET", `/v1/market/${symbol}/candles?interval=1m&limit=3`);
const lastCandle = candles.body?.candles?.at(-1);
if (candles.status !== 200 || lastCandle?.close !== price || lastCandle.closed) fail("REST candles", candles);
ok("REST candles: the open 1m candle closes at 0.0402");
const depth = await eventually("REST depth without the ask", async () => {
  const r = await api("GET", `/v1/market/${symbol}/depth?limit=50`);
  return r.status === 200 && r.body.sequence > 0 && !level(r.body.asks, price) ? r.body : null;
});
ok(`REST depth at sequence ${depth.sequence}, the ask gone`);
ws.close();
console.log("all market data checks passed");
