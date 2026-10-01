import { describe, expect, it } from "vitest";
import { OrderBook, displayUnit } from "./orderbook";

describe("OrderBook", () => {
  it("keeps each side sorted best first through updates", () => {
    const b = new OrderBook();
    b.snapshot({ bids: [["100", "1"], ["102", "2"], ["101", "3"]], asks: [["104", "1"], ["103", "2"]] }, 5);
    expect(b.bestBid).toBe("102");
    expect(b.bestAsk).toBe("103");
    b.update({ bids: [["101.5", "4"], ["102", "0"]], asks: [["103", "5"], ["105", "1"]] }, 6);
    const v = b.view(10);
    expect(v.bids.map((l) => l.price)).toEqual(["101.5", "101", "100"]);
    expect(v.asks.map((l) => [l.price, l.quantity])).toEqual([["103", "5"], ["104", "1"], ["105", "1"]]);
    expect(v.bids.map((l) => l.total)).toEqual(["4", "7", "8"]);
    expect(v.maxTotal).toBe("8");
    expect(v.spread).toBe("1.5");
    expect(v.seq).toBe(6);
  });
  it("merges levels into steps without flattering them", () => {
    const b = new OrderBook();
    b.snapshot({ bids: [["100.19", "1"], ["100.11", "2"], ["100.05", "1"]], asks: [["100.21", "1"], ["100.29", "2"], ["100.31", "1"]] });
    const v = b.view(5, "0.1");
    expect(v.bids).toEqual([
      { price: "100.1", quantity: "3", total: "3" },
      { price: "100.0", quantity: "1", total: "4" },
    ]);
    expect(v.asks).toEqual([
      { price: "100.3", quantity: "3", total: "3" },
      { price: "100.4", quantity: "1", total: "4" },
    ]);
  });
  it("cuts to the depth asked for", () => {
    const b = new OrderBook();
    const bids = Array.from({ length: 200 }, (_, i) => [String(1000 - i), "1"] as [string, string]);
    b.snapshot({ bids, asks: [] });
    expect(b.view(20).bids).toHaveLength(20);
    expect(b.view(20).spread).toBeNull();
  });
  it("folds levels too small to show into the next one away from the spread", () => {
    const b = new OrderBook();
    b.snapshot({
      bids: [["100.02", "0.00001"], ["100.01", "0.00002"], ["100", "0.5"], ["99.99", "0.00004"]],
      asks: [["100.03", "0.2"], ["100.04", "0.00003"], ["100.05", "0.1"]],
    });
    const v = b.view(10, "", displayUnit(4));
    // Two dust bids ride on 100 (their total counts); the last one has nothing behind it.
    expect(v.bids).toEqual([{ price: "100", quantity: "0.50003", total: "0.50003" }]);
    expect(v.asks).toEqual([
      { price: "100.03", quantity: "0.2", total: "0.2" },
      { price: "100.05", quantity: "0.10003", total: "0.30003" },
    ]);
    // The spread is the book's own, dust and all.
    expect(v.spread).toBe("0.01");
    // A folded level frees its slot for the next one.
    expect(b.view(1, "", "0.0001").bids.map((l) => l.price)).toEqual(["100"]);
  });
  it("names the smallest amount a number of decimals shows", () => {
    expect(displayUnit(4)).toBe("0.0001");
    expect(displayUnit(1)).toBe("0.1");
    expect(displayUnit(0)).toBe("1");
  });
});
