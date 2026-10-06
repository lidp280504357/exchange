import { describe, expect, it } from "vitest";
import { direction, inRange, stepOK } from "./capsRules";

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
    expect(inRange("contract_leverage", "0.5")).toBe(false);
    expect(inRange("contract_leverage", "1")).toBe(true);
    expect(inRange("contract_leverage", "125")).toBe(true);
    expect(inRange("contract_leverage", "126")).toBe(false);
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
