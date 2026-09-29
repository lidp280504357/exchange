import { compare } from "./decimal";

// Public market channels (§7.3): ticker:, depth:, trades: and candles:,
// over one WebSocket that needs no sign-in.

// MarketMessage is a public push. Depth pushes carry their own seq: a
// snapshot, then updates whose prev_seq is the seq before them.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type MarketMessage = { channel: string; type?: string; seq?: number; prev_seq?: number; data: any };

type Listener = (m: MarketMessage) => void;

// MarketSocket keeps one connection while any channel has a listener and
// subscribes again after a reconnect.
export class MarketSocket {
  private ws: WebSocket | null = null;
  private listeners = new Map<string, Set<Listener>>();
  private retry = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  // subscribe adds a listener to channel and returns the unsubscribe.
  subscribe(channel: string, l: Listener): () => void {
    let set = this.listeners.get(channel);
    if (!set) {
      set = new Set();
      this.listeners.set(channel, set);
      this.send({ op: "subscribe", args: [channel] });
    }
    set.add(l);
    if (!this.ws) this.connect();
    return () => {
      const s = this.listeners.get(channel);
      if (!s) return;
      s.delete(l);
      if (s.size === 0) {
        this.listeners.delete(channel);
        this.send({ op: "unsubscribe", args: [channel] });
      }
      if (this.listeners.size === 0) this.close();
    };
  }

  // resubscribe asks for a fresh depth snapshot after a gap.
  resubscribe(channel: string) {
    this.send({ op: "unsubscribe", args: [channel] });
    this.send({ op: "subscribe", args: [channel] });
  }

  private send(msg: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  }

  private connect() {
    const url = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/v1/ws`;
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => {
      this.retry = 0;
      if (this.listeners.size > 0) ws.send(JSON.stringify({ op: "subscribe", args: [...this.listeners.keys()] }));
    };
    ws.onmessage = (ev) => {
      const m = JSON.parse(String(ev.data));
      if (m.op === "ping") ws.send(JSON.stringify({ op: "pong" }));
      else if (typeof m.channel === "string") this.listeners.get(m.channel)?.forEach((l) => l(m as MarketMessage));
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.listeners.size === 0) return;
      const wait = Math.min(30000, 1000 * 2 ** this.retry++);
      this.timer = setTimeout(() => this.connect(), wait);
    };
  }

  private close() {
    clearTimeout(this.timer);
    const ws = this.ws;
    this.ws = null;
    ws?.close();
  }
}

export const marketSocket = new MarketSocket();

export type Level = [string, string];
export type Book = { seq: number; bids: Level[]; asks: Level[] };

// applyDepth returns the book after a depth push, or null when an update
// does not follow the book (the caller resubscribes for a snapshot).
export function applyDepth(book: Book | null, m: MarketMessage): Book | null {
  if (m.type === "snapshot") return { seq: m.seq ?? 0, bids: m.data.bids, asks: m.data.asks };
  if (!book || (m.prev_seq ?? 0) !== book.seq) return null;
  return { seq: m.seq ?? 0, bids: merge(book.bids, m.data.bids, -1), asks: merge(book.asks, m.data.asks, 1) };
}

// merge applies changed levels (quantity "0" removes one) and keeps the
// best price first: dir -1 sorts high to low (bids), 1 low to high.
function merge(levels: Level[], changes: Level[], dir: 1 | -1): Level[] {
  const byPrice = new Map(levels.map((l) => [l[0], l[1]]));
  for (const [price, qty] of changes) {
    if (/^0(\.0+)?$/.test(qty)) byPrice.delete(price);
    else byPrice.set(price, qty);
  }
  return [...byPrice.entries()].sort((a, b) => dir * compare(a[0], b[0]));
}

// scale splits a non-negative decimal string into digits and scale.
function scale(v: string): [bigint, number] {
  const [int = "0", frac = ""] = v.split(".");
  return [BigInt(int + frac || "0"), frac.length];
}

// fromScaled writes digits at scale back as a trimmed decimal string.
function fromScaled(digits: bigint, sc: number): string {
  const neg = digits < 0n;
  let s = (neg ? -digits : digits).toString().padStart(sc + 1, "0");
  if (sc > 0) s = s.slice(0, -sc) + "." + s.slice(-sc);
  s = s.includes(".") ? s.replace(/\.?0+$/, "") : s;
  return (neg ? "-" : "") + s;
}

// mul multiplies two non-negative decimal strings exactly.
export function mul(a: string, b: string): string {
  const [da, sa] = scale(a);
  const [db, sb] = scale(b);
  return fromScaled(da * db, sa + sb);
}

// decimalsOf counts the decimals of a step such as "0.0001".
export function decimalsOf(step: string): number {
  const frac = step.split(".")[1] ?? "";
  return frac.replace(/0+$/, "").length;
}

// percent renders a fraction such as "0.01253" as "+1.25%" (cut, not
// rounded, to two decimals).
export function percent(fraction: string | null | undefined): string {
  if (!fraction) return "—";
  const neg = fraction.startsWith("-");
  const [int = "0", frac = ""] = fraction.replace(/^[-+]/, "").split(".");
  const f = frac.padEnd(4, "0");
  const whole = (BigInt(int) * 100n + BigInt(f.slice(0, 2))).toString();
  const out = `${whole}.${f.slice(2, 4)}`;
  const zero = /^0\.00$/.test(out);
  return `${zero ? "" : neg ? "-" : "+"}${out}%`;
}
