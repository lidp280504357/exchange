import { describe, expect, it } from "vitest";
import {
  coinValue, contractMath, fromContracts, inverseCheckRisk, inverseReservePrice, inverseRiskRoom, inverseUnrealizedPnl, isInverse, perContract,
  toContracts, usdValue, type ContractTerms,
} from "./coinMargined";
import { openCost } from "./futuresMath";

// BTC-USD-PERP as deployed (deploy/instruments/test.json): 100 USD a
// contract, Binance COIN-M's ladder in BTC.
const btcUsd: ContractTerms = {
  quote_asset: "USD", settle_asset: "BTC", contract_size: "100", tick_size: "0.1", lot_size: "1", price_band: "0.05", taker_fee_rate: "0.0005",
  risk_tiers: [
    { max_notional: "5", max_leverage: 125, mmr: "0.004" },
    { max_notional: "10", max_leverage: 100, mmr: "0.005" },
    { max_notional: "25", max_leverage: 50, mmr: "0.01" },
  ],
};
const btcUsdt: ContractTerms = {
  quote_asset: "USDT", settle_asset: "USDT", contract_size: "0", tick_size: "0.1", lot_size: "0.001", price_band: "0.05", taker_fee_rate: "0.0005",
  risk_tiers: [{ max_notional: "50000", max_leverage: 125, mmr: "0.004" }],
};

describe("coin-margined contracts", () => {
  it("tells a coin-margined contract by its face value", () => {
    expect(isInverse(btcUsd)).toBe(true);
    expect(isInverse(btcUsdt)).toBe(false);
    expect(isInverse({ contract_size: "" })).toBe(false);
    expect(isInverse(undefined)).toBe(false);
  });

  it("values contracts in the coin and in USD", () => {
    expect(coinValue("3", "100", "60000")).toBe("0.005");
    // 70 / 2500.3 = 0.02799664040…
    expect(coinValue("-7", "10", "2500.3")).toBe("0.02799664");
    expect(coinValue("3", "100", null)).toBe("0");
    expect(usdValue("-7", "10")).toBe("70");
  });

  it("reserves each contract's margin and fee rounded up to the coin's decimals", () => {
    // 100 / (60,000 × 20) = 0.0000833333… → 0.00008334; 100 × 0.05% / 60,000 → 0.00000084.
    expect(perContract("60000", "100", 20, "0.0005", 8)).toBe("0.00008418");
    expect(perContract("60000", "100", 0, "0.0005", 8)).toBe("0");
    const m = contractMath(btcUsd);
    expect(m.openCost("60000", "3", 20)).toBe("0.00025254");
    // 0.01 / 0.00008418 = 118.79… whole contracts.
    expect(m.maxOpen("0.01", "60000", 20)).toBe("118");
    expect(m.maxOpen("0", "60000", 20)).toBe("0");
  });

  it("reserves a buy at the lower of its price and the mark, a market sell at its protection price", () => {
    expect(inverseReservePrice("BUY", "limit", "59000", "60000", "0.05", "0.1")).toBe("59000");
    expect(inverseReservePrice("BUY", "limit", "61000", "60000", "0.05", "0.1")).toBe("60000");
    expect(inverseReservePrice("BUY", "market", "", "60000", "0.05", "0.1")).toBe("60000");
    expect(inverseReservePrice("SELL", "limit", "61000", "60000", "0.05", "0.1")).toBe("61000");
    // 60,000.03 × 0.95 = 57,000.0285, up to the tick.
    expect(inverseReservePrice("SELL", "market", "", "60000.03", "0.05", "0.1")).toBe("57000.1");
    expect(inverseReservePrice("SELL", "market", "", "", "0.05", "0.1")).toBe("");
  });

  it("keeps a side within the leverage's cap in the coin", () => {
    // 125x allows 5 BTC: 5 × 60,000 / 100 = 3,000 contracts.
    expect(inverseRiskRoom(btcUsd.risk_tiers, 125, "60000", "10", "100")).toBe("2990");
    expect(inverseRiskRoom(btcUsd.risk_tiers, 125, "60000", "3000", "100")).toBe("0");
    expect(inverseCheckRisk(btcUsd.risk_tiers, 125, "60000", "2990", "10", "100")).toEqual({ ok: true, cap: "5", notional: "5" });
    expect(inverseCheckRisk(btcUsd.risk_tiers, 125, "60000", "2990", "11", "100").ok).toBe(false);
  });

  it("measures a result in the coin", () => {
    // 3 × 100 × (66,000 − 60,000) / (60,000 × 66,000) = 0.000454545…
    expect(inverseUnrealizedPnl("3", "60000", "66000", "100")).toBe("0.00045455");
    // A short gains as the price falls: 300 × 6,000 / (60,000 × 54,000).
    expect(inverseUnrealizedPnl("-3", "60000", "54000", "100")).toBe("0.00055556");
    expect(inverseUnrealizedPnl("3", "60000", null, "100")).toBe("0");
    const live = contractMath(btcUsd).live({ quantity: "3", entry_price: "60000", margin: "0.015", mark_price: null, unrealized_pnl: null }, "66000");
    expect(live).toEqual({ markPrice: "66000", unrealizedPnl: "0.00045455", roe: "0.030303" });
  });

  it("measures min_notional in USD on a coin-margined contract and in the quote on a linear one", () => {
    expect(contractMath(btcUsd).orderNotional("2", "60000")).toBe("200");
    expect(contractMath(btcUsd).notionalUnit).toBe("USD");
    expect(contractMath(btcUsdt).orderNotional("0.01", "60000")).toBe("600");
    expect(contractMath(btcUsdt).notionalUnit).toBe("USDT");
  });

  it("turns an amount in contracts, the coin or USD into whole contracts and back (B130)", () => {
    expect(toContracts("3", "CONT", "100", "60000")).toBe("3");
    // 0.0051 BTC at 60,000 is 306 USD: 3 whole contracts of 100.
    expect(toContracts("0.0051", "COIN", "100", "60000")).toBe("3");
    expect(toContracts("350", "USD", "100", "60000")).toBe("3");
    // Under one contract's worth still buys one.
    expect(toContracts("0.0001", "COIN", "100", "60000")).toBe("1");
    expect(toContracts("20", "USD", "100", "60000")).toBe("1");
    expect(toContracts("0", "USD", "100", "60000")).toBe("");
    expect(toContracts("0.01", "COIN", "100", "")).toBe("");
    expect(fromContracts("3", "CONT", "100", "60000")).toBe("3");
    expect(fromContracts("3", "COIN", "100", "60000")).toBe("0.005");
    expect(fromContracts("3", "USD", "100", "60000")).toBe("300");
    expect(fromContracts("", "USD", "100", "60000")).toBe("");
  });

  it("leaves a linear contract's arithmetic as it was", () => {
    const m = contractMath(btcUsdt);
    expect(m.inverse).toBe(false);
    expect(m.settle).toBe("USDT");
    expect(m.openCost("60000", "0.01", 20)).toBe(openCost("60000", "0.01", 20, "0.0005"));
    expect(m.maxOpen("1000", "60000", 20)).toBe("0.33");
    expect(m.worth("-0.5", "60000")).toBe("30000");
  });
});
