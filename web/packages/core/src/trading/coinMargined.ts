import { abs, add, div, gt, isDecimal, min, mul, normalize, quantize, sign, sub } from "../format/decimal";
import {
  checkRiskLimit, liveFigures, maxNotional, maxOpenQuantity, openCost, reservePrice, riskRoom, roe, type PricedPosition, type RiskCheck,
  type RiskTier,
} from "./futuresMath";

// The coin-margined (inverse) contracts' arithmetic (design 2026-10-06
// §2.2, ADR-0020), on decimal strings as derivatives-service computes it:
// quantities are whole contracts of contract_size USD; margin, fees and
// results are in the coin the contract settles in. A contract is worth
// size / price coins, more as the price falls.

function ok(v: string | null | undefined): v is string {
  return Boolean(v) && isDecimal(v as string) && sign(v as string) > 0;
}

/** The fields of a contract its arithmetic reads (the market API's Contract). */
export type ContractTerms = {
  quote_asset: string;
  settle_asset: string;
  contract_size: string;
  tick_size: string;
  lot_size: string;
  price_band: string;
  taker_fee_rate: string;
  risk_tiers: RiskTier[];
};

/** isInverse reports whether a contract is coin-margined: its contract_size is above zero. */
export function isInverse(c: { contract_size?: string | null } | null | undefined): boolean {
  return ok(c?.contract_size);
}

/** coinValue is what contracts are worth in the coin at a price: |contracts| × size / price, half up to decimals; "0" without a price. */
export function coinValue(contracts: string, size: string, price: string | null | undefined, decimals = 8): string {
  if (!isDecimal(contracts) || !ok(size) || !ok(price)) return "0";
  return normalize(div(mul(abs(contracts), size), price, decimals, "half"));
}

/** usdValue is the contracts' face value in USD: |contracts| × size. */
export function usdValue(contracts: string, size: string): string {
  if (!isDecimal(contracts) || !isDecimal(size)) return "0";
  return normalize(mul(abs(contracts), size));
}

/**
 * ContractUnit is what a coin-margined order's amount is typed in
 * (review FE, B130, as Binance offers): whole contracts, the coin they
 * settle in, or USD. Orders always carry whole contracts.
 */
export type ContractUnit = "CONT" | "COIN" | "USD";

/**
 * toContracts turns an amount typed in a unit into whole contracts of
 * size USD at price: contracts as they are, a coin amount × price ÷ size,
 * a USD amount ÷ size, down to whole contracts and at least one for an
 * amount above zero; "" for no amount (or a coin amount without a price).
 */
export function toContracts(amount: string, unit: ContractUnit, size: string, price: string | null | undefined): string {
  if (!isDecimal(amount) || sign(amount) <= 0 || !ok(size)) return "";
  let n: string;
  if (unit === "CONT") n = div(amount, "1", 0, "down");
  else if (unit === "USD") n = div(amount, size, 0, "down");
  else if (ok(price)) n = div(mul(amount, price), size, 0, "down");
  else return "";
  return sign(n) > 0 ? normalize(n) : "1";
}

/**
 * fromContracts shows whole contracts in a unit: themselves, the coin
 * they are worth at price (up to decimals, so that toContracts turns it
 * back into as many contracts, review FN, B136), or their USD face value;
 * "" for none (or the coin without a price).
 */
export function fromContracts(contracts: string, unit: ContractUnit, size: string, price: string | null | undefined, decimals = 8): string {
  if (!isDecimal(contracts) || sign(contracts) <= 0) return "";
  if (unit === "CONT") return normalize(contracts);
  if (unit === "USD") return usdValue(contracts, size);
  return ok(price) ? normalize(div(mul(abs(contracts), size), price, decimals, "up")) : "";
}

/**
 * perContract is what opening one contract reserves at a price: its coin
 * value over the leverage and the taker fee on it, each rounded up to the
 * coin's decimals (derivatives-service's margin and fee per lot).
 */
