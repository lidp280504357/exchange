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

describe("OrderBook.fit", () => {
  const steps = ["0.01", "0.1", "1", "10"];
  // levels makes n levels from a price in cents, `by` cents apart.
  const levels = (from: number, by: number, n: number) => Array.from({ length: n }, (_, i) => [((from + i * by) / 100).toFixed(2), "1"] as [string, string]);
  // A dense book as the public one carries it: 200 levels a side over
  // about 38 USDT (BTC-USDT on 2026-10-05).
  const dense = () => {
    const b = new OrderBook();
    b.snapshot({ bids: levels(8596200, -19, 200), asks: levels(8596201, 19, 200) });
    return b;
  };

  it("gives the default step way when the book is denser than usual", () => {
    // BTC-USDT at 20:20 on 2026-10-05: 200 levels a side over about 5
    // USDT, so the default step 1 had five rows a side (review CD).
    const b = new OrderBook();
    b.snapshot({ bids: levels(8596200, -2.5, 200), asks: levels(8596201, 2.5, 200) });
    expect(b.view(15, "1").bids.length).toBeLessThan(15);
    expect(b.fit(15, "1", steps)).toMatchObject({ step: "0.1", fits: ["0.01", "0.1"] });
  });
  it("never finds more levels at a coarser step, dust folded or not", () => {
    // Random books (a fixed seed): quantities from dust to whole units.
    let seed = 7;
    const rand = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
    for (let n = 0; n < 200; n++) {
      // Up to 200 levels from a price in cents, 1 to 40 cents apart.
      const side = (from: number, dir: 1 | -1) => {
        let cents = from;
        return Array.from({ length: 1 + Math.floor(rand() * 200) }, () => {
          cents += dir * (1 + Math.floor(rand() * 40));
          return [(cents / 100).toFixed(2), (rand() < 0.4 ? rand() * 0.0002 : rand() * 3).toFixed(5)] as [string, string];
        });
      };
      const b = new OrderBook();
      b.snapshot({ bids: side(1000000, -1), asks: side(1000000, 1) });
      for (const minQty of ["", "0.0001"]) {
        const counts = steps.map((s) => b.view(500, s, minQty));
        for (let i = 1; i < counts.length; i++) {
          expect(counts[i]!.bids.length).toBeLessThanOrEqual(counts[i - 1]!.bids.length);
          expect(counts[i]!.asks.length).toBeLessThanOrEqual(counts[i - 1]!.asks.length);
        }
      }
    }
  });
  it("gives a step its levels cannot fill way to the coarsest finer one that fills", () => {
    const v = dense().fit(15, "10", steps);
    expect(dense().view(15, "10").bids.length).toBeLessThan(15);
    expect(v.step).toBe("1");
    expect(v.bids).toHaveLength(15);
    expect(v.asks).toHaveLength(15);
    expect(v.fits).toEqual(["0.01", "0.1", "1"]);
    // The same view a cut at 1 gives (totals and the largest of them).
    expect(v).toMatchObject(dense().view(15, "1"));
  });
  it("keeps a step that fills, and tells which steps do", () => {
    expect(dense().fit(15, "1", steps)).toMatchObject({ step: "1", fits: ["0.01", "0.1", "1"] });
    expect(dense().fit(15, "0.01", steps)).toMatchObject({ step: "0.01", fits: ["0.01", "0.1", "1"] });
    // 40 rows a side: 1 has 38 or 39 levels, 0.1 fills.
    expect(dense().fit(40, "1", steps)).toMatchObject({ step: "0.1", fits: ["0.01", "0.1"] });
  });
  it("keeps the step asked when no step fills, or nothing is known yet", () => {
    const thin = new OrderBook();
    thin.snapshot({ bids: levels(10000, -500, 10), asks: levels(10100, 500, 10) });
    expect(thin.fit(15, "10", steps)).toMatchObject({ step: "10", fits: steps });
    expect(new OrderBook().fit(15, "10", steps)).toMatchObject({ step: "10", fits: steps });
    expect(dense().fit(15, "5", steps)).toMatchObject({ step: "5", fits: steps });
  });
  it("only needs the sides shown to fill", () => {
    const b = new OrderBook();
    b.snapshot({ bids: levels(8596200, -19, 200), asks: levels(8596201, 19, 5) });
    expect(b.fit(15, "1", steps, "", "bids")).toMatchObject({ step: "1", fits: ["0.01", "0.1", "1"] });
    expect(b.fit(15, "1", steps, "", "both")).toMatchObject({ step: "1", fits: steps });
  });
  it("holds a finer step until the coarser one fills with levels to spare", () => {
    // Steps of 1 and 10; at 10 the asks have 4 levels, the bids 5.
    const b = new OrderBook();
    b.snapshot({ bids: levels(10000, -100, 40), asks: levels(10100, 100, 40) });
    expect(b.fit(3, "10", ["1", "10"])).toMatchObject({ step: "10" });
    expect(b.fit(3, "10", ["1", "10"], "", "both", "1")).toMatchObject({ step: "1" });
    b.update({ bids: [], asks: [["150.00", "1"]] });
    expect(b.fit(3, "10", ["1", "10"], "", "both", "1")).toMatchObject({ step: "10" });
  });
});
