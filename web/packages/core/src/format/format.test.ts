import { describe, expect, it } from "vitest";
import * as d from "./decimal";
import { formatAmount, formatDecimal, formatPercent, formatPrice, group } from "./number";
import { formatRelative, formatTime } from "./time";

describe("decimal", () => {
  it("adds, subtracts and multiplies exactly", () => {
    expect(d.add("0.1", "0.2")).toBe("0.3");
    expect(d.sub("1", "1.000001")).toBe("-0.000001");
    expect(d.mul("63214.5", "0.0001")).toBe("6.32145");
    expect(d.add("-0.5", "0.5")).toBe("0");
  });
  it("divides with the rounding asked for", () => {
    expect(d.div("1", "3", 4)).toBe("0.3333");
    expect(d.div("2", "3", 4, "half")).toBe("0.6667");
    expect(d.div("1", "3", 2, "up")).toBe("0.34");
    expect(d.div("-1", "3", 2, "down")).toBe("-0.33");
    expect(d.div("10", "4", 0, "half")).toBe("3");
    expect(() => d.div("1", "0", 2)).toThrow();
  });
  it("compares and rounds", () => {
    expect(d.cmp("10", "9.99")).toBe(1);
    expect(d.cmp("0.10", "0.1")).toBe(0);
    expect(d.round("1.23456", 2)).toBe("1.23");
    expect(d.round("1.235", 2, "half")).toBe("1.24");
    expect(d.round("1.231", 2, "up")).toBe("1.24");
    expect(d.normalize("1.2300")).toBe("1.23");
  });
  it("snaps to steps", () => {
    expect(d.quantize("63214.57", "0.1")).toBe("63214.5");
    expect(d.quantize("63214.51", "0.1", "up")).toBe("63214.6");
    expect(d.quantize("0.00012345", "0.0001")).toBe("0.0001");
    expect(d.isMultipleOf("0.0003", "0.0001")).toBe(true);
    expect(d.isMultipleOf("0.00015", "0.0001")).toBe(false);
    expect(d.decimalsOf("0.0100")).toBe(2);
    expect(d.decimalsOf("1")).toBe(0);
  });
  it("checks user input", () => {
    expect(d.checkAmount("1.5", 6)).toBe("ok");
    expect(d.checkAmount("1.1234567", 6)).toBe("precision");
    expect(d.checkAmount("0.000", 6)).toBe("zero");
    expect(d.checkAmount("1e5", 6)).toBe("format");
    expect(d.checkAmount("-1", 6)).toBe("format");
  });
});

describe("number formatting", () => {
  it("groups thousands and pads to decimals", () => {
    expect(group("1234567.891")).toBe("1,234,567.891");
    expect(formatPrice("63214.5", 2)).toBe("63,214.50");
    expect(formatPrice("0.012345", 4)).toBe("0.0123");
    expect(formatAmount("1234.56789", 2)).toBe("1,234.56");
    expect(formatAmount(null)).toBe("—");
    expect(formatDecimal("5", { sign: true })).toBe("+5");
    expect(formatDecimal("0", { sign: true })).toBe("0");
  });
  it("renders fractions as percentages", () => {
    expect(formatPercent("0.01253")).toBe("+1.25%");
    expect(formatPercent("-0.00624048")).toBe("-0.62%");
    expect(formatPercent("0")).toBe("0.00%");
    expect(formatPercent(null)).toBe("—");
    expect(formatPercent("1.5", 0)).toBe("+150%");
  });
});

describe("time formatting", () => {
  it("formats in the time zone asked for", () => {
    const t = "2026-09-30T10:13:12.016Z";
    expect(formatTime(t, "datetimeSeconds", "en", "UTC")).toBe("2026-09-30 10:13:12");
    expect(formatTime(t, "datetime", "zh-CN", "Asia/Shanghai")).toBe("2026-09-30 18:13");
    expect(formatTime(t, "timeSeconds", "zh-CN", "UTC")).toBe("10:13:12");
    expect(formatTime(undefined)).toBe("—");
  });
  it("says how long ago", () => {
    const now = Date.parse("2026-09-30T10:13:00Z");
    expect(formatRelative("2026-09-30T10:10:00Z", now, "en")).toBe("3 minutes ago");
    expect(formatRelative("2026-09-30T10:10:00Z", now, "zh-CN")).toBe("3分钟前");
  });
});
