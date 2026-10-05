import { describe, expect, it } from "vitest";
import { marginSupport, marginTag, spendable, tradeAccountFor } from "./trade";

const pair = { symbol: "BTC-USDT", base_asset: "BTC", quote_asset: "USDT" };
const assets = [
  { asset: "BTC", collateral: true },
  { asset: "USDT", collateral: true },
  { asset: "LINK", collateral: false },
];
const pairs = [
  { symbol: "BTC-USDT", base: "BTC", quote: "USDT", isolated: true, leverage: 10, warn_level: "1.1", liquidation_level: "1.05", liquidation_fee_rate: "0.02" },
  { symbol: "LINK-USDT", base: "LINK", quote: "USDT", isolated: false, leverage: 3, warn_level: "1.25", liquidation_level: "1.15", liquidation_fee_rate: "0.02" },
];

describe("marginSupport", () => {
  it("needs both coins as margin for cross and the pair's isolated terms for isolated", () => {
    const btc = marginSupport(pair, assets, pairs);
    expect(btc.cross).toBe(true);
    expect(btc.isolated?.leverage).toBe(10);
    const link = marginSupport({ symbol: "LINK-USDT", base_asset: "LINK", quote_asset: "USDT" }, assets, pairs);
    expect(link).toEqual({ cross: false, isolated: null });
    expect(marginSupport(pair, undefined, undefined)).toEqual({ cross: false, isolated: null });
  });
});

describe("tradeAccountFor", () => {
  const both = marginSupport(pair, assets, pairs);
  it("keeps the choice where it is open and the pair has the account, else SPOT", () => {
    expect(tradeAccountFor("MARGIN_ISOLATED", true, both)).toBe("MARGIN_ISOLATED");
    expect(tradeAccountFor("MARGIN_CROSS", false, both)).toBe("SPOT");
    expect(tradeAccountFor("MARGIN_ISOLATED", true, { cross: true, isolated: null })).toBe("SPOT");
    expect(tradeAccountFor("MARGIN_CROSS", true, { cross: false, isolated: null })).toBe("SPOT");
    expect(tradeAccountFor("SPOT", true, both)).toBe("SPOT");
  });
});

describe("spendable", () => {
  const owner = { balances: [{ asset: "USDT", free: "100", locked: "5", borrowed: "50", interest: "0.01", net: "54.99" }] };
  it("is the free balance, and with AUTO_BORROW also what may be borrowed", () => {
    expect(spendable(owner, "USDT", "200", "NONE")).toBe("100");
    expect(spendable(owner, "USDT", "200", "AUTO_REPAY")).toBe("100");
    expect(spendable(owner, "USDT", "200", "AUTO_BORROW")).toBe("300");
    expect(spendable(owner, "USDT", undefined, "AUTO_BORROW")).toBe("100");
    expect(spendable(owner, "BTC", "0.5", "AUTO_BORROW")).toBe("0.5");
    expect(spendable(undefined, "BTC", "-1", "AUTO_BORROW")).toBe("0");
  });
});

describe("marginTag", () => {
  it("labels margin orders only", () => {
    expect(marginTag("MARGIN_CROSS")).toBe("CROSS");
    expect(marginTag("MARGIN_ISOLATED")).toBe("ISOLATED");
    expect(marginTag("SPOT")).toBeNull();
    expect(marginTag(undefined)).toBeNull();
  });
});
