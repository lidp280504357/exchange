import { describe, expect, it } from "vitest";
import { applyDepth, decimalsOf, mul, percent, type Book } from "./market";

describe("applyDepth", () => {
  it("starts from a snapshot and applies updates in order", () => {
    let book: Book | null = applyDepth(null, {
      channel: "depth:BTC-USDT", type: "snapshot", seq: 4,
      data: { bids: [["69900", "1"], ["69800", "2"]], asks: [["70100", "0.5"]] },
    });
    book = applyDepth(book, {
      channel: "depth:BTC-USDT", type: "update", seq: 5, prev_seq: 4,
      data: { bids: [["69950", "0.3"], ["69800", "0"]], asks: [["70050", "1"]] },
    });
    expect(book).toEqual({ seq: 5, bids: [["69950", "0.3"], ["69900", "1"]], asks: [["70050", "1"], ["70100", "0.5"]] });
  });

  it("reports a gap", () => {
    const book: Book = { seq: 5, bids: [], asks: [] };
    expect(applyDepth(book, { channel: "depth:X", type: "update", seq: 7, prev_seq: 6, data: { bids: [], asks: [] } })).toBeNull();
    expect(applyDepth(null, { channel: "depth:X", type: "update", seq: 1, data: { bids: [], asks: [] } })).toBeNull();
  });

  it("follows an empty snapshot at seq 0", () => {
    const empty = applyDepth(null, { channel: "depth:X", type: "snapshot", data: { bids: [], asks: [] } });
    const next = applyDepth(empty, { channel: "depth:X", type: "update", seq: 1, data: { bids: [["10", "1"]], asks: [] } });
    expect(next?.bids).toEqual([["10", "1"]]);
  });

  it("orders prices as decimals, not as text", () => {
    const book = applyDepth({ seq: 0, bids: [["9.5", "1"]], asks: [] }, {
      channel: "depth:X", type: "update", seq: 1, data: { bids: [["10", "1"], ["9.25", "2"]], asks: [] },
    });
    expect(book?.bids.map((l) => l[0])).toEqual(["10", "9.5", "9.25"]);
  });
});

describe("decimal helpers", () => {
  it("multiplies exactly", () => {
    expect(mul("70000.01", "0.0003")).toBe("21.000003");
    expect(mul("0.1", "0.2")).toBe("0.02");
    expect(mul("100", "0.5")).toBe("50");
    expect(mul("0", "5")).toBe("0");
  });
  it("counts step decimals", () => {
    expect(decimalsOf("0.01")).toBe(2);
    expect(decimalsOf("0.000100000000000000")).toBe(4);
    expect(decimalsOf("1")).toBe(0);
  });
  it("renders percentages", () => {
    expect(percent("0.01253333")).toBe("+1.25%");
    expect(percent("-0.005")).toBe("-0.50%");
    expect(percent("0")).toBe("0.00%");
    expect(percent("1.5")).toBe("+150.00%");
    expect(percent(null)).toBe("—");
  });
});
