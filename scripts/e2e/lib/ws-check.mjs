// WebSocket end-to-end check, run by scripts/e2e/gateway.sh with Node 22+
// (built-in WebSocket and fetch):
//   node ws-check.mjs <base URL> <access token> <email> <password>
// It subscribes to the private channels, makes a transfer and a new-device
// login over REST, and waits for the matching pushes.
const [base, token, email, password] = process.argv.slice(2);
const wsURL = base.replace(/^http/, "ws") + "/v1/ws";
const deadline = (ms, what) => new Promise((_, reject) => setTimeout(() => reject(new Error("timeout: " + what)), ms));
const ok = (what) => console.log("ok   " + what);

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

await Promise.race([new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; }), deadline(10000, "connect")]);
ok("connected to " + wsURL);
ws.send(JSON.stringify({ op: "subscribe", args: ["balances"] }));
let m = await next((m) => m.op === "subscribe", "subscribe reply");
if (m.ok || m.code !== "COMMON_UNAUTHORIZED") throw new Error("private channels need auth: " + JSON.stringify(m));
ok("private channels need auth");
inbox.length = 0;
ws.send(JSON.stringify({ op: "auth", token }));
m = await next((m) => m.op === "auth", "auth reply");
if (!m.ok) throw new Error("auth: " + JSON.stringify(m));
ok("authenticated as " + m.user_id);
ws.send(JSON.stringify({ op: "subscribe", args: ["balances", "notifications"] }));
m = await next((m) => m.op === "subscribe" && m.ok, "subscribe ok");
ok("subscribed to balances and notifications");

const res = await fetch(base + "/v1/account/transfers", {
  method: "POST",
  headers: { "Content-Type": "application/json", Authorization: "Bearer " + token, "Idempotency-Key": "ws-" + Date.now() },
  body: JSON.stringify({ asset: "USDT", amount: "12.5", from_account_type: "SPOT", to_account_type: "FUTURES" }),
});
if (res.status !== 201) throw new Error("transfer: " + res.status + " " + (await res.text()));
const futures = await next((m) => m.channel === "balances" && m.data.account_type === "FUTURES" && m.data.asset === "USDT", "futures balance push");
const spot = await next((m) => m.channel === "balances" && m.data.account_type === "SPOT" && m.data.asset === "USDT", "spot balance push");
if (futures.data.entry_type !== "ACCOUNT_TRANSFER" || spot.data.entry_type !== "ACCOUNT_TRANSFER" || !(futures.seq > 0) || spot.seq === futures.seq) {
  throw new Error("balance pushes: " + JSON.stringify([futures, spot]));
}
ok(`balance pushes after the transfer (seq ${spot.seq}, ${futures.seq})`);

const login = await fetch(base + "/v1/auth/login/password", {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-Client-Type": "APP" },
  body: JSON.stringify({ identifier: email, password, device_id: "ws-check-" + Date.now() }),
});
if (login.status !== 200) throw new Error("login: " + login.status + " " + (await login.text()));
const notice = await next((m) => m.channel === "notifications" && m.data.type === "NEW_DEVICE_LOGIN", "new-device notice push", 30000);
ok("notification push: " + notice.data.title);
ws.close();
console.log("all websocket checks passed");
process.exit(0);
