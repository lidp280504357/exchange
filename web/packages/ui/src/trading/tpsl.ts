import { dec } from "@exchange/core";

// Take-profit and stop-loss rules (requirements §11.7: triggered by the
// mark or the last price, reduce-only). A trigger must lie on the right
// side of the price it watches, or it would fire at once:
//   LONG:  take profit above, stop loss below;
//   SHORT: take profit below, stop loss above.

export type TriggerType = "MARK" | "LAST";
export type PositionSide = "LONG" | "SHORT";

export type TpSlLeg = { price: string; triggerType: TriggerType };
export type TpSlValues = { takeProfit?: TpSlLeg; stopLoss?: TpSlLeg };

/** The message key (under ui.tpsl) of a leg on the wrong side. */
export type TpSlErrorKey = "tpAbove" | "tpBelow" | "slBelow" | "slAbove";
export type TpSlErrors = { takeProfit?: TpSlErrorKey; stopLoss?: TpSlErrorKey };

export type ReferencePrices = { MARK?: string | null; LAST?: string | null };

function filled(v: string | undefined | null): v is string {
  return Boolean(v) && dec.isDecimal(v as string);
}

/**
 * validateTpSl checks each leg against the current price of its trigger
 * type. An empty leg, or one whose reference price is unknown, passes.
 */
export function validateTpSl(side: PositionSide, values: TpSlValues, reference: ReferencePrices): TpSlErrors {
  const errors: TpSlErrors = {};
  const tp = values.takeProfit;
  const sl = values.stopLoss;
  if (tp && filled(tp.price)) {
    const ref = reference[tp.triggerType];
    if (filled(ref)) {
      if (side === "LONG" && !dec.gt(tp.price, ref)) errors.takeProfit = "tpAbove";
      if (side === "SHORT" && !dec.lt(tp.price, ref)) errors.takeProfit = "tpBelow";
    }
  }
  if (sl && filled(sl.price)) {
    const ref = reference[sl.triggerType];
    if (filled(ref)) {
      if (side === "LONG" && !dec.lt(sl.price, ref)) errors.stopLoss = "slBelow";
      if (side === "SHORT" && !dec.gt(sl.price, ref)) errors.stopLoss = "slAbove";
    }
  }
  return errors;
}

/** estimatePnl is the PnL if the whole position closed at `price`: (price − entry) × qty, negated for shorts. */
export function estimatePnl(side: PositionSide, entryPrice: string, price: string, quantity: string): string | null {
  if (!filled(entryPrice) || !filled(price) || !filled(quantity)) return null;
  const pnl = dec.mul(dec.sub(price, entryPrice), quantity);
  return side === "LONG" ? pnl : dec.neg(pnl);
}
