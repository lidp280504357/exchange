import { describe, expect, it } from "vitest";
import type { CandleData } from "../ws/types";
import { flatten, mergeLive } from "./candles";
import { bookSteps } from "./pairs";

const c = (t: string, close: string): CandleData => ({
  open_time: t, open: "1", high: "2", low: "0.5", close, volume: "1", quote_volume: "1", trade_count: 1, closed: false,
});

describe("candles", () => {
  it("replaces the open candle, appends a new one and ignores an old one", () => {
    const page = { candles: [c("2026-09-30T10:00:00Z", "1"), c("2026-09-30T10:01:00Z", "2")] };
    expect(mergeLive(page, c("2026-09-30T10:01:00Z", "3")).candles.map((x) => x.close)).toEqual(["1", "3"]);
    expect(mergeLive(page, c("2026-09-30T10:02:00Z", "4")).candles).toHaveLength(3);
    expect(mergeLive(page, c("2026-09-30T09:59:00Z", "9"))).toBe(page);
    expect(mergeLive({ candles: [] }, c("2026-09-30T10:00:00Z", "5")).candles).toHaveLength(1);
  });

  it("flattens pages (latest first) into one series, oldest first", () => {
    const latest = { candles: [c("2026-09-30T10:02:00Z", "3"), c("2026-09-30T10:03:00Z", "4")] };
    const older = { candles: [c("2026-09-30T10:00:00Z", "1"), c("2026-09-30T10:01:00Z", "2")] };
    expect(flatten([latest, older]).map((x) => x.close)).toEqual(["1", "2", "3", "4"]);
  });
});

describe("bookSteps", () => {
  it("offers the tick and its powers of ten", () => {
    expect(bookSteps("0.01")).toEqual(["0.01", "0.1", "1", "10"]);
    expect(bookSteps("0.00001", 3)).toEqual(["0.00001", "0.0001", "0.001"]);
    expect(bookSteps("1")).toEqual(["1", "10", "100", "1000"]);
  });
});
