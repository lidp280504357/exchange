// Records the private "deposits" and "balances" pushes of a user, one JSON
// line each, for scripts/e2e/deposit.sh (Node 22+, built-in WebSocket):
//   node deposit-watch.mjs <base URL> <access token>
// It runs until killed; the script reads the lines afterwards.
const [base, token] = process.argv.slice(2);
const ws = new WebSocket(base.replace(/^http/, "ws") + "/v1/ws");
ws.onopen = () => ws.send(JSON.stringify({ op: "auth", token }));
ws.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.op === "ping") ws.send(JSON.stringify({ op: "pong" }));
  else if (m.op === "auth") {
    if (!m.ok) throw new Error("auth: " + ev.data);
    ws.send(JSON.stringify({ op: "subscribe", args: ["deposits", "balances"] }));
  } else if (m.op === "subscribe") console.log(JSON.stringify({ subscribed: m.ok === true }));
  else if (m.channel) console.log(JSON.stringify(m));
};
ws.onerror = (e) => {
  console.error("websocket error: " + (e.message ?? e.type));
  process.exit(1);
};
ws.onclose = () => process.exit(0);
