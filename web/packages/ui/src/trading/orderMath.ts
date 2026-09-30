import { dec } from "@exchange/core";

// The order form's arithmetic (design §6.2), kept out of the component so
// it can be tested alone. Every value is a decimal string; nothing is ever
// rounded in the user's disfavour: quantities go down to the lot size, a
// buy's total never exceeds the balance, fees round up.

export type OrderSide = "BUY" | "SELL";
export type OrderType = "limit" | "market";

/** A spot pair's trading rules (GET /v1/market/pairs), as decimal strings. */
export type PairRules = {
  base: string;
  quote: string;
  tickSize: string;
  lotSize: string;
  minQuantity: string;
  maxQuantity: string;
  minNotional: string;
  makerFeeRate: string;
  takerFeeRate: string;
};

export type Balances = { base: string; quote: string };

/** usable reports whether v is a decimal string above zero. */
export function usable(v: string | null | undefined): v is string {
  return Boolean(v) && dec.isDecimal(v as string) && dec.sign(v as string) > 0;
}

/** snapToStep moves a value down to a multiple of a step (tick or lot); "" stays "". */
export function snapToStep(v: string, step: string): string {
  if (!v || !dec.isDecimal(v) || !usable(step)) return v;
  return dec.normalize(dec.quantize(v, step, "down"));
}

/** totalOf is price × quantity, exact (tick × lot precision fits the quote asset). */
export function totalOf(price: string, quantity: string): string {
  if (!usable(price) || !usable(quantity)) return "";
  return dec.mul(price, quantity);
}

/** quantityForTotal is total ÷ price, down to the lot size. */
export function quantityForTotal(total: string, price: string, lotSize: string): string {
  if (!usable(total) || !usable(price) || !usable(lotSize)) return "";
  const q = dec.div(total, price, dec.decimalsOf(lotSize), "down");
  return dec.normalize(dec.quantize(q, lotSize, "down"));
}

/**
 * maxQuantity is the most one can order: a buy spends the quote balance at
 * the price (the fee is taken from the bought base), a sell the base balance.
 */
export function maxQuantity(side: OrderSide, price: string, available: Balances | null | undefined, lotSize: string): string {
  if (!available || !usable(lotSize)) return "";
  if (side === "SELL") return usable(available.base) ? dec.normalize(dec.quantize(available.base, lotSize, "down")) : "0";
  return quantityForTotal(available.quote, price, lotSize) || "0";
}

/** percentOfQuantity is pct % of the maximum quantity, down to the lot. */
export function percentOfQuantity(max: string, pct: number, lotSize: string): string {
  if (!usable(max) || pct <= 0) return "";
  const whole = String(Math.min(100, Math.round(pct)));
  return dec.normalize(dec.quantize(dec.div(dec.mul(max, whole), "100", dec.decimalsOf(lotSize), "down"), lotSize, "down"));
}

/** percentOfAmount is pct % of a balance, down to `decimals` (a market buy's total). */
export function percentOfAmount(balance: string, pct: number, decimals: number): string {
  if (!usable(balance) || pct <= 0) return "";
  const whole = String(Math.min(100, Math.round(pct)));
  return dec.normalize(dec.div(dec.mul(balance, whole), "100", decimals, "down"));
}

export type FeeEstimate = { amount: string; asset: string; rate: string };

/**
 * estimateFee: buyers pay in the base they receive, sellers in the quote
 * (requirements §11.3); limit orders use the maker rate, market orders the
 * taker rate. Rounded up, like the ledger does.
 */
export function estimateFee(args: {
  side: OrderSide;
  type: OrderType;
  rules: PairRules;
  price: string;
  quantity: string;
  quoteAmount: string;
  lastPrice?: string | null;
  baseDecimals?: number;
  quoteDecimals?: number;
}): FeeEstimate | null {
  const { side, type, rules, price, quantity, quoteAmount, lastPrice, baseDecimals = 8, quoteDecimals = 8 } = args;
  const rate = type === "market" ? rules.takerFeeRate : rules.makerFeeRate;
  if (!dec.isDecimal(rate)) return null;
  const at = type === "market" ? lastPrice : price;
  if (side === "BUY") {
    let qty = quantity;
    if (type === "market") qty = usable(quoteAmount) && usable(at) ? dec.div(quoteAmount, at, baseDecimals + 2, "down") : "";
    if (!usable(qty)) return null;
    return { amount: dec.round(dec.mul(qty, rate), baseDecimals, "up"), asset: rules.base, rate };
  }
  if (!usable(quantity) || !usable(at)) return null;
  return { amount: dec.round(dec.mul(dec.mul(quantity, at), rate), quoteDecimals, "up"), asset: rules.quote, rate };
}

/** An error message key under ui.order with its values. */
export type OrderFieldError = { key: "priceRequired" | "quantityRequired" | "totalRequired" | "minQuantity" | "maxQuantity" | "minNotional" | "insufficient"; values?: Record<string, string> };

export type OrderErrors = { price?: OrderFieldError; quantity?: OrderFieldError; total?: OrderFieldError };

/**
 * validateOrder checks a draft against the pair's rules and the balances
 * (when signed in): minimum and maximum quantity, minimum notional, and
 * what the balance covers. Market buys are sized by total, market sells by
 * quantity; the notional of a market sell is estimated at the last price.
 */
export function validateOrder(args: {
  side: OrderSide;
  type: OrderType;
  rules: PairRules;
  price: string;
  quantity: string;
  total: string;
  available?: Balances | null;
  lastPrice?: string | null;
}): OrderErrors {
  const { side, type, rules, price, quantity, total, available, lastPrice } = args;
  const errors: OrderErrors = {};
  const byTotal = type === "market" && side === "BUY";

  if (byTotal) {
    if (!usable(total)) errors.total = { key: "totalRequired" };
    else if (usable(rules.minNotional) && dec.lt(total, rules.minNotional)) errors.total = { key: "minNotional", values: { value: rules.minNotional, unit: rules.quote } };
    else if (available && dec.gt(total, available.quote)) errors.total = { key: "insufficient" };
    return errors;
  }

  if (type === "limit" && !usable(price)) errors.price = { key: "priceRequired" };
  if (!usable(quantity)) {
    errors.quantity = { key: "quantityRequired" };
    return errors;
  }
  if (usable(rules.minQuantity) && dec.lt(quantity, rules.minQuantity)) {
    errors.quantity = { key: "minQuantity", values: { value: rules.minQuantity, unit: rules.base } };
  } else if (usable(rules.maxQuantity) && dec.gt(quantity, rules.maxQuantity)) {
    errors.quantity = { key: "maxQuantity", values: { value: rules.maxQuantity, unit: rules.base } };
  } else if (side === "SELL" && available && dec.gt(quantity, available.base)) {
    errors.quantity = { key: "insufficient" };
  }

  const at = type === "market" ? lastPrice : price;
  const notional = usable(at) ? dec.mul(at, quantity) : "";
  if (notional && usable(rules.minNotional) && dec.lt(notional, rules.minNotional)) {
    const e: OrderFieldError = { key: "minNotional", values: { value: rules.minNotional, unit: rules.quote } };
    if (type === "market") errors.quantity ??= e;
    else errors.total ??= e;
  }
  if (side === "BUY" && available && notional && dec.gt(notional, available.quote)) errors.total ??= { key: "insufficient" };
  return errors;
}
