import "../test/setup";
import { channels, LiveProvider, MarketStore, useOrderBook, type WsClient } from "@exchange/core";
import { act, render } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// core's useOrderBook, tested here where React renders in tests: a
// throttled book (the terminals redraw theirs at most every 250 ms) shows
// what its throttle passed on.

/** fakeWs hands the test each channel's handler. */
function fakeWs() {
  const handlers = new Map<string, (m: unknown) => void>();
  const ws = {
    subscribe: (channel: string, handler: (m: unknown) => void) => {
      handlers.set(channel, handler);
      return () => handlers.delete(channel);
    },
  } as unknown as WsClient;
  return { ws, push: (channel: string, m: unknown) => handlers.get(channel)?.(m) };
}

describe("useOrderBook with a throttle", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows the state its throttle passed on, not a newer one a render for another reason would bring", () => {
    const { ws, push } = fakeWs();
    const market = new MarketStore(ws, (cb) => cb());
    const depth = channels.depth("BTC-USDT");
    let renderAgain = () => {};
    let best = "";
    function Book() {
      const [, set] = useState(0);
      renderAgain = () => set((n) => n + 1);
      best = useOrderBook("BTC-USDT", 5, "", { every: 250 }).bids[0]?.price ?? "";
      return null;
    }
    render(
      <LiveProvider ws={ws} market={market}>
        <Book />
      </LiveProvider>,
    );

    // The first change after a quiet spell is passed on at once.
    act(() => push(depth, { channel: depth, type: "snapshot", seq: 1, data: { bids: [["100", "1"]], asks: [["101", "1"]] } }));
    expect(best).toBe("100");

    // A better bid within the 250 ms: the book keeps what it showed, even
    // when it renders for another reason (the last trade above it).
    act(() => push(depth, { channel: depth, type: "update", seq: 2, data: { bids: [["100.5", "2"]], asks: [] } }));
    act(() => renderAgain());
    expect(best).toBe("100");

    // The throttle passes it on at the end of its window.
    act(() => vi.advanceTimersByTime(250));
    expect(best).toBe("100.5");
  });

  it("shows the live state when it subscribes anew, not what its old throttle passed on", () => {
    const { ws, push } = fakeWs();
    const market = new MarketStore(ws, (cb) => cb());
    const depth = channels.depth("BTC-USDT");
    let best = "";
    function Book({ every }: { every: number }) {
      best = useOrderBook("BTC-USDT", 5, "", { every }).bids[0]?.price ?? "";
      return null;
    }
    const tree = (every: number) => (
      <LiveProvider ws={ws} market={market}>
        <Book every={every} />
      </LiveProvider>
    );
    const { rerender } = render(tree(250));
    act(() => push(depth, { channel: depth, type: "snapshot", seq: 1, data: { bids: [["100", "1"]], asks: [["101", "1"]] } }));
    act(() => push(depth, { channel: depth, type: "update", seq: 2, data: { bids: [["100.5", "2"]], asks: [] } }));
    expect(best).toBe("100");

    // The same book with another throttle: the old one is unsubscribed
    // with the change it held back, and the new one has passed nothing on
    // yet, so the book shows the store's state at once.
    act(() => rerender(tree(500)));
    expect(best).toBe("100.5");
  });
});

