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
