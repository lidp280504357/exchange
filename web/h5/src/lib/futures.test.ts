import { describe, expect, it } from "vitest";
import { addDecimals, countdown, divCeil, orderCost, pnlTone, ratePercent, signed } from "./futures";

describe("futures helpers", () => {
  it("counts down to the next funding", () => {
    const now = Date.parse("2026-09-30T05:59:30Z");
    expect(countdown("2026-09-30T08:00:00Z", now)).toBe("02:00:30");
    expect(countdown("2026-09-30T05:00:00Z", now)).toBe("00:00:00");
    expect(countdown(null, now)).toBe("—");
  });
  it("divides and adds exactly", () => {
    expect(divCeil("6000", 7, 6)).toBe("857.142858");
    expect(divCeil("6000", 10, 6)).toBe("600");
    expect(addDecimals(6, "600", "3", "-0.5")).toBe("602.5");
  });
  it("estimates an order's reservation", () => {
    // 0.1 x 60000 = 6000: 600 of margin at 10x and a 3 taker fee.
    expect(orderCost("60000", "0.1", 10, "0.0005", 6)).toBe("603");
  });
  it("formats rates and signed amounts", () => {
    expect(ratePercent("0.0001")).toBe("0.0100%");
    expect(ratePercent("-0.00012345")).toBe("-0.0123%");
    expect(ratePercent("0.0075")).toBe("0.7500%");
    expect(signed("1.5")).toBe("+1.5");
    expect(signed("-1.5")).toBe("-1.5");
    expect(pnlTone("-2")).toBe("text-red-400");
    expect(pnlTone("0")).toBe("text-gray-300");
  });
});
