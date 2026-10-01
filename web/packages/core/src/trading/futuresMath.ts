import { add, decimalsOf, div, gt, isDecimal, max, min, mul, normalize, quantize, sign, sub } from "../format/decimal";

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

/**
 * reservePrice is the price an opening order reserves its margin and fee
 * at, as derivatives-service does: a buy at its price, a market buy at its
 * protection price (the mark plus the price band, down to the tick); a
 * sell at the higher of its price and the mark, since it fills at its
 * price or above (a market sell at the mark). "" without the prices it
 * needs.
 */
export function reservePrice(side: "BUY" | "SELL", type: "limit" | "market", price: string, mark: string, band: string, tick: string): string {
  if (side === "BUY") {
    if (type === "limit") return ok(price) ? price : "";
    if (!ok(mark) || !isDecimal(band) || !ok(tick)) return "";
    return normalize(quantize(mul(mark, add("1", band)), tick, "down"));
  }
  if (type === "market") return ok(mark) ? mark : "";
  if (!ok(price)) return "";
  return ok(mark) && gt(mark, price) ? mark : price;
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

/**
 * unrealizedPnl is a position's result at a mark price: its signed
 * quantity × (mark − entry), so a short gains as the mark falls; "0"
 * without a mark.
 */
export function unrealizedPnl(quantity: string, entryPrice: string, mark: string | null | undefined): string {
  if (!isDecimal(quantity) || !isDecimal(entryPrice) || !ok(mark)) return "0";
  return normalize(mul(quantity, sub(mark, entryPrice)));
}

/** The fields of a position (as the derivatives API returns it) that its live figures read. */
export type PricedPosition = { quantity: string; entry_price: string; margin: string; mark_price: string | null; unrealized_pnl: string | null };

/**
 * liveFigures is what a position shows at the latest mark price: the mark
 * moves every second on its channel, the position only when it changes,
 * so the result and its return on margin follow the mark here; the
 * server's figures stand until a mark arrives.
 */
export function liveFigures(p: PricedPosition, mark: string | null | undefined): { markPrice: string; unrealizedPnl: string; roe: string } {
  const m = ok(mark) ? mark : p.mark_price;
  if (!ok(m)) return { markPrice: "0", unrealizedPnl: p.unrealized_pnl ?? "0", roe: roe(p.unrealized_pnl, p.margin) };
  const pnl = unrealizedPnl(p.quantity, p.entry_price, m);
  return { markPrice: m, unrealizedPnl: pnl, roe: roe(pnl, p.margin) };
}

// Risk limits (requirements §11.7): what a side may hold at a leverage,
// as the derivatives service checks it (internal/derivatives/domain:
// Contract.MaxNotional, CheckRiskLimit).

/** RiskTier is one tier of a contract's risk limit ladder. */
export type RiskTier = { max_notional: string; max_leverage: number; mmr: string };

/**
 * maxNotional is the most a side may hold at a leverage: the cap of the
 * last tier whose leverage is at least it, "0" when none allows it.
 */
export function maxNotional(tiers: readonly RiskTier[], leverage: number): string {
  let out = "0";
  for (const t of tiers) if (t.max_leverage >= leverage) out = t.max_notional;
  return out;
}

type Side = "BUY" | "SELL";
type PositionSide = "BOTH" | "LONG" | "SHORT";
/** The fields of a position and of an active order that the risk limit reads. */
export type ExposedPosition = { position_side: PositionSide; quantity: string };
export type ExposedOrder = { side: Side; position_side: PositionSide; reduce_only: boolean; quantity: string; filled_quantity: string };

/**
 * sideExposure is the base quantity that an opening order adds to: in
 * one-way mode the position when it is on the order's side and the active
 * non-reducing orders of that side; in hedge mode the position of the
 * order's side and the opening orders on it.
 */
export function sideExposure(side: Side, positionSide: PositionSide, positions: readonly ExposedPosition[], orders: readonly ExposedOrder[]): string {
  const remaining = (o: ExposedOrder) => max(sub(o.quantity, o.filled_quantity), "0");
  let out = "0";
  if (positionSide === "BOTH") {
    const pos = positions.find((p) => p.position_side === "BOTH")?.quantity ?? "0";
    out = max(side === "BUY" ? pos : sub("0", pos), "0");
    for (const o of orders) if (o.position_side === "BOTH" && !o.reduce_only && o.side === side) out = add(out, remaining(o));
  } else {
    const pos = positions.find((p) => p.position_side === positionSide)?.quantity ?? "0";
    out = sign(pos) < 0 ? sub("0", pos) : pos;
    const opening = (o: ExposedOrder) => (o.position_side === "LONG" && o.side === "BUY") || (o.position_side === "SHORT" && o.side === "SELL");
    for (const o of orders) if (o.position_side === positionSide && opening(o)) out = add(out, remaining(o));
  }
  return normalize(out);
}

/**
 * riskRoom is how much more a side may open at a leverage: the leverage's
 * cap over the mark price, less what the side already holds and has on
 * order, down to the lot; "0" when nothing is left.
 */
export function riskRoom(tiers: readonly RiskTier[], leverage: number, mark: string, exposure: string, lotSize: string): string {
  const cap = maxNotional(tiers, leverage);
  if (!ok(mark) || !ok(cap) || !ok(lotSize)) return "0";
  const room = sub(div(cap, mark, decimalsOf(lotSize) + 8, "down"), exposure);
  return sign(room) > 0 ? normalize(quantize(room, lotSize, "down")) : "0";
}

/** RiskCheck is an opening order against its leverage's cap, at the mark. */
export type RiskCheck = { ok: boolean; cap: string; notional: string };

/** checkRiskLimit tells whether an opening order keeps its side within the leverage's cap. */
export function checkRiskLimit(tiers: readonly RiskTier[], leverage: number, mark: string, exposure: string, quantity: string): RiskCheck {
  const cap = maxNotional(tiers, leverage);
  const notional = ok(mark) && isDecimal(quantity) ? normalize(mul(add(exposure, quantity), mark)) : "0";
  return { ok: !gt(notional, cap), cap, notional };
}

/** openLimit is the most an order may open: the margin's and the risk limit's lesser. */
export function openLimit(byMargin: string, byRisk: string): string {
  return normalize(min(byMargin, byRisk));
}