export function perContract(price: string, size: string, leverage: number, feeRate: string, decimals: number): string {
  if (!ok(price) || !ok(size) || leverage < 1) return "0";
  const margin = div(size, mul(price, String(leverage)), decimals, "up");
  const fee = isDecimal(feeRate) && sign(feeRate) > 0 ? div(mul(size, feeRate), price, decimals, "up") : "0";
  return normalize(add(margin, fee));
}

/**
 * inverseReservePrice is the price an opening order reserves at: a buy at
 * the lower of its price and the mark (a market buy at the mark), a sell
 * at its price (a market sell at its protection price, the band below the
 * mark, up to the tick); "" without the prices it needs.
 */
export function inverseReservePrice(side: "BUY" | "SELL", type: "limit" | "market", price: string, mark: string, band: string, tick: string): string {
  if (side === "BUY") {
    if (type === "market") return ok(mark) ? mark : "";
    if (!ok(price)) return "";
    return ok(mark) ? min(price, mark) : price;
  }
  if (type === "limit") return ok(price) ? price : "";
  if (!ok(mark) || !isDecimal(band) || !ok(tick)) return "";
  const at = quantize(mul(mark, sub("1", band)), tick, "up");
  return sign(at) > 0 ? normalize(at) : "";
}

/**
 * inverseRiskRoom is how many more contracts a side may open at a
 * leverage: the leverage's cap (in the coin) at the mark, less the
 * contracts the side holds and has on order; "0" when nothing is left.
 */
export function inverseRiskRoom(tiers: readonly RiskTier[], leverage: number, mark: string, exposure: string, size: string): string {
  const cap = maxNotional(tiers, leverage);
  if (!ok(mark) || !ok(cap) || !ok(size)) return "0";
  const room = sub(div(mul(cap, mark), size, 0, "down"), isDecimal(exposure) ? exposure : "0");
  return sign(room) > 0 ? normalize(room) : "0";
}

/** inverseCheckRisk tells whether an opening order keeps its side within the leverage's cap, both in the coin at the mark. */
export function inverseCheckRisk(
  tiers: readonly RiskTier[], leverage: number, mark: string, exposure: string, contracts: string, size: string, decimals = 8,
): RiskCheck {
  const cap = maxNotional(tiers, leverage);
  const notional = ok(mark) && isDecimal(contracts) && isDecimal(exposure) ? coinValue(add(exposure, contracts), size, mark, decimals) : "0";
  return { ok: !gt(notional, cap), cap, notional };
}

/**
 * inverseUnrealizedPnl is a position's result in the coin at a mark: its
 * signed contracts × size × (1 / entry − 1 / mark), so a long gains as the
 * price rises; "0" without a mark.
 */
export function inverseUnrealizedPnl(contracts: string, entryPrice: string, mark: string | null | undefined, size: string, decimals = 8): string {
  if (!isDecimal(contracts) || !ok(entryPrice) || !ok(mark) || !ok(size)) return "0";
  return normalize(div(mul(mul(contracts, size), sub(mark, entryPrice)), mul(entryPrice, mark), decimals, "half"));
}

/**
 * ContractMath is a contract's order arithmetic, linear or coin-margined
 * alike: quantities in the contract's unit (the base asset, or whole
 * contracts), amounts in its settlement asset.
 */
