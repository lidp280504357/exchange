import { beforeEach, describe, expect, it } from "vitest";
import { useTerminalPrefs } from "./prefs";

const initial = useTerminalPrefs.getState();

describe("terminal preferences", () => {
  beforeEach(() => {
    localStorage.clear();
    useTerminalPrefs.setState(initial, true);
  });

  it("start from the defaults on a fresh install, and keep no side effect", async () => {
    await useTerminalPrefs.persist.rehydrate();
    const s = useTerminalPrefs.getState();
    expect(s).toMatchObject({ tradeAccount: "SPOT", sideEffect: "NONE", interval: "15m" });
    s.set({ sideEffect: "AUTO_BORROW", tradeAccount: "MARGIN_ISOLATED" });
    const stored = JSON.parse(localStorage.getItem("exchange.terminal") ?? "{}");
    expect(stored).toMatchObject({ version: 2, state: { tradeAccount: "MARGIN_ISOLATED" } });
    expect(stored.state).not.toHaveProperty("sideEffect");
  });

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
