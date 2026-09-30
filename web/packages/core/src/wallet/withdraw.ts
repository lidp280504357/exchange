import * as dec from "../format/decimal";

// The withdrawal amount (design §6.2 withdraw step 4; wallet.yaml). The
// amount is what arrives at the address; the network fee is frozen and
// charged on top, so what leaves the balance is amount + fee. A withdrawal
// to another user's deposit address completes inside the ledger without a
// fee. The minimum applies to the amount. Everything is exact decimal
// arithmetic, and the maximum is cut down to the asset's decimals: nothing
// is ever rounded up against the user.

export type WithdrawIssue = "format" | "zero" | "precision" | "belowMin" | "insufficient";

export type WithdrawInput = {
  /** What the user typed ("" while empty). */
  amount: string;
  /** The spot balance available. */
  available: string;
  /** The network's withdrawal fee. */
  fee: string;
  /** The network's minimum withdrawal. */
  min: string;
  /** The asset's decimals. */
  decimals: number;
  /** Another user's deposit address: no fee. */
  internal?: boolean;
};

export type WithdrawQuote = {
  /** The fee charged ("0" for an internal transfer). */
  fee: string;
  /** What arrives at the address; null until the amount is valid. */
  received: string | null;
  /** What leaves the balance (amount + fee); null until the amount is valid. */
  total: string | null;
  /** The most that can be withdrawn: available − fee, cut to the decimals, never below 0. */
  max: string;
  /** Whether the balance covers the minimum and the fee at all. */
  enough: boolean;
  /** What is wrong with the amount, if anything (nothing while empty). */
  issue: WithdrawIssue | null;
};

/** maxWithdrawable is available − fee, cut down to the asset's decimals, or "0". */
export function maxWithdrawable(available: string, fee: string, decimals: number): string {
  const rest = dec.sub(available, fee);
  if (dec.sign(rest) <= 0) return "0";
  return dec.normalize(dec.round(rest, decimals, "down"));
}

/** withdrawQuote works out the fee, what arrives, what is charged and what is wrong. */
export function withdrawQuote(input: WithdrawInput): WithdrawQuote {
  const fee = input.internal ? "0" : dec.normalize(input.fee);
  const max = maxWithdrawable(input.available, fee, input.decimals);
  const enough = dec.sign(max) > 0 && dec.gte(max, input.min);
  const amount = input.amount.trim();
  const base = { fee, max, enough };
  if (amount === "") return { ...base, received: null, total: null, issue: null };
  const check = dec.checkAmount(amount, input.decimals);
  if (check !== "ok") return { ...base, received: null, total: null, issue: check };
  const received = dec.normalize(amount);
  const total = dec.add(received, fee);
  let issue: WithdrawIssue | null = null;
  if (dec.lt(received, input.min)) issue = "belowMin";
  else if (dec.gt(total, input.available)) issue = "insufficient";
  return { ...base, received, total, issue };
}
