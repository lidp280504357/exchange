import { dec } from "@exchange/core";

// The rules of HOUSE's caps the console applies before admin-service does
// (A69, user 2026-10-07 06:0x): each cap's range, and which way a change
// moves it.

/** HOUSE's caps by name, in the order the console shows them. */
export const HOUSE_CAPS = ["level", "symbol", "total", "contract", "safety", "contract_leverage"] as const;
export type CapName = (typeof HOUSE_CAPS)[number];

/**
 * inRange reports whether a cap's value is within its range, as
 * admin-service checks it: the level cap zero or more (zero: not capped),
 * the other USDT caps above zero, all at most 1e15; the leverage 1 to 125.
 */
export function inRange(name: CapName, raw: string): boolean {
  const v = raw.trim();
  if (!dec.isDecimal(v)) return false;
  if (name === "contract_leverage") return dec.gte(v, "1") && dec.lte(v, "125");
  if (dec.gt(v, "1000000000000000")) return false;
  return name === "level" ? !dec.lt(v, "0") : dec.gt(v, "0");
}

/**
 * stepOK reports whether a change moves a cap at most ten times up or down
 * (market-maker's HOUSE_CAPS_STEP); a level cap going to or from zero (no
 * cap) is not a step, nor is a value that is not a decimal (inRange says).
 */
export function stepOK(before: string, after: string): boolean {
  const a = after.trim();
  if (!dec.isDecimal(before) || !dec.isDecimal(a) || !dec.gt(before, "0") || !dec.gt(a, "0")) return true;
  return dec.lte(a, dec.mul(before, "10")) && dec.gte(dec.mul(a, "10"), before);
}

/**
 * stepRange is how far one change can move a cap from before (stepOK)
 * within its range: a tenth to ten times, the leverage within 1 to 125 (10
 * to 125 takes 10 → 100 → 125); null where no step applies (before not
 * above zero: a level cap of zero is no cap).
 */
export function stepRange(name: CapName, before: string): { min: string; max: string } | null {
  if (!dec.isDecimal(before) || !dec.gt(before, "0")) return null;
  const min = dec.div(before, "10", dec.decimalsOf(before) + 1);
  const max = dec.mul(before, "10");
  if (name === "contract_leverage") return { min: dec.max(min, "1"), max: dec.min(max, "125") };
  return { min, max: dec.min(max, "1000000000000000") };
}

/** Holding is HOUSE's holding of an asset in USDT; below zero for an internal asset it sold. */
export type Holding = { asset: string; value: string };

/**
 * holdings reads HOUSE's inventory as market-maker holds it against the
 * caps (its SpotRooms): every asset but USDT that has a price, largest
 * absolute value first, and their absolute values together (the total
 * cap's measure).
 */
export function holdings(assets: readonly { asset: string; value_usdt?: string | null }[]): { list: Holding[]; total: string } {
  const list = assets
    .filter((a) => a.asset !== "USDT" && a.value_usdt != null && dec.isDecimal(a.value_usdt) && !dec.isZero(a.value_usdt))
    .map((a) => ({ asset: a.asset, value: a.value_usdt as string }))
    .sort((a, b) => dec.cmp(dec.abs(b.value), dec.abs(a.value)) || a.asset.localeCompare(b.asset));
  return { list, total: list.reduce((sum, h) => dec.add(sum, dec.abs(h.value)), "0") };
}

/**
 * over lists the holdings a per-asset cap would leave at or beyond it,
 * either way: HOUSE stops buying those (selling, the ones sold below zero)
 * on every pair, as market-maker's room is zero there already (SpotRooms;
 * A80 ①). None for a value that is not a cap (inRange).
 */
export function over(list: readonly Holding[], cap: string): Holding[] {
  const v = cap.trim();
  if (!inRange("symbol", v)) return [];
  return list.filter((h) => dec.gte(dec.abs(h.value), v));
}

/** totalOver reports whether a total cap would be at or below what HOUSE holds together (A80 ①). */
export function totalOver(total: string, cap: string): boolean {
  const v = cap.trim();
  return inRange("total", v) && dec.gte(total, v);
}

/**
 * direction says whether a change lowers or raises a cap - a level cap of
 * zero is no cap, above any other - or null when it stays (or a value is
 * not a decimal).
 */
export function direction(name: CapName, before: string, after: string): "lower" | "raise" | null {
  if (!dec.isDecimal(before) || !dec.isDecimal(after)) return null;
  if (name === "level") {
    if (dec.isZero(after)) return dec.isZero(before) ? null : "raise";
    if (dec.isZero(before)) return "lower";
  }
  const c = dec.cmp(after, before);
  return c < 0 ? "lower" : c > 0 ? "raise" : null;
}
