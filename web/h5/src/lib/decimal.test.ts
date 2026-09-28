import { describe, expect, it } from "vitest";
import { checkAmount, compare, format } from "./decimal";

describe("decimal", () => {
  it("validates input against precision", () => {
    expect(checkAmount("12.5", 6)).toBe("ok");
    expect(checkAmount("0.0000001", 6)).toBe("precision");
    expect(checkAmount("0.000", 6)).toBe("zero");
    expect(checkAmount("1e5", 6)).toBe("format");
    expect(checkAmount("-1", 6)).toBe("format");
    expect(checkAmount("01", 6)).toBe("format");
    expect(checkAmount("10", 0)).toBe("ok");
  });
  it("compares without floating point", () => {
    expect(compare("10000", "9999.999999")).toBe(1);
    expect(compare("0.1", "0.10")).toBe(0);
    expect(compare("2500.5", "2500.50001")).toBe(-1);
    expect(compare("0.30000000000000004", "0.3")).toBe(1);
  });
  it("groups digits", () => {
    expect(format("7499.5")).toBe("7,499.5");
    expect(format("1234567.000001")).toBe("1,234,567.000001");
    expect(format("-1000")).toBe("-1,000");
    expect(format("0")).toBe("0");
  });
});
