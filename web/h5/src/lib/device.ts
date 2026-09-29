// A random ID the browser keeps for good (§6.1: every write carries it).
const KEY = "exchange.device_id";

export function deviceId(): string {
  let id = localStorage.getItem(KEY);
  if (!id) {
    id = "web-" + crypto.randomUUID();
    localStorage.setItem(KEY, id);
  }
  return id;
}
