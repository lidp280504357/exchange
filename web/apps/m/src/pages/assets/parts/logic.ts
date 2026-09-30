import { dec } from "@exchange/core";
import { isSmall, type AssetRow } from "@exchange/core/assets/valuation";
import type { StepState } from "@exchange/core/wallet/timeline";

// Pure logic of the mobile assets pages (the data and its arithmetic come
// from @exchange/core; this is only how the phone pages cut and group it).

/**
 * filterAssets keeps the asset rows matching a search (code or name, any
 * case) and, with hideSmall, drops the small ones (valued under 1 USDT, or
 * empty; an unpriced balance is never small).
 */
export function filterAssets(rows: readonly AssetRow[], opts: { query: string; hideSmall: boolean; name: (asset: string) => string }): AssetRow[] {
  const q = opts.query.trim().toLowerCase();
  return rows.filter(
    (r) => (!opts.hideSmall || !isSmall(r)) && (!q || r.asset.toLowerCase().includes(q) || opts.name(r.asset).toLowerCase().includes(q)),
  );
}

/** matchAddress tells whether an address book entry matches a search: its label or its address, any case. */
export function matchAddress(entry: { label: string; address: string }, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return entry.label.toLowerCase().includes(q) || entry.address.toLowerCase().includes(q);
}

/**
 * currentStep is where a record's timeline stands: the step in progress,
 * the failed step, or the number of steps once everything is done.
 */
export function currentStep(steps: readonly { state: StepState }[]): number {
  const i = steps.findIndex((s) => s.state === "current" || s.state === "error");
  return i < 0 ? steps.length : i;
}

export type LedgerFilters = { asset: string; type: string; range: string };

/** filterCount is how many history filters differ from "everything". */
export function filterCount(f: LedgerFilters): number {
  return (f.asset ? 1 : 0) + (f.type ? 1 : 0) + (f.range !== "all" ? 1 : 0);
}

/**
 * ledgerTotals adds up what flowed in and out of the available balances
 * (frozen lines only move funds between the two sides of one balance).
 */
export function ledgerTotals(entries: readonly { amount: string; balance_kind: string }[]): { inflow: string; outflow: string } {
  let inflow = "0";
  let outflow = "0";
  for (const e of entries) {
    if (e.balance_kind !== "AVAILABLE" || !dec.isDecimal(e.amount)) continue;
    if (dec.sign(e.amount) > 0) inflow = dec.add(inflow, e.amount);
    else outflow = dec.add(outflow, e.amount);
  }
  return { inflow, outflow };
}

export type DayGroup<T> = { day: string; items: T[] };

/**
 * groupByDay splits a newest-first list into runs of the same calendar day
 * (dayOf gives the day in the user's time zone), keeping the order.
 */
export function groupByDay<T>(items: readonly T[], dayOf: (item: T) => string): DayGroup<T>[] {
  const out: DayGroup<T>[] = [];
  for (const item of items) {
    const day = dayOf(item);
    const last = out[out.length - 1];
    if (last && last.day === day) last.items.push(item);
    else out.push({ day, items: [item] });
  }
  return out;
}
