import { native } from "./native";

// A random ID the browser (or the desktop app's WebView) keeps for good
// (§6.1: every write carries it).
const KEY = "exchange.device_id";

export function deviceId(): string {
  let id = localStorage.getItem(KEY);
  if (!id) {
    id = (native ? "desktop-" : "web-") + crypto.randomUUID();
    localStorage.setItem(KEY, id);
  }
  return id;
}
