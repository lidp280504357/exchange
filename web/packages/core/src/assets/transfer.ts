import * as dec from "../format/decimal";

// Transfers between the SPOT and FUTURES accounts (POST /v1/account/transfers,
// design §6.2 transfer). They settle at once, free. What may leave FUTURES
// is also bounded by the cross positions' unrealized loss: the ledger
// allows min(available, available + that result), which the derivatives
// account reports as `transferable`.

export type AccountType = "SPOT" | "FUTURES";

/** otherAccount is the account on the other side of a transfer. */
export function otherAccount(a: AccountType): AccountType {
  return a === "SPOT" ? "FUTURES" : "SPOT";
}

/**
 * transferMax is what may move: the available balance, and when given
 * (out of FUTURES) no more than `transferable`, never below 0.
 */
export function transferMax(available: string, transferable?: string | null): string {
  let max = dec.isDecimal(available) ? available : "0";
  if (transferable && dec.isDecimal(transferable)) max = dec.min(max, transferable);
  return dec.sign(max) > 0 ? dec.normalize(max) : "0";
}

export type TransferIssue = "format" | "zero" | "precision" | "insufficient";

/** checkTransfer checks a typed amount against the asset's decimals and the maximum ("" is no issue yet). */
export function checkTransfer(amount: string, max: string, decimals: number): TransferIssue | null {
  const a = amount.trim();
  if (a === "") return null;
  const c = dec.checkAmount(a, decimals);
  if (c !== "ok") return c;
  return dec.gt(a, max) ? "insufficient" : null;
}

type BalanceRow = { account_type: string; asset: string; available: string };

/**
 * transferCoins lists the coins a transfer may move (review FE, B128): the
 * futures accounts' settlement assets (USDT and the coin-margined
 * contracts' coins) and what FUTURES still holds of another; those with a
 * balance on the "from" side first, in codes' order.
 */
export function transferCoins(codes: readonly string[], settles: readonly string[], balances: readonly BalanceRow[] | undefined, from: AccountType): string[] {
  const positive = (b: BalanceRow) => dec.isDecimal(b.available) && dec.sign(b.available) > 0;
  const inFutures = new Set((balances ?? []).filter((b) => b.account_type === "FUTURES" && positive(b)).map((b) => b.asset));
  const held = new Set((balances ?? []).filter((b) => b.account_type === from && positive(b)).map((b) => b.asset));
  const movable = codes.filter((c) => settles.includes(c) || inFutures.has(c));
  return [...movable.filter((c) => held.has(c)), ...movable.filter((c) => !held.has(c))];
}
