// The "tickers" channel, run by scripts/e2e/marketdata.sh with Node 22+:
//   node tickers-check.mjs <base URL>
// One subscription brings every symbol's ticker, then only the changed
// ones about once a second: each a newer ticker than the symbol's last
// (every pair follows Binance, whose tickers move about once a second, so
// one update may carry most of them). BTC-USDT changes every second.
const [base] = process.argv.slice(2);
const ws = new WebSocket(base.replace(/^http/, "ws") + "/v1/ws");
const timeout = (ms, what) => new Promise((_, reject) => setTimeout(() => reject(new Error("timeout: " + what)), ms));
const messages = [];
let wake = () => {};
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.op === "ping") { ws.send(JSON.stringify({ op: "pong" })); return; }
  messages.push({ at: Date.now(), m });
  wake();
};
// until waits for a message after index from that pred accepts.
const until = async (from, pred, what, ms = 10000) => {
  const deadline = Date.now() + ms;
  for (;;) {
    const i = messages.findIndex((x, j) => j >= from && pred(x.m));
    if (i >= 0) return i;
    if (Date.now() > deadline) throw new Error("timeout: " + what);
    await Promise.race([new Promise((r) => { wake = r; }), new Promise((r) => setTimeout(r, 200))]);
  }
};
try {
  await Promise.race([new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; }), timeout(10000, "connect")]);
  ws.send(JSON.stringify({ op: "subscribe", args: ["tickers"] }));
  const r = await until(0, (m) => m.op === "subscribe", "subscribe reply");
  if (!messages[r].m.ok) throw new Error("tickers needs no sign-in: " + JSON.stringify(messages[r].m));
  const s = await until(r, (m) => m.channel === "tickers" && m.type === "snapshot", "snapshot");
  const symbols = messages[s].m.data.map((t) => t.symbol);
  if (!symbols.includes("BTC-USDT") || !symbols.includes("ETH-BTC")) throw new Error("snapshot symbols: " + symbols);
  console.log(`ok   tickers: a snapshot of ${symbols.length} symbols`);
  // Four updates with BTC-USDT: the gaps between them are about a second.
  let at = s;
  const stamps = [];
  for (let n = 0; n < 4; n++) {
    at = await until(at + 1, (m) => m.channel === "tickers" && m.type === "update" && m.data.some((t) => t.symbol === "BTC-USDT"), "an update");
    stamps.push(messages[at].at);
  }
  // From the second update on, an update carries only tickers newer than
  // the symbol's last (the first may repeat ones that changed just before
  // the snapshot went out).
  const updates = messages.slice(s + 1, at + 1).filter(({ m }) => m.channel === "tickers" && m.type === "update");
  const last = new Map(messages[s].m.data.map((t) => [t.symbol, t.updated_at]));
  updates.forEach(({ m }, i) => {
    for (const t of m.data) {
      const prev = last.get(t.symbol);
      if (i > 0 && prev && !(Date.parse(t.updated_at) > Date.parse(prev))) throw new Error(`an update repeats ${t.symbol} as of ${t.updated_at}`);
      last.set(t.symbol, t.updated_at);
    }
  });
  const gaps = stamps.slice(1).map((t, i) => t - stamps[i]);
  if (gaps.some((g) => g < 500 || g > 3000)) throw new Error("update gaps " + gaps.join(", ") + " ms");
  console.log(`ok   tickers: only changed symbols, about once a second (${gaps.join(", ")} ms apart)`);
  ws.close();
  process.exit(0);
} catch (e) {
  console.error("FAIL " + e.message);
  process.exit(1);
}
