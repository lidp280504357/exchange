import { describe, expect, it } from "vitest";
import { closeableQuantity, maxOpenQuantity, openCost, roe } from "./futuresMath";

describe("futures order math", () => {
  it("reserves notional / leverage plus the taker fee", () => {
    // 0.01 BTC at 60,000 = 600 notional; 20x → 30 margin; 0.05% fee → 0.3.
    expect(openCost("60000", "0.01", 20, "0.0005")).toBe("30.3");
    expect(openCost("60000", "0", 20, "0.0005")).toBe("0");
    expect(openCost("60000", "0.01", 0, "0.0005")).toBe("0");
  });

  it("opens at most what the margin covers, down to the lot", () => {
    // 1000 / (60000/20 + 60000 × 0.0005) = 1000 / 3030 = 0.33003… → 0.330
    expect(maxOpenQuantity("1000", "60000", 20, "0.0005", "0.001")).toBe("0.33");
    expect(maxOpenQuantity("0", "60000", 20, "0.0005", "0.001")).toBe("0");
    expect(maxOpenQuantity("1000", "60000", 1, "0", "0.001")).toBe("0.016");
  });

  it("closes the position's size whatever its sign", () => {
    expect(closeableQuantity("-0.25")).toBe("0.25");
    expect(closeableQuantity("0.4")).toBe("0.4");
    expect(closeableQuantity(undefined)).toBe("0");
  });

  it("reports the return on margin", () => {
    expect(roe("15", "100")).toBe("0.15");
    expect(roe("-7.5", "100")).toBe("-0.075");
    expect(roe(null, "100")).toBe("0");
  });
});
