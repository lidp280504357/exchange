import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WsClient, type SocketLike } from "./client";

// FakeSocket records what the client sends and lets a test play the server.
class FakeSocket implements SocketLike {
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: Record<string, unknown>[] = [];
  onopen: ((ev: unknown) => void) | null = null;
  onclose: ((ev: unknown) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  constructor(public url: string) {
    FakeSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.readyState = 3;
    this.onclose?.({});
  }
  open() {
    this.readyState = 1;
    this.onopen?.({});
  }
  push(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
  drop() {
    this.readyState = 3;
    this.onclose?.({});
  }
  ops(op: string) {
    return this.sent.filter((m) => m.op === op);
  }
}

const last = () => FakeSocket.all[FakeSocket.all.length - 1]!;

function client(opts: Partial<ConstructorParameters<typeof WsClient>[0]> = {}) {
  return new WsClient({
    url: "ws://test/v1/ws",
    connect: (u) => new FakeSocket(u),
    random: () => 0.5, // no jitter
    visible: () => true,
    ...opts,
  });
}

beforeEach(() => {
  FakeSocket.all = [];
  vi.useFakeTimers();
});
afterEach(() => vi.useRealTimers());

describe("WsClient", () => {
  it("shares one connection and unsubscribes 15 seconds after the last listener", () => {
    const ws = client();
    const a = vi.fn();
    const offA = ws.subscribe("ticker:BTC-USDT", a);
    const offB = ws.subscribe("ticker:BTC-USDT", vi.fn());
    ws.subscribe("trades:BTC-USDT", vi.fn());
    expect(FakeSocket.all).toHaveLength(1);
    last().open();
    expect(last().ops("subscribe")[0]!.args).toEqual(["ticker:BTC-USDT", "trades:BTC-USDT"]);
    last().push({ channel: "ticker:BTC-USDT", data: { last: "1" } });
    expect(a).toHaveBeenCalledOnce();
    offA();
    offB();
    vi.advanceTimersByTime(14_000);
    expect(last().ops("unsubscribe")).toHaveLength(0);
    // Back before the timer: nothing is unsubscribed or sent again.
    const offC = ws.subscribe("ticker:BTC-USDT", vi.fn());
    vi.advanceTimersByTime(5_000);
    expect(last().ops("unsubscribe")).toHaveLength(0);
    expect(last().ops("subscribe")).toHaveLength(1);
    offC();
    vi.advanceTimersByTime(15_000);
    expect(last().ops("unsubscribe")[0]!.args).toEqual(["ticker:BTC-USDT"]);
  });

  it("reconnects with backoff and replays the subscriptions", () => {
    const statuses: string[] = [];
    const ws = client();
    ws.onStatus((s) => statuses.push(s));
    ws.subscribe("depth:BTC-USDT", vi.fn());
    last().open();
    last().drop();
    expect(statuses).toEqual(["connecting", "open", "reconnecting"]);
    vi.advanceTimersByTime(999);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
    last().drop(); // failed again: 2 seconds
    vi.advanceTimersByTime(1999);
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(1);
    last().open();
    expect(last().ops("subscribe")[0]!.args).toEqual(["depth:BTC-USDT"]);
    expect(statuses.at(-1)).toBe("open");
  });

  it("subscribes private channels after auth and asks for what it missed after a reconnect", () => {
    let token = "t1";
    const ws = client({ token: () => token });
    const orders = vi.fn();
    ws.subscribe("orders", orders);
    last().open();
    expect(last().ops("auth")[0]!.token).toBe("t1");
    expect(last().ops("subscribe")).toHaveLength(0); // waits for auth
    last().push({ op: "auth", ok: true, user_id: "u1" });
    expect(last().ops("subscribe")[0]).toEqual({ op: "subscribe", args: ["orders"] });
    last().push({ channel: "orders", seq: 7, data: { order_id: "o1" } });
    last().push({ channel: "orders", seq: 7, data: { order_id: "o1" } }); // a replay
    expect(orders).toHaveBeenCalledOnce();
    last().drop();
    vi.advanceTimersByTime(1000);
    token = "t2";
    last().open();
    expect(last().ops("auth")[0]!.token).toBe("t2");
    last().push({ op: "auth", ok: true });
    expect(last().ops("subscribe")[0]).toEqual({ op: "subscribe", args: ["orders"], last_seq: 7 });
    const resync = vi.fn();
    ws.onResync(resync);
    last().push({ op: "resync", ok: true, args: ["orders"] });
    expect(resync).toHaveBeenCalledWith(["orders"]);
  });

  it("reloads private data once after the first private subscription, which the server cannot replay", () => {
    const ws = client({ token: () => "t1" });
    const resync = vi.fn();
    ws.onResync(resync);
    ws.subscribe("orders", vi.fn());
    last().open();
    last().push({ op: "auth", ok: true });
    expect(resync).not.toHaveBeenCalled();
    last().push({ op: "subscribe", ok: true, args: ["orders"] });
    expect(resync).toHaveBeenCalledOnce();
    // A public subscription afterwards, or the same reply again, changes nothing.
    last().push({ op: "subscribe", ok: true, args: ["orders"] });
    expect(resync).toHaveBeenCalledOnce();
    // After a reconnect with a sequence the server replays instead.
    last().push({ channel: "orders", seq: 3, data: {} });
    last().drop();
    vi.advanceTimersByTime(1000);
    last().open();
    last().push({ op: "auth", ok: true });
    last().push({ op: "subscribe", ok: true, args: ["orders"] });
    expect(resync).toHaveBeenCalledOnce();
  });

  it("refreshes an expired token and authenticates again", async () => {
    const ws = client({ token: () => "old", refresh: async () => "new" });
    ws.subscribe("balances", vi.fn());
    last().open();
    last().push({ op: "error", code: "AUTH_TOKEN_EXPIRED" });
    await vi.runAllTimersAsync();
    expect(last().ops("auth").map((m) => m.token)).toEqual(["old", "new"]);
  });

  it("asks once for a snapshot when depth skips a sequence", () => {
    const ws = client();
    const got = vi.fn();
    const sync: [string, boolean][] = [];
    ws.onSync((ch, s) => sync.push([ch, s]));
    ws.subscribe("depth:BTC-USDT", got);
    last().open();
    last().push({ channel: "depth:BTC-USDT", type: "snapshot", seq: 5, data: { bids: [], asks: [] } });
    last().push({ channel: "depth:BTC-USDT", type: "update", seq: 6, prev_seq: 5, data: { bids: [], asks: [] } });
    expect(got).toHaveBeenCalledTimes(2);
    last().push({ channel: "depth:BTC-USDT", type: "update", seq: 9, prev_seq: 8, data: { bids: [], asks: [] } });
    last().push({ channel: "depth:BTC-USDT", type: "update", seq: 10, prev_seq: 9, data: { bids: [], asks: [] } });
    expect(got).toHaveBeenCalledTimes(2);
    expect(ws.isSyncing("depth:BTC-USDT")).toBe(true);
    expect(last().ops("unsubscribe")).toHaveLength(1); // once, not per message
    last().push({ channel: "depth:BTC-USDT", type: "snapshot", seq: 10, data: { bids: [], asks: [] } });
    expect(got).toHaveBeenCalledTimes(3);
    expect(sync).toEqual([
      ["depth:BTC-USDT", true],
      ["depth:BTC-USDT", false],
      ["depth:BTC-USDT", true],
      ["depth:BTC-USDT", false],
    ]);
  });

  it("keeps only the last message of a busy channel while the page is hidden", () => {
    let visible = true;
    const ws = client({ visible: () => visible });
    const got = vi.fn();
    ws.subscribe("ticker:BTC-USDT", got);
    last().open();
    visible = false;
    document.dispatchEvent(new Event("visibilitychange"));
    last().push({ channel: "ticker:BTC-USDT", data: { last: "1" } });
    last().push({ channel: "ticker:BTC-USDT", data: { last: "2" } });
    expect(got).not.toHaveBeenCalled();
    visible = true;
    document.dispatchEvent(new Event("visibilitychange"));
    expect(got).toHaveBeenCalledOnce();
    expect(got.mock.calls[0]![0].data.last).toBe("2");
  });

  it("starts anonymous again on sign-out", () => {
    const ws = client({ token: () => "t1" });
    ws.subscribe("ticker:BTC-USDT", vi.fn());
    last().open();
    last().push({ op: "auth", ok: true });
    ws.authenticate(undefined);
    expect(FakeSocket.all).toHaveLength(2);
  });
});
