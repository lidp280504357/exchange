import { dec, formatDecimal, formatPrice } from "@exchange/core";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Dialog } from "../components/Dialog";
import { NumberInput } from "../components/NumberInput";
import { toneOf } from "../components/PriceText";
import { Segmented } from "../components/Segmented";
import { cn } from "../lib/cn";
import { estimatePnl, validateTpSl, type PositionSide, type TpSlValues, type TriggerType } from "./tpsl";

export type TpSlDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  side: PositionSide;
  /** The position's entry price and size, for the estimated PnL. */
  entryPrice?: string;
  quantity?: string;
  markPrice?: string | null;
  lastPrice?: string | null;
  priceDecimals: number;
  /** The margin asset (default USDT) and its decimals (default 2). */
  quote?: string;
  quoteDecimals?: number;
  /** Values already set (editing existing TP/SL). */
  initial?: TpSlValues;
  /** Gets the legs that were filled in. */
  onConfirm: (values: TpSlValues) => void;
  submitting?: boolean;
  symbol?: string;
};

type Leg = { price: string; triggerType: TriggerType };

/**
 * TpSlDialog sets a position's take-profit and stop-loss triggers, each
 * watching the mark or the last price. A trigger on the wrong side of its
 * price (a long's take profit below it, say) is flagged in place and the
 * dialog will not confirm; the estimated PnL at each trigger shows below.
 */
export function TpSlDialog({
  open, onOpenChange, side, entryPrice, quantity, markPrice, lastPrice, priceDecimals, quote = "USDT", quoteDecimals = 2, initial, onConfirm,
  submitting, symbol,
}: TpSlDialogProps) {
  const { t } = useTranslation();
  const [tp, setTp] = useState<Leg>({ price: "", triggerType: "MARK" });
  const [sl, setSl] = useState<Leg>({ price: "", triggerType: "MARK" });
  const id = useId();

  // Each opening starts from `initial`; later renders (prices ticking) keep the draft.
  const initialRef = useRef(initial);
  initialRef.current = initial;
  useEffect(() => {
    if (!open) return;
    const init = initialRef.current;
    setTp({ price: init?.takeProfit?.price ?? "", triggerType: init?.takeProfit?.triggerType ?? "MARK" });
    setSl({ price: init?.stopLoss?.price ?? "", triggerType: init?.stopLoss?.triggerType ?? "MARK" });
  }, [open]);

  const values: TpSlValues = {
    takeProfit: tp.price ? tp : undefined,
    stopLoss: sl.price ? sl : undefined,
  };
  const errors = validateTpSl(side, values, { MARK: markPrice, LAST: lastPrice });
  const none = !values.takeProfit && !values.stopLoss;
  const ok = !none && !errors.takeProfit && !errors.stopLoss;
  const typeName = (type: TriggerType) => (type === "MARK" ? t("ui.tpsl.mark") : t("ui.tpsl.last"));
  const refOf = (type: TriggerType) => (type === "MARK" ? markPrice : lastPrice);
  const tick = priceDecimals > 0 ? dec.div("1", `1${"0".repeat(priceDecimals)}`, priceDecimals) : "1";

  const leg = (kind: "takeProfit" | "stopLoss", value: Leg, set: (l: Leg) => void) => {
    const err = errors[kind];
    const pnl = entryPrice && quantity ? estimatePnl(side, entryPrice, value.price, quantity) : null;
    const pnlTone = toneOf(pnl);
    const labelId = `${id}-${kind}`;
    return (
      <div role="group" aria-labelledby={labelId} className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <span id={labelId} className="text-sm font-medium text-fg-1">
            {kind === "takeProfit" ? t("codes.TAKE_PROFIT") : t("codes.STOP_LOSS")}
          </span>
          <Segmented
            size="xs"
            value={value.triggerType}
            onValueChange={(v) => set({ ...value, triggerType: v as TriggerType })}
            aria-label={t("ui.tpsl.triggerBy")}
            items={[
              { value: "MARK", label: t("ui.tpsl.mark") },
              { value: "LAST", label: t("ui.tpsl.last") },
            ]}
          />
        </div>
        <NumberInput
          aria-label={`${kind === "takeProfit" ? t("codes.TAKE_PROFIT") : t("codes.STOP_LOSS")} ${t("ui.tpsl.trigger")}`}
          prefix={<span className="text-xs">{t("ui.tpsl.trigger")}</span>}
          unit={quote}
          align="right"
          value={value.price}
          onValueChange={(p) => set({ ...value, price: p })}
          decimals={priceDecimals}
          step={tick}
          error={err ? t(`ui.tpsl.${err}`, { type: typeName(value.triggerType) }) : undefined}
          hint={t("ui.tpsl.reference", { type: typeName(value.triggerType), price: formatPrice(refOf(value.triggerType), priceDecimals) })}
        />
        {pnl && !err && (
          <div className="flex justify-between text-xs">
            <span className="text-fg-3">{t("ui.tpsl.estPnl")}</span>
            <span className={cn("tabular-nums", pnlTone === "up" ? "text-up" : pnlTone === "down" ? "text-down" : "text-fg-1")}>
              {formatDecimal(pnl, { decimals: quoteDecimals, rounding: "half", sign: true })} {quote}
            </span>
          </div>
        )}
      </div>
    );
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={symbol ? `${t("ui.tpsl.title")} · ${symbol}` : t("ui.tpsl.title")}
      size="sm"
      onConfirm={() => ok && onConfirm(values)}
      confirmDisabled={!ok}
      confirmLoading={submitting}
    >
      <div className="flex flex-col gap-5">
        {leg("takeProfit", tp, setTp)}
        {leg("stopLoss", sl, setSl)}
        {none && <p className="text-xs text-fg-3">{t("ui.tpsl.empty")}</p>}
      </div>
    </Dialog>
  );
}
