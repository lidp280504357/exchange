import { describe, expect, it } from "vitest";
import { accountId, balanceOf, gaugeShare, hasDebt, isEmpty, levelText, levelZone, owed, repayMax } from "./math";

describe("levelZone", () => {
  it("places a level against the cross account's 1.3 and 1.1", () => {
    expect(levelZone(null, "1.3", "1.1")).toBe("none");
    expect(levelZone("1.29", "1.3", "1.1")).toBe("danger");
    expect(levelZone("1.3", "1.3", "1.1")).toBe("caution");
    expect(levelZone("1.69", "1.3", "1.1")).toBe("caution");
    expect(levelZone("1.7", "1.3", "1.1")).toBe("safe");
    expect(levelZone("999", "1.3", "1.1")).toBe("safe");
  });

  it("narrows the caution band with an isolated 10x account's 1.10 and 1.05", () => {
    expect(levelZone("1.19", "1.1", "1.05")).toBe("caution");
    expect(levelZone("1.2", "1.1", "1.05")).toBe("safe");
  });

  it("reads anything that is not a number as no debts", () => {
    expect(levelZone("", "1.3", "1.1")).toBe("none");
    expect(levelZone(undefined, "1.3", "1.1")).toBe("none");
  });
});

describe("levelText", () => {
  it("shows two decimals, rounded down, and 999 without debts or beyond", () => {
    expect(levelText("1.6666")).toBe("1.66");
    expect(levelText("2")).toBe("2");
    expect(levelText(null)).toBe("999");
    expect(levelText("1500.5")).toBe("999");
  });
});

describe("gaugeShare", () => {
  it("runs from the liquidation level to twice the warning level", () => {
    expect(gaugeShare("1.1", "1.3", "1.1")).toBe(0);
    expect(gaugeShare("2.6", "1.3", "1.1")).toBe(1);
    expect(gaugeShare("1.85", "1.3", "1.1")).toBe(0.5);
    expect(gaugeShare("1", "1.3", "1.1")).toBe(0);
    expect(gaugeShare("40", "1.3", "1.1")).toBe(1);
    expect(gaugeShare(null, "1.3", "1.1")).toBe(1);
  });
});

describe("debts", () => {
  const b = { asset: "USDT", free: "120.5", locked: "10", borrowed: "100", interest: "0.0015", net: "30.4985" };

  it("adds principal and interest", () => {
    expect(owed(b)).toBe("100.0015");
    expect(hasDebt(b)).toBe(true);
    expect(hasDebt({ borrowed: "0", interest: "0" })).toBe(false);
  });

  it("repays at most the debt, as far as the free balance goes", () => {
    expect(repayMax(b)).toBe("100.0015");
    expect(repayMax({ ...b, free: "50" })).toBe("50");
    expect(repayMax({ ...b, borrowed: "0", interest: "0" })).toBe("0");
  });
});

describe("accounts", () => {
  it("names the cross account and each isolated account", () => {
    expect(accountId({ account: "MARGIN_CROSS", symbol: null })).toBe("MARGIN_CROSS");
    expect(accountId({ account: "MARGIN_ISOLATED", symbol: "BTC-USDT" })).toBe("MARGIN_ISOLATED:BTC-USDT");
  });

  it("finds an asset's row, zero without one, and tells an empty account", () => {
    const a = { balances: [{ asset: "BTC", free: "0", locked: "0", borrowed: "0", interest: "0.0001", net: "-0.0001" }] };
    expect(balanceOf(a, "BTC").interest).toBe("0.0001");
    expect(balanceOf(a, "ETH")).toEqual({ asset: "ETH", free: "0", locked: "0", borrowed: "0", interest: "0", net: "0" });
    expect(isEmpty(a)).toBe(false);
    expect(isEmpty({ balances: [] })).toBe(true);
  });
});
