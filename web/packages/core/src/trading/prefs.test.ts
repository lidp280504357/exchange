import { describe, expect, it } from "vitest";
import { useTerminalPrefs } from "./prefs";

describe("terminal preferences", () => {
  it("keep no side effect, nor one an older build stored", async () => {
    localStorage.setItem(
      "exchange.terminal",
      JSON.stringify({ state: { tradeAccount: "MARGIN_CROSS", sideEffect: "AUTO_BORROW", interval: "1h" }, version: 1 }),
    );
    await useTerminalPrefs.persist.rehydrate();
    const s = useTerminalPrefs.getState();
    expect(s.sideEffect).toBe("NONE");
    expect(s.tradeAccount).toBe("MARGIN_CROSS");
    expect(s.interval).toBe("1h");
    s.set({ sideEffect: "AUTO_REPAY" });
    const stored = JSON.parse(localStorage.getItem("exchange.terminal") ?? "{}");
    expect(stored.version).toBe(2);
    expect(stored.state).not.toHaveProperty("sideEffect");
    expect(stored.state.tradeAccount).toBe("MARGIN_CROSS");
  });
});
