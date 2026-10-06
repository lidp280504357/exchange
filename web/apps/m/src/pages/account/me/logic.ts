import { DEFAULT_SYMBOL, dec, routes } from "@exchange/core";
import type { SecurityFactors } from "@exchange/core/user/security";

// Pure rules of the "me" tab (design §7.3).

/** The protections the security card counts, in the order it suggests them (the security centre's). */
export const GUARDS = ["totp", "phone", "email", "antiPhishing"] as const satisfies readonly (keyof SecurityFactors)[];

export type GuardProgress = {
  done: number;
  max: number;
  /** The first protection still off; null when all are on. */
  next: keyof SecurityFactors | null;
};

/** guardProgress counts the protections that are on and names the first one that is not. */
export function guardProgress(f: SecurityFactors): GuardProgress {
  return { done: GUARDS.filter((k) => f[k]).length, max: GUARDS.length, next: GUARDS.find((k) => !f[k]) ?? null };
}

export type OrdersTab = "open" | "history" | "fills";

/**
 * ordersPath opens the spot terminal's orders at a tab, on the spot pair
 * traded last (the terminal lists one pair's orders), else the default.
 */
export function ordersPath(recent: readonly string[], tab: OrdersTab): string {
  const spot = recent.find((s) => !s.endsWith("-PERP")) ?? DEFAULT_SYMBOL;
  return `${routes.trade(spot)}?orders=${tab}`;
}

/** ordersTabOf reads the terminal's ?orders= (the "me" shortcuts); anything else is the open orders. */
export function ordersTabOf(raw: string | null): OrdersTab {
  return raw === "history" || raw === "fills" ? raw : "open";
}

/** An announcement this young (by its date) gets the red dot. */
export const NEW_FOR_MS = 3 * 24 * 3600_000;

/** isNew says whether an announcement dated YYYY-MM-DD is fresh enough for the red dot. */
export function isNew(date: string, now: number): boolean {
  const at = Date.parse(date);
  return Number.isFinite(at) && now >= at && now - at < NEW_FOR_MS;
}

/**
 * splitShares are the spot, futures and margin shares of their sum for the
 * split bar (each 0..1; all 0 without funds; a margin net below zero, more
 * owed than held, takes no share).
 */
export function splitShares(spot: string, futures: string, margin = "0"): { spot: number; futures: number; margin: number } {
  const s = Math.max(0, dec.toNumber(spot));
  const f = Math.max(0, dec.toNumber(futures));
  const m = Math.max(0, dec.toNumber(margin));
  const total = s + f + m;
  if (!(total > 0)) return { spot: 0, futures: 0, margin: 0 };
  return { spot: s / total, futures: f / total, margin: m / total };
}
