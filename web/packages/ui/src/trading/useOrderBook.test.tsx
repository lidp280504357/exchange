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
});
