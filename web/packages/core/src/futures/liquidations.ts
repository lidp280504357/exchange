import type { components } from "../api/gen/market";

// A contract's liquidation stream: the REST list of the last day merged
// with the pushes of liquidations:{symbol}, newest first. The reference
// market streams the latest liquidation of a contract within a second,
// so the same order can come twice (in the list and pushed after it).

type Liquidation = components["schemas"]["Liquidation"];

/** How many liquidations a stream keeps (the REST list's largest page). */
export const LIQUIDATIONS_KEPT = 100;

/** liquidationKey tells one liquidation from another: no two orders share time, side, price and quantity. */
export function liquidationKey(l: Pick<Liquidation, "traded_at" | "position_side" | "price" | "quantity">): string {
  return `${Date.parse(l.traded_at)}|${l.position_side}|${l.price}|${l.quantity}`;
}

/**
 * mergeLiquidations adds incoming liquidations to a list: each once,
 * newest first (by traded_at; equal times keep the list's order), at most
 * `keep`. The list it returns is the one given when nothing is new, so a
 * repeated push changes no state.
 */
export function mergeLiquidations(list: readonly Liquidation[], incoming: readonly Liquidation[], keep = LIQUIDATIONS_KEPT): Liquidation[] {
  const seen = new Set(list.map(liquidationKey));
  const fresh = incoming.filter((l) => {
    const k = liquidationKey(l);
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  if (fresh.length === 0 && list.length <= keep) return list as Liquidation[];
  const at = (l: Liquidation) => Date.parse(l.traded_at) || 0;
  return [...fresh, ...list]
    .map((l, i) => ({ l, i }))
    .sort((a, b) => at(b.l) - at(a.l) || a.i - b.i)
    .slice(0, keep)
    .map((x) => x.l);
}
