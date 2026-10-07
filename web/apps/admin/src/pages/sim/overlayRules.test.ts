import { describe, expect, it } from "vitest";
import { moveOf, newOverlayDraft, overlayBody, targetOf, type OverlayDraft } from "./overlayRules";

const draft = (over: Partial<OverlayDraft>): OverlayDraft => ({ ...newOverlayDraft(), symbols: ["BTC-USDT"], ...over });

describe("a price event on any pair", () => {
  it("makes the body market-sim takes, the leverage reached unless spared", () => {
    expect(overlayBody(draft({ pct: "16" }))).toEqual({
      body: { type: "OVERLAY", symbols: ["BTC-USDT"], target_pct: 16, ramp_up_seconds: 15, ramp_down_seconds: 5, risk: true },
    });
    expect(overlayBody(draft({ symbols: ["BTC-USDT", "ETH-USDT"], pct: "-2.5", hold: "30", spare: true }))).toEqual({
      body: { type: "OVERLAY", symbols: ["BTC-USDT", "ETH-USDT"], target_pct: -2.5, ramp_up_seconds: 15, hold_seconds: 30, ramp_down_seconds: 5, risk: false },
    });
    expect(overlayBody(draft({ mode: "price", price: " 100000 " }))).toEqual({
      body: { type: "OVERLAY", symbols: ["BTC-USDT"], target_price: "100000", ramp_up_seconds: 15, ramp_down_seconds: 5, risk: true },
    });
  });

  it("says what is wrong first", () => {
    const problem = (d: OverlayDraft) => ("problem" in overlayBody(d) ? (overlayBody(d) as { problem: string }).problem : null);
    expect(problem(draft({ symbols: [] }))).toBe("pairs");
    expect(problem(draft({ symbols: Array.from({ length: 11 }, (_, i) => `C${i}-USDT`) }))).toBe("pairs");
    expect(problem(draft({ symbols: ["BTC-USDT", "ETH-USDT"], mode: "price", price: "100" }))).toBe("onePrice");
    expect(problem(draft({ mode: "price", price: "0" }))).toBe("price");
    expect(problem(draft({ mode: "price", price: "abc" }))).toBe("price");
    for (const pct of ["0", "", "x", "90.01", "-91", "1e2"]) expect(problem(draft({ pct }))).toBe("pct");
    expect(problem(draft({ pct: "90" }))).toBe(null);
    expect(problem(draft({ pct: "-90" }))).toBe(null);
    expect(problem(draft({ up: "0" }))).toBe("seconds");
    expect(problem(draft({ down: "2" }))).toBe("seconds");
    expect(problem(draft({ hold: "1.5" }))).toBe("seconds");
    expect(problem(draft({ hold: "-1" }))).toBe("seconds");
    expect(problem(draft({ up: "300", hold: "200", down: "101" }))).toBe("total");
    expect(problem(draft({ up: "300", hold: "200", down: "100" }))).toBe(null);
  });

  it("tells where the target takes a pair's price", () => {
    expect(targetOf(draft({ pct: "16" }), "84100")).toBe("97556");
    expect(targetOf(draft({ pct: "-2.5" }), "84100.5")).toBe("81997.9875");
    expect(targetOf(draft({ pct: "16" }), undefined)).toBe(null);
    expect(targetOf(draft({ mode: "price", price: "90000" }), undefined)).toBe("90000");
    expect(moveOf("97556", "84100")).toBeCloseTo(0.16, 8);
    expect(moveOf(null, "84100")).toBe(null);
  });
});
