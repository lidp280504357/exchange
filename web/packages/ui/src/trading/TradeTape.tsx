import { formatAmount, formatPrice, formatTime, type TradeData } from "@exchange/core";
import { memo, useCallback, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Skeleton } from "../components/Skeleton";
import { cn } from "../lib/cn";
import { useFormatContext } from "../lib/settings";

export type TradeTapeProps = {
  /** Public trades, newest first (core useTrades). */
  trades: TradeData[];
  priceDecimals: number;
  qtyDecimals: number;
  /** Rows shown (default 30). */
  max?: number;
  base?: string;
  quote?: string;
  onPriceClick?: (price: string) => void;
  loading?: boolean;
  /** Row height in px (default 20). */
  rowHeight?: number;
  className?: string;
};

type RowProps = {
  trade: TradeData;
  priceDecimals: number;
  qtyDecimals: number;
  locale: string;
  timeZone: string | undefined;
  height: number;
  /** Only the newest row slides in, and only when it just arrived. */
  animate: boolean;
  onPick: (price: string) => void;
};

// One trade: formatted once, then left alone (memo, keyed by trade ID).
const TradeRow = memo(function TradeRow({ trade, priceDecimals, qtyDecimals, locale, timeZone, height, animate, onPick }: RowProps) {
  const buy = trade.taker_side === "BUY";
  return (
    <button
      type="button"
      tabIndex={-1}
      onClick={() => onPick(trade.price)}
      style={{ height }}
      className={cn(
        "grid w-full shrink-0 grid-cols-[1fr_1fr_1fr] items-center px-3 text-xs tabular-nums hover:bg-bg-2",
        animate && "animate-slide-down",
      )}
    >
      <span className={cn("text-left", buy ? "text-up" : "text-down")}>{formatPrice(trade.price, priceDecimals)}</span>
      <span className="text-right text-fg-1">{formatAmount(trade.quantity, qtyDecimals)}</span>
      <span className="text-right text-fg-3">{formatTime(trade.executed_at, "timeSeconds", locale, timeZone)}</span>
    </button>
  );
});

/**
 * TradeTape lists the latest public trades: price coloured by the taker's
 * side, amount and time (in the user's zone). A new trade slides in from
 * the top in 150 ms; the others just move down, so a busy market costs one
 * animated row per frame.
 */
export function TradeTape({ trades, priceDecimals, qtyDecimals, max = 30, base, quote, onPriceClick, loading, rowHeight = 20, className }: TradeTapeProps) {
  const { t } = useTranslation();
  const { locale, timeZone } = useFormatContext();
  const pickRef = useRef(onPriceClick);
  pickRef.current = onPriceClick;
  const onPick = useCallback((price: string) => pickRef.current?.(price), []);

  // The newest ID seen at the last commit: a different one on this render
  // is a fresh trade. Nothing animates on the first render.
  const shownNewest = useRef<string | null>(null);
  const mounted = useRef(false);
  const newest = trades[0]?.trade_id ?? null;
  const fresh = mounted.current && newest !== null && newest !== shownNewest.current ? newest : null;
  useEffect(() => {
    shownNewest.current = newest;
    mounted.current = true;
  });

  const rows = trades.slice(0, max);
  return (
    <section aria-label={t("ui.tape.title")} className={cn("flex min-w-0 flex-col bg-bg-1 text-fg-1", className)}>
      <div className="grid h-6 shrink-0 grid-cols-[1fr_1fr_1fr] items-center px-3 text-xs text-fg-3">
        <span>
          {t("common.price")}
          {quote && `(${quote})`}
        </span>
        <span className="text-right">
          {t("common.amount")}
          {base && `(${base})`}
        </span>
        <span className="text-right">{t("common.time")}</span>
      </div>
      <div className="flex flex-col overflow-hidden" style={{ height: max * rowHeight }}>
        {loading && rows.length === 0
          ? Array.from({ length: Math.min(max, 10) }, (_, i) => (
              <div key={`sk-${i}`} className="flex items-center gap-3 px-3" style={{ height: rowHeight }}>
                <Skeleton className="h-3 flex-1" />
                <Skeleton className="h-3 flex-1" />
                <Skeleton className="h-3 flex-1" />
              </div>
            ))
          : rows.map((tr) => (
              <TradeRow
                key={tr.trade_id}
                trade={tr}
                priceDecimals={priceDecimals}
                qtyDecimals={qtyDecimals}
                locale={locale}
                timeZone={timeZone}
                height={rowHeight}
                animate={tr.trade_id === fresh}
                onPick={onPick}
              />
            ))}
      </div>
    </section>
  );
}
