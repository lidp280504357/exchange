import { refresh } from "../api/client";
import { useSession } from "../store/session";

// Push is a private event (§7.3).
export type Push = { channel: string; seq: number; data: Record<string, string> };

type Listener = (p: Push) => void;

// PrivateSocket keeps one WebSocket to /v1/ws: it authenticates with the
// current access token, subscribes to the private channels and, after a
// reconnect, asks for what it missed with last_seq.
export class PrivateSocket {
  private ws: WebSocket | null = null;
  private lastSeq = 0;
  private listeners = new Set<Listener>();
  private retry = 0;
  private stopped = false;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private channels: string[]) {}

  on(l: Listener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    clearTimeout(this.timer);
    this.ws?.close();
    this.ws = null;
  }

  // reauth sends a fresh token after a refresh.
  reauth(token: string) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify({ op: "auth", token }));
  }

  private connect() {
    const token = useSession.getState().session?.accessToken;
    if (!token || this.stopped) return;
    const url = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/v1/ws`;
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 0;
      ws.send(JSON.stringify({ op: "auth", token }));
    };
    ws.onmessage = (ev) => {
      const m = JSON.parse(String(ev.data));
      if (m.op === "ping") ws.send(JSON.stringify({ op: "pong" }));
      else if (m.op === "auth" && m.ok) {
        const sub: Record<string, unknown> = { op: "subscribe", args: this.channels };
        if (this.lastSeq > 0) sub.last_seq = this.lastSeq;
        ws.send(JSON.stringify(sub));
      } else if ((m.op === "auth" && m.code === "AUTH_TOKEN_EXPIRED") || (m.op === "error" && m.code === "AUTH_TOKEN_EXPIRED")) {
        // The token expired (or the account status changed): refresh it
        // and authenticate again within the server's 60-second grace.
        void refresh().then((s) => s && this.reauth(s.accessToken));
      } else if (m.op === "resync") {
        this.emit({ channel: "resync", seq: this.lastSeq, data: {} });
      } else if (typeof m.channel === "string") {
        if (m.seq <= this.lastSeq) return;
        this.lastSeq = m.seq;
        this.emit(m as Push);
      }
    };
    ws.onclose = () => {
      if (this.stopped || this.ws !== ws) return;
      const wait = Math.min(30000, 1000 * 2 ** this.retry++);
      this.timer = setTimeout(() => this.connect(), wait);
    };
  }

  private emit(p: Push) {
    this.listeners.forEach((l) => l(p));
  }
}
