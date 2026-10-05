import "../test/setup";
import type { BookView } from "@exchange/core";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { OrderBook } from "./OrderBook";

// The step box when core's OrderBook.fit cut the view finer than the step
// chosen (B73, B75): "≈ 1" in the trigger and why, on hover and for screen
// readers; the reason differs while a finer step is only held.

const steps = ["0.1", "1", "10"];
const view = (more: Partial<BookView>): BookView => ({ bids: [], asks: [], maxTotal: "0", spread: null, seq: 0, ...more });

function why() {
  const trigger = screen.getByRole("combobox", { name: "Price step" });
  const described = document.getElementById(trigger.getAttribute("aria-describedby") ?? "")?.textContent ?? "";
  return { shown: trigger.textContent?.replace(/\s+/g, " ").trim(), title: trigger.parentElement?.title, described };
}

describe("OrderBook's step box", () => {
  it("says the chosen step cannot fill the book", () => {
    render(<OrderBook view={view({ step: "1", fits: ["0.1", "1"], fills: ["0.1", "1"] })} priceDecimals={1} qtyDecimals={4} steps={steps} step="10" />);
    const w = why();
    expect(w.shown).toBe("≈ 1");
    expect(w.title).toBe("Not enough depth to fill the book at 10: shown at 1");
    expect(w.described).toBe(w.title);
  });
  it("says the chosen step fills it with no levels to spare while a finer one is held", () => {
    render(<OrderBook view={view({ step: "1", fits: ["0.1", "1"], fills: steps })} priceDecimals={1} qtyDecimals={4} steps={steps} step="10" />);
    const w = why();
    expect(w.shown).toBe("≈ 1");
    expect(w.title).toBe("10 just fills the rows, with none to spare: shown at 1 for now");
    expect(w.described).toBe(w.title);
  });
  it("shows the chosen step plainly when the view is cut at it", () => {
    render(<OrderBook view={view({ step: "10", fits: steps, fills: steps })} priceDecimals={1} qtyDecimals={4} steps={steps} step="10" />);
    const w = why();
    expect(w.shown).toBe("10");
    expect(w.title).toBe("");
    expect(w.described).toBe("");
  });
});
