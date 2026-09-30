import { add, decimalsOf, div, isDecimal, mul, normalize, quantize, sign, sub } from "../format/decimal";

// The futures order form's arithmetic (requirements §11.7, design §6.2),
// on decimal strings. Opening reserves the initial margin (notional /
// leverage) and the taker fee; nothing is ever rounded in the user's
// disfavour: quantities go down to the lot, costs up to the cent.

const COST_DECIMALS = 8;

function ok(v: string | null | undefined): v is string {
  return Boolean(v) && isDecimal(v as string) && sign(v as string) > 0;
}

/** openCost is what opening reserves: notional / leverage plus the fee at the taker rate. */
export function openCost(price: string, quantity: string, leverage: number, feeRate: string): string {
  if (!ok(price) || !ok(quantity) || leverage < 1) return "0";
  const notional = mul(price, quantity);
  const margin = div(notional, String(leverage), COST_DECIMALS, "up");
  const fee = isDecimal(feeRate) && sign(feeRate) > 0 ? mul(notional, feeRate) : "0";
  return normalize(add(margin, fee));
}

/**
 * maxOpenQuantity is the most that the available margin opens at a price:
 * available / (price × (1/leverage + feeRate)), down to the lot.
 */
export function maxOpenQuantity(available: string, price: string, leverage: number, feeRate: string, lotSize: string): string {
  if (!ok(available) || !ok(price) || leverage < 1 || !ok(lotSize)) return "0";
  // perUnit = price / leverage + price × feeRate
  const perUnit = add(div(price, String(leverage), COST_DECIMALS + 4, "up"), isDecimal(feeRate) ? mul(price, feeRate) : "0");
  if (sign(perUnit) <= 0) return "0";
  const q = div(available, perUnit, decimalsOf(lotSize), "down");
  return normalize(quantize(q, lotSize, "down"));
}

/** closeableQuantity is how much of a position (signed quantity) a close order may take. */
export function closeableQuantity(positionQuantity: string | undefined): string {
  if (!positionQuantity || !isDecimal(positionQuantity)) return "0";
  return sign(positionQuantity) < 0 ? normalize(sub("0", positionQuantity)) : normalize(positionQuantity);
}

/** roe is the unrealized result over the margin, as a fraction ("0.1234"). */
export function roe(unrealizedPnl: string | null | undefined, margin: string | null | undefined): string {
  if (!unrealizedPnl || !isDecimal(unrealizedPnl) || !ok(margin)) return "0";
  return div(unrealizedPnl, margin, 6, "down");
}
