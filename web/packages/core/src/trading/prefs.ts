import { useCallback, useMemo } from "react";
import { create } from "zustand";
import { persist } from "zustand/middleware";
import { bookSteps, defaultBookStep } from "./pairs";

// The terminal's layout preferences (design §6.2), kept on the device:
// the order book view and its step per pair, the chart interval and
// indicators, the depth chart, the bottom panel's height, the pairs
// traded lately (the top bar's spot menu and the mobile trade tab), and
// the account spot orders trade from.

export type BookViewMode = "both" | "bids" | "asks";

type TerminalPrefs = {
  bookMode: BookViewMode;
  /** The book's aggregation step per symbol ("" = defaultBookStep). */
  bookStep: Record<string, string>;
  interval: string;
  indicators: string[];
  showDepthChart: boolean;
  /** Height of the orders panel in px. */
  panelHeight: number;
  /** Symbols traded lately, most recent first (spot and futures). */
  recent: string[];
  /**
   * The account spot orders trade from (margin design 2026-10-06 §7) and a
   * margin order's side effect; the terminal falls back to SPOT where
   * margin trading is not open or the pair has no such account.
   */
  tradeAccount: "SPOT" | "MARGIN_CROSS" | "MARGIN_ISOLATED";
  sideEffect: "NONE" | "AUTO_BORROW" | "AUTO_REPAY";
  set: (patch: Partial<Omit<TerminalPrefs, "set" | "visit" | "setStep">>) => void;
  setStep: (symbol: string, step: string) => void;
  /** visit puts a symbol first in the recent list (at most 6). */
  visit: (symbol: string) => void;
};

export const useTerminalPrefs = create<TerminalPrefs>()(
  persist(
    (set) => ({
      bookMode: "both",
      bookStep: {},
      interval: "15m",
      indicators: ["MA", "VOL"],
      showDepthChart: false,
      panelHeight: 300,
      recent: [],
      tradeAccount: "SPOT",
      sideEffect: "NONE",
      set: (patch) => set(patch),
      setStep: (symbol, step) => set((s) => ({ bookStep: { ...s.bookStep, [symbol]: step } })),
      visit: (symbol) => set((s) => ({ recent: [symbol, ...s.recent.filter((x) => x !== symbol)].slice(0, 6) })),
    }),
    // A side effect stays for the visit only: a borrow is never made by a
    // choice remembered from another day. Version 1 kept it: the migration
    // drops what an older build stored (review CW2, B99).
    {
      name: "exchange.terminal",
      version: 2,
      partialize: ({ sideEffect: _, ...kept }) => kept,
      migrate: (persisted) => {
        const { sideEffect: _, ...kept } = (persisted ?? {}) as Partial<TerminalPrefs>;
        return kept as TerminalPrefs;
      },
    },
  ),
);

/**
 * useBookStep is a book's aggregation: the steps it offers, the one shown
 * (the user's choice for the symbol, kept on the device, else
 * defaultBookStep at the price) and how to choose one. Choosing the default
 * stores nothing, so the book follows the price's magnitude again.
 */
export function useBookStep(symbol: string, tickSize: string, price: string | null | undefined) {
  const chosen = useTerminalPrefs((s) => s.bookStep[symbol] ?? "");
  const steps = useMemo(() => bookSteps(tickSize), [tickSize]);
  const auto = defaultBookStep(steps, price);
  const step = chosen && steps.includes(chosen) ? chosen : auto;
  const setStep = useCallback((s: string) => useTerminalPrefs.getState().setStep(symbol, s === auto ? "" : s), [symbol, auto]);
  return { steps, step, setStep };
}
