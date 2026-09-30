import { describe, expect, it } from "vitest";
import { maxWithdrawable, withdrawQuote } from "./withdraw";

// ETH on Sepolia: 18 decimals, minimum 0.001, fee 0.0002 (deploy/instruments/test.json).
const eth = { available: "1", fee: "0.0002", min: "0.001", decimals: 18 };

describe("withdrawQuote", () => {
  it("charges the fee on top: the amount arrives, amount + fee leaves", () => {
    const q = withdrawQuote({ ...eth, amount: "0.5" });
    expect(q).toMatchObject({ fee: "0.0002", received: "0.5", total: "0.5002", issue: null });
  });

  it("offers everything but the fee as the maximum", () => {
    const q = withdrawQuote({ ...eth, amount: "" });
    expect(q.max).toBe("0.9998");
    expect(q.enough).toBe(true);
    expect(q.issue).toBeNull();
    expect(q.received).toBeNull();
    // The maximum itself passes, one unit more does not.
    expect(withdrawQuote({ ...eth, amount: "0.9998" }).issue).toBeNull();
    expect(withdrawQuote({ ...eth, amount: "0.999800000000000001" }).issue).toBe("insufficient");
  });

  it("refuses less than the minimum, and the minimum itself passes", () => {
    expect(withdrawQuote({ ...eth, amount: "0.0009" }).issue).toBe("belowMin");
    expect(withdrawQuote({ ...eth, amount: "0.001" })).toMatchObject({ issue: null, total: "0.0012" });
  });

  it("checks the typed amount first", () => {
    expect(withdrawQuote({ ...eth, amount: "abc" }).issue).toBe("format");
    expect(withdrawQuote({ ...eth, amount: "0" }).issue).toBe("zero");
    expect(withdrawQuote({ ...eth, amount: "0.1234567", decimals: 6 }).issue).toBe("precision");
    expect(withdrawQuote({ ...eth, amount: " 0.5 " }).issue).toBeNull();
  });

  it("takes no fee to another user's deposit address", () => {
    const q = withdrawQuote({ ...eth, amount: "1", internal: true });
    expect(q).toMatchObject({ fee: "0", received: "1", total: "1", max: "1", issue: null });
  });

  it("says when the balance cannot cover the minimum and the fee", () => {
    expect(withdrawQuote({ ...eth, available: "0.0011", amount: "" })).toMatchObject({ max: "0.0009", enough: false });
    expect(withdrawQuote({ ...eth, available: "0.0001", amount: "" })).toMatchObject({ max: "0", enough: false });
    expect(withdrawQuote({ ...eth, available: "0.0012", amount: "" })).toMatchObject({ max: "0.001", enough: true });
  });

  it("never rounds the maximum up", () => {
    expect(maxWithdrawable("10.123456789", "0.5", 6)).toBe("9.623456");
    expect(maxWithdrawable("0.5", "0.5", 6)).toBe("0");
    expect(maxWithdrawable("100", "1", 2)).toBe("99");
  });
});
