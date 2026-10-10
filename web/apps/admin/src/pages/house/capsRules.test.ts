import { describe, expect, it } from "vitest";
import { direction, holdings, inRange, over, stepOK, stepRange, totalOver } from "./capsRules";

describe("HOUSE's caps", () => {
  it("keeps each cap within its range", () => {
    expect(inRange("level", "0")).toBe(true);
    expect(inRange("level", "-0.1")).toBe(false);
    for (const name of ["symbol", "total", "contract", "safety"] as const) {
      expect(inRange(name, "0")).toBe(false);
      expect(inRange(name, "0.01")).toBe(true);
      expect(inRange(name, "1000000000000000")).toBe(true);
      expect(inRange(name, "1000000000000000.01")).toBe(false);
    }
    // The leverage up to the contracts' highest (A97), market-maker's own bound while unknown.
    expect(inRange("contract_leverage", "0.5", "150")).toBe(false);
    expect(inRange("contract_leverage", "1", "150")).toBe(true);
    expect(inRange("contract_leverage", "150", "150")).toBe(true);
    expect(inRange("contract_leverage", "151", "150")).toBe(false);
    expect(inRange("contract_leverage", "1000")).toBe(true);
    expect(inRange("contract_leverage", "1001")).toBe(false);
    expect(inRange("safety", " 1000 ")).toBe(true);
    expect(inRange("safety", "lots")).toBe(false);
    expect(inRange("safety", "")).toBe(false);
  });

  it("moves a cap ten times at most either way", () => {
    expect(stepOK("100", "1000")).toBe(true);
    expect(stepOK("100", "1000.01")).toBe(false);
    expect(stepOK("100", "10")).toBe(true);
    expect(stepOK("100", "9.99")).toBe(false);
    expect(stepOK("500", "0")).toBe(true);
    expect(stepOK("0", "500000")).toBe(true);
    expect(stepOK("100", "x")).toBe(true);
  });

  it("tells how far one change can move a cap", () => {
    expect(stepRange("total", "500000000")).toEqual({ min: "50000000", max: "5000000000" });
    expect(stepRange("safety", "12.5")).toEqual({ min: "1.25", max: "125" });
    expect(stepRange("symbol", "500000000000000")).toEqual({ min: "50000000000000", max: "1000000000000000" });
    // The leverage too: 10 to 150 takes two changes.
    expect(stepRange("contract_leverage", "10", "150")).toEqual({ min: "1", max: "100" });
    expect(stepRange("contract_leverage", "100", "150")).toEqual({ min: "10", max: "150" });
    expect(stepRange("level", "0")).toBe(null);
    expect(stepRange("level", "x")).toBe(null);
    for (const [name, before] of [["total", "500000000"], ["safety", "12.5"], ["contract_leverage", "10"]] as const) {
      const r = stepRange(name, before, "150");
      expect(r && stepOK(before, r.min) && stepOK(before, r.max) && inRange(name, r.min, "150") && inRange(name, r.max, "150")).toBe(true);
    }
  });

  it("reads HOUSE's holdings as the caps count them", () => {
    const { list, total } = holdings([
      { asset: "USDT", value_usdt: "900000000" },
      { asset: "BTC", value_usdt: "1200000.5" },
      { asset: "ASTRA", value_usdt: "-3000000" },
      { asset: "ETH", value_usdt: "800000" },
      { asset: "NOPRICE", value_usdt: null },
      { asset: "DUST", value_usdt: "0" },
    ]);
    expect(list).toEqual([
      { asset: "ASTRA", value: "-3000000" },
      { asset: "BTC", value: "1200000.5" },
      { asset: "ETH", value: "800000" },
    ]);
    expect(total).toBe("5000000.5");
    expect(over(list, "1000000").map((h) => h.asset)).toEqual(["ASTRA", "BTC"]);
    // At the cap HOUSE stops already: its room is zero there (A80 ①).
    expect(over(list, "3000000").map((h) => h.asset)).toEqual(["ASTRA"]);
    expect(over(list, "3000000.01")).toEqual([]);
    expect(over(list, "0")).toEqual([]);
    expect(over(list, "")).toEqual([]);
    expect(totalOver(total, "5000000.5")).toBe(true);
    expect(totalOver(total, "5000000.51")).toBe(false);
    expect(totalOver(total, "4000000")).toBe(true);
    expect(totalOver(total, "0")).toBe(false);
    expect(totalOver(total, "x")).toBe(false);
  });

  it("says which way a change moves a cap, a level cap of zero being none", () => {
    expect(direction("total", "500", "400")).toBe("lower");
    expect(direction("total", "500", "500.00")).toBe(null);
    expect(direction("contract_leverage", "10", "20")).toBe("raise");
    expect(direction("level", "500", "0")).toBe("raise");
    expect(direction("level", "0", "500")).toBe("lower");
    expect(direction("level", "0", "0")).toBe(null);
    expect(direction("level", "500", "600")).toBe("raise");
    expect(direction("safety", "1000", "x")).toBe(null);
  });
});
