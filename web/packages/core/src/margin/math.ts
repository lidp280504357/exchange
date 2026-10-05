import * as dec from "../format/decimal";
import type { components } from "../api/gen/margin";

// Margin accounts (margin design 2026-10-06 §2): what the pages show of an
// account's margin level, debts and what may be repaid. Amounts stay
// decimal strings (ADR-0008).

export type MarginAccount = components["schemas"]["MarginAccount"];
export type MarginBalance = components["schemas"]["MarginBalance"];
export type MarginAsset = components["schemas"]["MarginAsset"];
export type MarginPair = components["schemas"]["MarginPair"];
export type MarginTerms = components["schemas"]["MarginTerms"];
/** A message of the private channel "margin". */
export type MarginPush = components["schemas"]["MarginPush"];
export type MarginLoan = components["schemas"]["MarginLoan"];
export type MarginInterest = components["schemas"]["MarginInterest"];
export type MarginTransfer = components["schemas"]["MarginTransfer"];
export type MarginAccountType = components["schemas"]["MarginAccountType"];

/** Where an account stands: no debts, well above the warning level, near it, under it. */
export type LevelZone = "none" | "safe" | "caution" | "danger";

/** The margin level shown for an account without debts, and the most any level shows. */
export const LEVEL_CEILING = "999";

/**
 * levelZone places a margin level against the account's thresholds:
 * under the warning level is danger (liquidation follows at the
 * liquidation level), up to twice the gap between the two levels above
 * the warning level is caution, higher is safe. null (no debts) is none.
 */
export function levelZone(level: string | null | undefined, warn: string, liquidation: string): LevelZone {
  if (level === null || level === undefined || !dec.isDecimal(level)) return "none";
  if (dec.lt(level, warn)) return "danger";
  const caution = dec.add(warn, dec.mul(dec.max(dec.sub(warn, liquidation), "0"), "2"));
  return dec.lt(level, caution) ? "caution" : "safe";
}

/** levelText is the margin level as the pages show it: two decimals, 999 without debts or beyond. */
export function levelText(level: string | null | undefined): string {
  if (level === null || level === undefined || !dec.isDecimal(level) || dec.gte(level, LEVEL_CEILING)) return LEVEL_CEILING;
  return dec.round(level, 2, "down");
}

/**
 * gaugeShare is where a margin level sits on a gauge from the liquidation
 * level (0) to twice the warning level (1), clamped; no debts is full.
 */
export function gaugeShare(level: string | null | undefined, warn: string, liquidation: string): number {
  if (level === null || level === undefined || !dec.isDecimal(level)) return 1;
  const top = dec.mul(warn, "2");
  const span = dec.sub(top, liquidation);
  if (dec.sign(span) <= 0) return dec.gte(level, warn) ? 1 : 0;
  const share = dec.toNumber(dec.div(dec.sub(level, liquidation), span, 4));
  return Math.min(1, Math.max(0, share));
}

/** owed is what an asset's debt comes to: principal and interest. */
export function owed(b: Pick<MarginBalance, "borrowed" | "interest">): string {
  return dec.normalize(dec.add(b.borrowed, b.interest));
}

/** hasDebt reports whether the asset is owed at all. */
export function hasDebt(b: Pick<MarginBalance, "borrowed" | "interest">): boolean {
  return dec.sign(owed(b)) > 0;
}

/** repayMax is what may be repaid of an asset now: its debt, as far as the free balance goes. */
export function repayMax(b: Pick<MarginBalance, "free" | "borrowed" | "interest">): string {
  const max = dec.min(b.free, owed(b));
  return dec.sign(max) > 0 ? dec.normalize(max) : "0";
}

/** accountId names an account: MARGIN_CROSS, or MARGIN_ISOLATED:<symbol>. */
export function accountId(a: { account: MarginAccountType; symbol?: string | null }): string {
  return a.account === "MARGIN_ISOLATED" ? `MARGIN_ISOLATED:${a.symbol ?? ""}` : a.account;
}

/** balanceOf is the account's row of an asset, zero when it has none. */
export function balanceOf(a: Pick<MarginAccount, "balances"> | undefined, asset: string): MarginBalance {
  return a?.balances.find((b) => b.asset === asset) ?? { asset, free: "0", locked: "0", borrowed: "0", interest: "0", net: "0" };
}

/** isEmpty reports whether an account holds and owes nothing. */
export function isEmpty(a: Pick<MarginAccount, "balances">): boolean {
  return a.balances.every((b) => dec.isZero(b.free) && dec.isZero(b.locked) && !hasDebt(b));
}
