import type { components } from "../api/gen/market";

// A contract's liquidation stream: the REST list of the last day merged
// with the pushes of liquidations:{symbol}, newest first. The same order
// can come twice (in the list and pushed after it): it is one by its
// primary key.

type Liquidation = components["schemas"]["Liquidation"];

/** How many liquidations a stream keeps (the REST list's largest page). */
export const LIQUIDATIONS_KEPT = 100;

/**
 * liquidationKey is a liquidation's identity, the service's primary key
 * (market 00008: symbol, traded_at, position_side): the reference market
 * sends a contract's latest liquidation per side at most once a millisecond.
 */
export function liquidationKey(l: Pick<Liquidation, "symbol" | "traded_at" | "position_side">): string {
  return `${l.symbol}|${Date.parse(l.traded_at)}|${l.position_side}`;
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
