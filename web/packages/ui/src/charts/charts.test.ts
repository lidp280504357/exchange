import type { BookView, CandleData } from "@exchange/core";
import { describe, expect, it } from "vitest";
import { intervalParts, legendRoom, topMargin, updateMode } from "./candles";
import { depthGeometry } from "./DepthChart";
import { computeIndicators, ema, sma } from "./indicators";
import { createIndicatorClient } from "./indicatorClient";
import { numToDecimal } from "./numbers";

function candle(t: string, open = "1", close = "1"): CandleData {
  return { open_time: t, open, high: close, low: open, close, volume: "1", quote_volume: "1", trade_count: 1, closed: true };
}

describe("moving averages", () => {
  it("computes the simple moving average", () => {
    expect(sma([1, 2, 3, 4, 5], 3)).toEqual([null, null, 2, 3, 4]);
    expect(sma([1, 2], 3)).toEqual([null, null]);
  });
  it("computes the exponential moving average seeded with the SMA", () => {
    const out = ema([1, 2, 3, 4, 5], 3);
    expect(out.slice(0, 2)).toEqual([null, null]);
    expect(out[2]).toBe(2);
    expect(out[3]).toBe(3);
    expect(out[4]).toBe(4);
  });
  it("answers requests inline when there is no worker", async () => {
    const client = createIndicatorClient();
    expect(client.threaded).toBe(false);
    const res = await client.compute({ closes: [1, 2, 3, 4], ma: [2], ema: [2] });
    expect(res.ma[0]).toEqual([null, 1.5, 2.5, 3.5]);
    expect(computeIndicators({ id: 9, closes: [1, 2, 3], ma: [3], ema: [] })).toEqual({ id: 9, ma: [[null, null, 2]], ema: [] });
    client.dispose();
  });
});

describe("candle updates", () => {
  const base = [candle("2026-09-30T08:00:00Z"), candle("2026-09-30T09:00:00Z"), candle("2026-09-30T10:00:00Z")];
  const state = { key: "BTC|1h", firstTime: "2026-09-30T08:00:00Z", firstOpen: "1", lastTime: "2026-09-30T10:00:00Z", len: 3 };

  it("tells a live update from a new candle, a page of history and a reset", () => {
    expect(updateMode(null, "BTC|1h", base)).toBe("reset");
    expect(updateMode(state, "BTC|1h", [...base.slice(0, 2), candle("2026-09-30T10:00:00Z", "1", "2")])).toBe("tail");
    expect(updateMode(state, "BTC|1h", [...base, candle("2026-09-30T11:00:00Z")])).toBe("append");
    expect(updateMode(state, "BTC|1h", [candle("2026-09-30T06:00:00Z"), candle("2026-09-30T07:00:00Z"), ...base])).toBe("prepend");
    expect(updateMode(state, "BTC|4h", base)).toBe("reset");
    expect(updateMode(state, "BTC|1h", [candle("2026-09-30T08:00:00Z", "9"), ...base.slice(1)])).toBe("reset");
  });

  it("reads intervals", () => {
    expect(intervalParts("15m")).toEqual({ n: 15, unit: "m" });
    expect(intervalParts("1M")).toEqual({ n: 1, unit: "M" });
    expect(intervalParts("1x")).toBeNull();
  });
});

describe("chart numbers", () => {
  it("turns drawn numbers into decimal strings without float formatting", () => {
    expect(numToDecimal(63214.456, 2)).toBe("63214.46");
    expect(numToDecimal(0.1 + 0.2, 4)).toBe("0.3");
    expect(numToDecimal(-1.5, 0)).toBe("-1");
    expect(numToDecimal(Number.NaN, 2)).toBe("");
  });

  it("centres the depth chart on the mid price", () => {
    const view: BookView = {
      bids: [
        { price: "99", quantity: "1", total: "1" },
        { price: "98", quantity: "2", total: "3" },
      ],
      asks: [
        { price: "101", quantity: "1", total: "1" },
        { price: "104", quantity: "1", total: "2" },
      ],
      maxTotal: "3",
      spread: "2",
      seq: 1,
    };
    const g = depthGeometry(view);
    expect(g?.mid).toBe(100);
    expect(g?.min).toBe(96);
    expect(g?.max).toBe(104);
    expect(depthGeometry({ bids: [], asks: [], maxTotal: "0", spread: null, seq: 0 })).toBeNull();
  });
});

describe("topMargin (B116)", () => {
  it("leaves the legend's height and 12 px at the top of the candles, between 8 % and half the pane", () => {
    expect(topMargin(0, 234)).toBe(0.08);
    expect(topMargin(58, 234)).toBeCloseTo(70 / 234, 6);
    expect(topMargin(10, 600)).toBe(0.08);
    expect(topMargin(200, 300)).toBe(0.5);
    expect(topMargin(58, 0)).toBe(0.08);
  });
});

describe("legendRoom (B116, B118)", () => {
  it("keeps the tallest legend while the key and width stay, and measures afresh when either changes", () => {
    let room = legendRoom({ key: "", width: 0, legend: 0 }, "BTC-USDT|1h|MA,VOL", 600, 34);
    expect(room).toEqual({ key: "BTC-USDT|1h|MA,VOL", width: 600, legend: 34 });
    room = legendRoom(room, "BTC-USDT|1h|MA,VOL", 600, 50);
    expect(room.legend).toBe(50);
    room = legendRoom(room, "BTC-USDT|1h|MA,VOL", 600, 34);
    expect(room.legend).toBe(50);
    expect(legendRoom(room, "BTC-USDT|1d|MA,VOL", 600, 34).legend).toBe(34);
    expect(legendRoom(room, "BTC-USDT|1h|MA,VOL", 420, 34).legend).toBe(34);
  });
});
