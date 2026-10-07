import { describe, expect, it } from "vitest";
import { ALL_OPEN, allOpen, isOpen, openProducts, productOf, productOfPath, terminalLine, tradeEntry } from "./products";

describe("product lines", () => {
  it("are open until known, and a line the answer leaves out stays open", () => {
    expect(openProducts(undefined)).toEqual(ALL_OPEN);
    expect(openProducts({ spot: { enabled: true }, usdt_m: { enabled: false, closed_at: "2026-10-07T08:00:00Z" }, coin_m: { enabled: true } })).toEqual({
      spot: true,
      usdt_m: false,
      coin_m: true,
    });
    expect(allOpen(ALL_OPEN)).toBe(true);
    expect(allOpen({ ...ALL_OPEN, coin_m: false })).toBe(false);
  });

  it("name the line of a market and of a trading page", () => {
    expect(productOf("BTC-USDT")).toBe("spot");
    expect(productOf("BTC-USDT-PERP")).toBe("usdt_m");
    expect(productOf("ASTRA-USDT-PERP")).toBe("usdt_m");
    expect(productOf("BTC-USD-PERP")).toBe("coin_m");
    expect(isOpen("BTC-USD-PERP", { ...ALL_OPEN, coin_m: false })).toBe(false);
    expect(isOpen("BTC-USDT", { ...ALL_OPEN, coin_m: false })).toBe(true);
    expect(productOfPath("/trade/BTC-USDT")).toBe("spot");
    expect(productOfPath("/futures/ETH-USD-PERP")).toBe("coin_m");
    expect(productOfPath("/futures/data")).toBeNull();
    expect(productOfPath("/markets")).toBeNull();
    expect(terminalLine("trade", "BTC-USDT")).toBe("spot");
    expect(terminalLine("futures", "BTC-USD-PERP")).toBe("coin_m");
    expect(terminalLine("futures", "BTC-USDT")).toBeNull();
  });

  it("lead a trade entry to an open line, the last one visited first", () => {
    expect(tradeEntry("/futures/BTC-USD-PERP", ALL_OPEN)).toBe("/futures/BTC-USD-PERP");
    expect(tradeEntry("/futures/BTC-USD-PERP", { ...ALL_OPEN, coin_m: false })).toBe("/trade/BTC-USDT");
    expect(tradeEntry("/trade/ETH-USDT", { spot: false, usdt_m: true, coin_m: true })).toBe("/futures/BTC-USDT-PERP");
    expect(tradeEntry(null, { spot: false, usdt_m: false, coin_m: true })).toBe("/futures/BTC-USD-PERP");
    expect(tradeEntry("/trade/ETH-USDT", { spot: false, usdt_m: false, coin_m: false })).toBe("/trade/ETH-USDT");
  });

});
