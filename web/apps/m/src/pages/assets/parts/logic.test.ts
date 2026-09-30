import type { AssetRow } from "@exchange/core/assets/valuation";
import { describe, expect, it } from "vitest";
import { currentStep, filterAssets, filterCount, groupByDay, ledgerTotals, matchAddress } from "./logic";

const row = (asset: string, total: string, value: string | null): AssetRow => ({ asset, available: total, frozen: "0", total, price: null, value });

const names: Record<string, string> = { BTC: "Bitcoin", ETH: "Ethereum", USDT: "Tether", DOGE: "Dogecoin" };
const name = (a: string) => names[a] ?? a;

describe("filterAssets", () => {
  const rows = [row("BTC", "0.5", "30000"), row("ETH", "0.0001", "0.3"), row("USDT", "0", "0"), row("DOGE", "12", null)];

  it("keeps everything without a search or the switch", () => {
    expect(filterAssets(rows, { query: "", hideSmall: false, name }).map((r) => r.asset)).toEqual(["BTC", "ETH", "USDT", "DOGE"]);
  });

  it("matches the code or the name in any case, ignoring spaces around", () => {
    expect(filterAssets(rows, { query: " btc ", hideSmall: false, name }).map((r) => r.asset)).toEqual(["BTC"]);
    expect(filterAssets(rows, { query: "coin", hideSmall: false, name }).map((r) => r.asset)).toEqual(["BTC", "DOGE"]);
    // Names count too: "eth" is in Tether.
    expect(filterAssets(rows, { query: "ETH", hideSmall: false, name }).map((r) => r.asset)).toEqual(["ETH", "USDT"]);
    expect(filterAssets(rows, { query: "xyz", hideSmall: false, name })).toEqual([]);
  });

  it("hides small and empty balances, never unpriced ones", () => {
    expect(filterAssets(rows, { query: "", hideSmall: true, name }).map((r) => r.asset)).toEqual(["BTC", "DOGE"]);
  });
});

describe("matchAddress", () => {
  const entry = { label: "My cold wallet", address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed" };

  it("matches the label or the address in any case", () => {
    expect(matchAddress(entry, "")).toBe(true);
    expect(matchAddress(entry, "cold")).toBe(true);
    expect(matchAddress(entry, "0x5AAEB")).toBe(true);
    expect(matchAddress(entry, "beaed ")).toBe(true);
    expect(matchAddress(entry, "hot")).toBe(false);
  });
});

describe("currentStep", () => {
  it("is the step in progress, the failed one, or the end", () => {
    expect(currentStep([{ state: "done" }, { state: "current" }, { state: "upcoming" }])).toBe(1);
    expect(currentStep([{ state: "done" }, { state: "error" }])).toBe(1);
    expect(currentStep([{ state: "done" }, { state: "done" }, { state: "done" }])).toBe(3);
    expect(currentStep([])).toBe(0);
  });
});

describe("filterCount", () => {
  it("counts the filters that narrow the list", () => {
    expect(filterCount({ asset: "", type: "", range: "all" })).toBe(0);
    expect(filterCount({ asset: "USDT", type: "", range: "all" })).toBe(1);
    expect(filterCount({ asset: "USDT", type: "DEPOSIT_CREDIT", range: "7d" })).toBe(3);
    expect(filterCount({ asset: "", type: "", range: "custom" })).toBe(1);
  });
});

describe("ledgerTotals", () => {
  it("adds up the available lines exactly, in and out apart", () => {
    const totals = ledgerTotals([
      { amount: "10000", balance_kind: "AVAILABLE" },
      { amount: "-12.34", balance_kind: "AVAILABLE" },
      { amount: "12.34", balance_kind: "FROZEN" },
      { amount: "0.1", balance_kind: "AVAILABLE" },
      { amount: "0.2", balance_kind: "AVAILABLE" },
      { amount: "-0.000000000000000001", balance_kind: "AVAILABLE" },
      { amount: "oops", balance_kind: "AVAILABLE" },
    ]);
    expect(totals).toEqual({ inflow: "10000.3", outflow: "-12.340000000000000001" });
  });

  it("is zero for nothing", () => {
    expect(ledgerTotals([])).toEqual({ inflow: "0", outflow: "0" });
  });
});

describe("groupByDay", () => {
  it("splits a newest-first list into runs of one day, in order", () => {
    const items = [
      { id: 1, at: "2026-10-01" },
      { id: 2, at: "2026-10-01" },
      { id: 3, at: "2026-09-30" },
      { id: 4, at: "2026-09-28" },
      { id: 5, at: "2026-09-28" },
    ];
    expect(groupByDay(items, (i) => i.at)).toEqual([
      { day: "2026-10-01", items: [items[0], items[1]] },
      { day: "2026-09-30", items: [items[2]] },
      { day: "2026-09-28", items: [items[3], items[4]] },
    ]);
    expect(groupByDay([], (i: { at: string }) => i.at)).toEqual([]);
  });
});