describe("useOrderBook with the steps offered", () => {
  it("cuts a step the book cannot fill at a finer one, and tells which", () => {
    const { ws, push } = fakeWs();
    const market = new MarketStore(ws, (cb) => cb());
    const depth = channels.depth("BTC-USDT");
    let shown = { step: "", rows: 0, fits: [] as string[] };
    function Book() {
      const v = useOrderBook("BTC-USDT", 15, "10", { steps: ["0.01", "0.1", "1", "10"] });
      shown = { step: v.step ?? "", rows: Math.min(v.bids.length, v.asks.length), fits: v.fits ?? [] };
      return null;
    }
    render(
      <LiveProvider ws={ws} market={market}>
        <Book />
      </LiveProvider>,
    );
    // 200 levels a side over about 38 USDT: at 10, five rows a side.
    const side = (from: number, by: number) => Array.from({ length: 200 }, (_, i) => [((from + i * by) / 100).toFixed(2), "1"]);
    act(() => push(depth, { channel: depth, type: "snapshot", seq: 1, data: { bids: side(8596200, -19), asks: side(8596201, 19) } }));
    expect(shown).toEqual({ step: "1", rows: 15, fits: ["0.01", "0.1", "1"] });
  });

  it("holds the finer step it took until the chosen one fills with levels to spare, per depth", () => {
    const { ws, push } = fakeWs();
    const market = new MarketStore(ws, (cb) => cb());
    const depth = channels.depth("BTC-USDT");
    let step = "";
    function Book({ rows }: { rows: number }) {
      step = useOrderBook("BTC-USDT", rows, "10", { steps: ["1", "10"] }).step ?? "";
      return null;
    }
    const tree = (rows: number) => (
      <LiveProvider ws={ws} market={market}>
        <Book rows={rows} />
      </LiveProvider>
    );
    const { rerender } = render(tree(15));
    const lv = (from: number, by: number, n: number) => Array.from({ length: n }, (_, i) => [String(from + i * by), "1"]);
    // Fifteen rows a side (18 with the levels to spare). At 10 the bids
    // (1000 down to 801) have 21 levels, the asks (1001 up to 1140) 14:
    // the book takes 1.
    act(() => push(depth, { channel: depth, type: "snapshot", seq: 1, data: { bids: lv(1000, -1, 200), asks: lv(1001, 1, 140) } }));
    expect(step).toBe("1");
    // Asks up to 1150: 15 levels at 10, the rows but none to spare.
    act(() => push(depth, { channel: depth, type: "update", seq: 2, data: { bids: [], asks: lv(1141, 1, 10) } }));
    expect(step).toBe("1");
    // Up and down between 14 and 15 levels: still 1, no swapping.
    act(() => push(depth, { channel: depth, type: "update", seq: 3, data: { bids: [], asks: lv(1141, 1, 10).map(([p]) => [p, "0"]) } }));
    expect(step).toBe("1");
    act(() => push(depth, { channel: depth, type: "update", seq: 4, data: { bids: [], asks: lv(1141, 1, 10) } }));
    expect(step).toBe("1");
    // Another depth starts afresh: 14 rows fill at 10.
    act(() => rerender(tree(14)));
    expect(step).toBe("10");
    act(() => rerender(tree(15)));
    // Back at 15 rows: 10 fills them, and nothing is held for this depth now.
    expect(step).toBe("10");
    // Up to 1180: 18 levels at 10, three to spare.
    act(() => push(depth, { channel: depth, type: "update", seq: 5, data: { bids: [], asks: lv(1151, 1, 30) } }));
    expect(step).toBe("10");
  });

  it("goes back to the chosen step only with levels to spare", () => {
    const { ws, push } = fakeWs();
    const market = new MarketStore(ws, (cb) => cb());
    const depth = channels.depth("BTC-USDT");
    let step = "";
    function Book() {
      step = useOrderBook("BTC-USDT", 15, "10", { steps: ["1", "10"] }).step ?? "";
      return null;
    }
    render(
      <LiveProvider ws={ws} market={market}>
        <Book />
      </LiveProvider>,
    );
    const lv = (from: number, by: number, n: number) => Array.from({ length: n }, (_, i) => [String(from + i * by), "1"]);
    act(() => push(depth, { channel: depth, type: "snapshot", seq: 1, data: { bids: lv(1000, -1, 200), asks: lv(1001, 1, 140) } }));
    expect(step).toBe("1");
    act(() => push(depth, { channel: depth, type: "update", seq: 2, data: { bids: [], asks: lv(1141, 1, 10) } }));
    expect(step).toBe("1");
    act(() => push(depth, { channel: depth, type: "update", seq: 3, data: { bids: [], asks: lv(1151, 1, 30) } }));
    expect(step).toBe("10");
  });
});
