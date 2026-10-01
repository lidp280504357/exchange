// Market data end-to-end check, run by scripts/e2e/marketdata.sh with Node
// 22+ (built-in WebSocket and fetch):
//   node md-check.mjs <base URL> <buyer token>
// Public channels need no sign-in and show Binance's ETHBTC for ETH-BTC (a
// depth snapshot, then updates in sequence; ticker and 1m candles; live
// trades are watched on BTC-USDT, as ETHBTC may trade only every minute or
// two). The buyer follows orders and fills, buys at the market against
// HOUSE (every order trades against HOUSE; its own trades are not public),
// and REST agrees with the pushes.
const [base, buyerToken] = process.argv.slice(2);
const symbol = "ETH-BTC";
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

await Promise.race([new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; }), deadline(10000, "connect")]);
ws.send(JSON.stringify({ op: "subscribe", args: [`depth:${symbol}`, `trades:${symbol}`, "trades:BTC-USDT", `ticker:${symbol}`, `candles:${symbol}:1m`] }));
let m = await next((m) => m.op === "subscribe", "public subscribe reply");
if (!m.ok) fail("public channels need no sign-in", m);
const snapshot = await next((m) => m.channel === `depth:${symbol}` && m.type === "snapshot", "depth snapshot");
if (!(snapshot.data.bids?.length > 0) || !(snapshot.data.asks?.length > 0)) fail("a two-sided depth snapshot", snapshot);
ok(`public channels without sign-in; a two-sided depth snapshot at seq ${snapshot.seq ?? 0}`);
// Two updates in a row, each following the one before.
const first = await next((m) => m.channel === `depth:${symbol}` && m.type === "update", "a depth update");
const second = await next((m) => m.channel === `depth:${symbol}` && m.type === "update" && m.seq > first.seq, "another depth update");
if (second.prev_seq !== second.seq - 1 && second.prev_seq !== first.seq) fail("depth updates in sequence", { first, second });
ok(`depth updates in sequence (${first.seq}, ${second.seq})`);
const trade = await next((m) => m.channel === "trades:BTC-USDT" && Number(m.data.price) > 0 && m.data.trade_number > 0, "a public BTC-USDT trade");
ok(`trades: Binance's, BTC-USDT #${trade.data.trade_number} at ${trade.data.price}`);
const tick = await next((m) => m.channel === `ticker:${symbol}` && Number(m.data.last) > 0, "a ticker");
ok(`ticker: last ${tick.data.last}`);
await next((m) => m.channel === `candles:${symbol}:1m` && Number(m.data.close) > 0, "a 1m candle", 30000);
ok("candles:1m: the open candle");

ws.send(JSON.stringify({ op: "auth", token: buyerToken }));
m = await next((m) => m.op === "auth", "auth reply");
if (!m.ok) fail("auth", m);
ws.send(JSON.stringify({ op: "subscribe", args: ["orders", "fills"] }));
await next((m) => m.op === "subscribe" && m.ok && m.args.includes("orders"), "private subscribe");
ok("the buyer follows orders and fills");

const r = await api("POST", "/v1/orders", buyerToken, { symbol, side: "BUY", type: "MARKET", quote_amount: "0.0005" });
if (r.status !== 202) fail("the buyer's market buy", r);
const orderID = r.body.order_id;
await next((m) => m.channel === "orders" && m.data.order_id === orderID && m.data.status === "NEW", "orders: NEW");
const filled = await next((m) => m.channel === "orders" && m.data.order_id === orderID && m.data.status === "FILLED", "orders: FILLED");
if (!(Number(filled.data.filled_quantity) > 0) || !(Number(filled.data.filled_quote) <= 0.0005) || !(filled.seq > 0)) fail("orders FILLED", filled);
ok(`orders: NEW, then FILLED (${filled.data.filled_quantity} ETH for ${filled.data.filled_quote} BTC)`);
const fill = await next((m) => m.channel === "fills" && m.data.order_id === orderID, "fills");
const fee = (Number(fill.data.quantity) * 0.001).toFixed(8);
if (fill.data.role !== "TAKER" || fill.data.fee_asset !== "ETH" || Number(fill.data.fee).toFixed(8) !== fee) fail("fill", fill);
ok(`fills: taker against HOUSE at ${fill.data.price}, fee ${fill.data.fee} ETH (0.1%)`);

const fills = await api("GET", `/v1/orders/${orderID}/fills`, buyerToken);
if (fills.status !== 200 || !fills.body.fills.some((f) => f.trade_id === fill.data.trade_id)) fail("REST fills", fills);
ok("REST fills: the same fill");
const trades = await api("GET", `/v1/market/${symbol}/trades?limit=5`);
if (trades.status !== 200 || !(trades.body.trades.length > 0)) fail("REST trades", trades);
ok("REST trades: Binance's");
const ticker = await api("GET", `/v1/market/${symbol}/ticker`);
if (ticker.status !== 200 || !(Number(ticker.body.last) > 0)) fail("REST ticker", ticker);
ok(`REST ticker: last ${ticker.body.last}`);
const candles = await api("GET", `/v1/market/${symbol}/candles?interval=1m&limit=3`);
if (candles.status !== 200 || !(candles.body?.candles?.length > 0)) fail("REST candles", candles);
ok("REST candles: Binance's 1m candles");
const depth = await api("GET", `/v1/market/${symbol}/depth?limit=50`);
if (depth.status !== 200 || !(depth.body.sequence > 0) || !(depth.body.bids.length > 0 && depth.body.asks.length > 0)) fail("REST depth", depth);
ok(`REST depth at sequence ${depth.body.sequence}, two-sided`);
ws.close();
console.log("all market data checks passed");
