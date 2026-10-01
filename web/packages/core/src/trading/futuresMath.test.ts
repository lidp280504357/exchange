import { describe, expect, it } from "vitest";
import {
  checkRiskLimit, closeableQuantity, maxNotional, maxOpenQuantity, openCost, openLimit, reservePrice, riskRoom, roe, sideExposure, unrealizedPnl,
  type ExposedOrder, type ExposedPosition, type RiskTier,
} from "./futuresMath";

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

  it("values a position at a newer mark price", () => {
    expect(unrealizedPnl("2", "84101.3", "84500")).toBe("797.4");
    expect(unrealizedPnl("-0.5", "60000", "59000")).toBe("500");
    expect(unrealizedPnl("1", "60000", null)).toBe("0");
  });
});

describe("risk limits", () => {
  // The deployed ladder (deploy/instruments/test.json, 2026-10-02).
  const ladder: RiskTier[] = [
    { max_notional: "50000", max_leverage: 125, mmr: "0.004" },
    { max_notional: "250000", max_leverage: 100, mmr: "0.005" },
    { max_notional: "1000000", max_leverage: 50, mmr: "0.01" },
    { max_notional: "5000000", max_leverage: 20, mmr: "0.025" },
    { max_notional: "20000000", max_leverage: 10, mmr: "0.05" },
    { max_notional: "50000000", max_leverage: 5, mmr: "0.1" },
    { max_notional: "100000000", max_leverage: 2, mmr: "0.125" },
  ];

  it("caps a leverage at the last tier that allows it", () => {
    expect(maxNotional(ladder, 125)).toBe("50000");
    expect(maxNotional(ladder, 101)).toBe("50000");
    expect(maxNotional(ladder, 100)).toBe("250000");
    // 30x is allowed up to the 50x tier: 1 BTC at 84,196 fits.
    expect(maxNotional(ladder, 30)).toBe("1000000");
    expect(maxNotional(ladder, 1)).toBe("100000000");
    expect(maxNotional(ladder, 126)).toBe("0");
  });

  it("counts the side's position and its opening orders", () => {
    const positions: ExposedPosition[] = [{ position_side: "BOTH", quantity: "0.4" }];
    const orders: ExposedOrder[] = [
      { side: "BUY", position_side: "BOTH", reduce_only: false, quantity: "0.5", filled_quantity: "0.2" },
      { side: "BUY", position_side: "BOTH", reduce_only: true, quantity: "9", filled_quantity: "0" },
      { side: "SELL", position_side: "BOTH", reduce_only: false, quantity: "0.1", filled_quantity: "0" },
    ];
    expect(sideExposure("BUY", "BOTH", positions, orders)).toBe("0.7");
    // A long does not count against sells; the sell order does.
    expect(sideExposure("SELL", "BOTH", positions, orders)).toBe("0.1");
    const hedge: ExposedPosition[] = [
      { position_side: "LONG", quantity: "1" },
      { position_side: "SHORT", quantity: "-2" },
    ];
    const hedgeOrders: ExposedOrder[] = [
      { side: "SELL", position_side: "SHORT", reduce_only: false, quantity: "0.5", filled_quantity: "0" },
      { side: "BUY", position_side: "SHORT", reduce_only: false, quantity: "1", filled_quantity: "0" }, // closes
    ];
    expect(sideExposure("SELL", "SHORT", hedge, hedgeOrders)).toBe("2.5");
    expect(sideExposure("BUY", "LONG", hedge, hedgeOrders)).toBe("1");
  });

  it("opens no more than the cap leaves, and says why an order does not fit", () => {
    // 30x: 1,000,000 / 84,196 = 11.877 BTC, less 0.7 held or ordered.
    expect(riskRoom(ladder, 30, "84196", "0.7", "0.001")).toBe("11.177");
    expect(riskRoom(ladder, 125, "84196", "0.7", "0.001")).toBe("0");
    expect(openLimit("3.51", "11.177")).toBe("3.51");
    expect(checkRiskLimit(ladder, 30, "84196", "0", "1")).toEqual({ ok: true, cap: "1000000", notional: "84196" });
    expect(checkRiskLimit(ladder, 125, "84196", "0", "1")).toEqual({ ok: false, cap: "50000", notional: "84196" });
  });
});

describe("reservePrice", () => {
  it("is where derivatives-service reserves an opening order", () => {
    // A buy at its price; a market buy at the mark plus the band, down to the tick.
    expect(reservePrice("BUY", "limit", "59000", "60000.07", "0.05", "0.1")).toBe("59000");
    expect(reservePrice("BUY", "market", "", "60000.07", "0.05", "0.1")).toBe("63000");
    // A sell at the higher of its price and the mark; a market sell at the mark.
    expect(reservePrice("SELL", "limit", "61000", "60000.07", "0.05", "0.1")).toBe("61000");
    expect(reservePrice("SELL", "limit", "59000", "60000.07", "0.05", "0.1")).toBe("60000.07");
    expect(reservePrice("SELL", "market", "", "60000.07", "0.05", "0.1")).toBe("60000.07");
    // Without a mark a limit order still has its price; a market one has nothing.
    expect(reservePrice("SELL", "limit", "59000", "", "0.05", "0.1")).toBe("59000");
    expect(reservePrice("BUY", "market", "", "", "0.05", "0.1")).toBe("");
    expect(reservePrice("SELL", "limit", "", "60000", "0.05", "0.1")).toBe("");
  });
});
