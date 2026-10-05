import { describe, expect, it } from "vitest";
import { dayRange, dayStart, inRange, LEDGER_ENTRY_TYPES, ledgerRelation, pastRange, presetRange } from "./ledger";
import { checkTransfer, otherAccount, transferMax } from "./transfer";

describe("ledgerRelation", () => {
  it("links every entry type to the page it belongs to", () => {
    expect(LEDGER_ENTRY_TYPES.every((t) => ledgerRelation(t) !== null)).toBe(true);
    expect(ledgerRelation("DEPOSIT_CREDIT")).toBe("deposit");
    expect(ledgerRelation("WITHDRAW_SETTLE")).toBe("withdraw");
    expect(ledgerRelation("INTERNAL_TRANSFER")).toBe("withdraw");
    expect(ledgerRelation("ACCOUNT_TRANSFER")).toBe("transfer");
    expect(ledgerRelation("TRADE_FEE")).toBe("spot");
    expect(ledgerRelation("TRADE_FEE", "FUTURES")).toBe("futures");
    expect(ledgerRelation("FUNDING_PAYMENT")).toBe("futures");
    expect(ledgerRelation("MANUAL_ADJUSTMENT")).toBe("adjustment");
    expect(ledgerRelation("MARGIN_BORROW", "MARGIN_CROSS")).toBe("margin");
    expect(ledgerRelation("MARGIN_TRANSFER_IN")).toBe("margin");
    expect(ledgerRelation("ORDER_FREEZE", "MARGIN_ISOLATED")).toBe("margin");
    expect(ledgerRelation("SOMETHING_NEW")).toBeNull();
  });
});

describe("time ranges", () => {
  const now = Date.parse("2026-09-30T12:00:00Z");

  it("rolls presets back from now", () => {
    expect(presetRange("7d", now)).toEqual({ from: Date.parse("2026-09-23T12:00:00Z"), to: null });
    expect(presetRange("90d", now).from).toBe(Date.parse("2026-07-02T12:00:00Z"));
    expect(presetRange("all", now)).toEqual({ from: null, to: null });
  });

  it("starts calendar days in the user's time zone", () => {
    expect(dayStart("2026-09-30", "UTC")).toBe(Date.parse("2026-09-30T00:00:00Z"));
    expect(dayStart("2026-09-30", "Asia/Shanghai")).toBe(Date.parse("2026-09-29T16:00:00Z"));
    // Daylight saving in New York: starts on 8 March, ends on 1 November 2026.
    expect(dayStart("2026-03-08", "America/New_York")).toBe(Date.parse("2026-03-08T05:00:00Z"));
    expect(dayStart("2026-03-09", "America/New_York")).toBe(Date.parse("2026-03-09T04:00:00Z"));
    expect(dayStart("2026-11-01", "America/New_York")).toBe(Date.parse("2026-11-01T04:00:00Z"));
    expect(dayStart("30/09/2026", "UTC")).toBeNull();
  });

  it("covers whole days, the last one included", () => {
    expect(dayRange("2026-09-01", "2026-09-30", "Asia/Shanghai")).toEqual({
      from: Date.parse("2026-08-31T16:00:00Z"),
      to: Date.parse("2026-09-30T16:00:00Z"),
    });
    expect(dayRange("", "2026-03-08", "America/New_York")).toEqual({ from: null, to: Date.parse("2026-03-09T04:00:00Z") });
    expect(dayRange("2026-09-01", "", "UTC")).toEqual({ from: Date.parse("2026-09-01T00:00:00Z"), to: null });
  });

  it("filters times and tells when paging can stop", () => {
    const r = { from: Date.parse("2026-09-01T00:00:00Z"), to: Date.parse("2026-09-02T00:00:00Z") };
    expect(inRange("2026-09-01T00:00:00Z", r)).toBe(true);
    expect(inRange("2026-09-01T23:59:59Z", r)).toBe(true);
    expect(inRange("2026-09-02T00:00:00Z", r)).toBe(false);
    expect(inRange("not a time", r)).toBe(false);
    expect(pastRange("2026-08-31T23:59:59Z", r)).toBe(true);
    expect(pastRange("2026-09-01T00:00:00Z", r)).toBe(false);
    expect(pastRange("2000-01-01T00:00:00Z", { from: null, to: null })).toBe(false);
  });
});

describe("transfers", () => {
  it("swaps the accounts", () => {
    expect(otherAccount("SPOT")).toBe("FUTURES");
    expect(otherAccount("FUTURES")).toBe("SPOT");
  });

  it("bounds what leaves FUTURES by the transferable amount", () => {
    expect(transferMax("100.5")).toBe("100.5");
    expect(transferMax("100.5", "80.25")).toBe("80.25");
    expect(transferMax("100.5", "120")).toBe("100.5");
    expect(transferMax("100.5", "-3")).toBe("0");
    expect(transferMax("0")).toBe("0");
  });

  it("checks the amount exactly against the precision and the maximum", () => {
    expect(checkTransfer("", "10", 6)).toBeNull();
    expect(checkTransfer("10", "10", 6)).toBeNull();
    expect(checkTransfer("10.000001", "10", 6)).toBe("insufficient");
    expect(checkTransfer("1.1234567", "10", 6)).toBe("precision");
    expect(checkTransfer("0", "10", 6)).toBe("zero");
    expect(checkTransfer("1e3", "10", 6)).toBe("format");
    expect(checkTransfer("-1", "10", 6)).toBe("format");
  });
});