export type ContractMath = {
  inverse: boolean;
  /** The asset margin, fees and results are in. */
  settle: string;
  /** The unit min_notional is in: the quote asset, or USD (the contracts' face value). */
  notionalUnit: string;
  /** The decimals amounts in the settlement asset show with: USDT's 2, a coin's own. */
  amountDecimals: number;
  /** The price an opening order reserves its margin and fee at ("" without it). */
  reservePrice(side: "BUY" | "SELL", type: "limit" | "market", price: string, mark: string): string;
  /** What opening reserves: margin and taker fee. */
  openCost(price: string, quantity: string, leverage: number): string;
  /** The most the available margin opens at a price. */
  maxOpen(available: string, price: string, leverage: number): string;
  /** How much more a side may open at a leverage (risk limit). */
  riskRoom(leverage: number, mark: string, exposure: string): string;
  /** An opening order against its leverage's cap, in the settlement asset. */
  checkRisk(leverage: number, mark: string, exposure: string, quantity: string): RiskCheck;
  /** What min_notional is compared with: price × quantity, or the contracts' face value. */
  orderNotional(quantity: string, price: string): string;
  /** What a quantity is worth at a price in the settlement asset. */
  worth(quantity: string, price: string | null | undefined): string;
  /** A position's mark, result and return on margin at the latest mark. */
  live(p: PricedPosition, mark: string | null | undefined): { markPrice: string; unrealizedPnl: string; roe: string };
};

/**
 * contractMath is c's arithmetic; decimals are its settlement asset's (the
 * coin's rounding of a coin-margined contract's margin and fees).
 */
export function contractMath(c: ContractTerms, decimals = 8): ContractMath {
  if (!isInverse(c)) {
    return {
      inverse: false,
      settle: c.settle_asset || c.quote_asset,
      notionalUnit: c.quote_asset,
      amountDecimals: 2,
      reservePrice: (side, type, price, mark) => reservePrice(side, type, price, mark, c.price_band, c.tick_size),
      openCost: (price, quantity, leverage) => openCost(price, quantity, leverage, c.taker_fee_rate),
      maxOpen: (available, price, leverage) => maxOpenQuantity(available, price, leverage, c.taker_fee_rate, c.lot_size),
      riskRoom: (leverage, mark, exposure) => riskRoom(c.risk_tiers, leverage, mark, exposure, c.lot_size),
      checkRisk: (leverage, mark, exposure, quantity) => checkRiskLimit(c.risk_tiers, leverage, mark, exposure, quantity),
      orderNotional: (quantity, price) => (isDecimal(quantity) && isDecimal(price) ? normalize(mul(price, quantity)) : "0"),
      worth: (quantity, price) => (isDecimal(quantity) && ok(price) ? normalize(mul(abs(quantity), price)) : "0"),
      live: (p, mark) => liveFigures(p, mark),
    };
  }
  const size = c.contract_size;
  return {
    inverse: true,
    settle: c.settle_asset,
    notionalUnit: "USD",
    amountDecimals: decimals,
    reservePrice: (side, type, price, mark) => inverseReservePrice(side, type, price, mark, c.price_band, c.tick_size),
    openCost: (price, quantity, leverage) =>
      ok(quantity) && ok(price) ? normalize(mul(quantity, perContract(price, size, leverage, c.taker_fee_rate, decimals))) : "0",
    maxOpen: (available, price, leverage) => {
      const per = perContract(price, size, leverage, c.taker_fee_rate, decimals);
      return ok(available) && ok(per) ? normalize(div(available, per, 0, "down")) : "0";
    },
    riskRoom: (leverage, mark, exposure) => inverseRiskRoom(c.risk_tiers, leverage, mark, exposure, size),
    checkRisk: (leverage, mark, exposure, quantity) => inverseCheckRisk(c.risk_tiers, leverage, mark, exposure, quantity, size, decimals),
    orderNotional: (quantity) => usdValue(quantity, size),
    worth: (quantity, price) => coinValue(quantity, size, price, decimals),
    live: (p, mark) => {
      const m = ok(mark) ? mark : p.mark_price;
      if (!ok(m)) return { markPrice: "0", unrealizedPnl: p.unrealized_pnl ?? "0", roe: roe(p.unrealized_pnl, p.margin) };
      const pnl = inverseUnrealizedPnl(p.quantity, p.entry_price, m, size, decimals);
      return { markPrice: m, unrealizedPnl: pnl, roe: roe(pnl, p.margin) };
    },
  };
}
