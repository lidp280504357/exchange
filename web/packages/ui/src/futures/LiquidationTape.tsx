import { dec, formatAmount, formatCompact, formatDecimal, formatPrice, formatTime } from "@exchange/core";
import type { Liquidation } from "@exchange/core/futures/data";
import { memo, useRef } from "react";
import { cn } from "../lib/cn";
import { useFormatContext } from "../lib/settings";

// A contract's liquidations as they stream in (design 2026-10-06 §3.3):
// the time, whose position was closed (a long by a forced sell, in the
// fall colour; a short by a forced buy, in the rise colour), the average
// price, the quantity and its USD value, newest first. A new one slides in
// from the top; the rest only move down.

export type LiquidationTapeLabels = {
  time: string;
  side: string;
  price: string;
  quantity: string;
  value: string;
  /** "多单爆仓": a long position closed. */
  long: string;
  short: string;
};

export type LiquidationTapeProps = {
  /** Newest first (core useLiquidations). */
  items: readonly Liquidation[];
  priceDecimals: number;
  /** Decimals of a quantity: the lot's (0 for contracts). */
  qtyDecimals: number;
  labels: LiquidationTapeLabels;
  /** Rows shown (default 30). */
  max?: number;
  /** Row height in px (default 24). */
  rowHeight?: number;
  className?: string;
};

type RowProps = {
  item: Liquidation;
  priceDecimals: number;
  qtyDecimals: number;
  labels: LiquidationTapeLabels;
  locale: string;
  timeZone: string | undefined;
  height: number;
  animate: boolean;
};

/** The USD value as written in the list: in full to the cent below a million, compact above. */
export function liquidationValue(v: string, locale: string): string {
  return dec.isDecimal(v) && dec.gte(dec.abs(v), "1000000") ? formatCompact(v, locale) : formatDecimal(v, { decimals: 2, rounding: "half" });
}

const Row = memo(function Row({ item, priceDecimals, qtyDecimals, labels, locale, timeZone, height, animate }: RowProps) {
  const long = item.position_side === "LONG";
  return (
    <div
      role="row"
      style={{ height }}
      className={cn("grid grid-cols-[1fr_1fr_1.2fr_1fr_1fr] items-center gap-2 px-3 text-xs tabular-nums", animate && "animate-slide-down")}
    >
      <span role="cell" className="text-left text-fg-3">
        {formatTime(item.traded_at, "timeSeconds", locale, timeZone)}
      </span>
      <span role="cell" className={cn("text-left", long ? "text-down" : "text-up")}>
        {long ? labels.long : labels.short}
      </span>
      <span role="cell" className="text-right text-fg-1" title={`${labels.price} ${formatPrice(item.average_price, priceDecimals)}`}>
        {formatPrice(item.average_price, priceDecimals)}
      </span>
      <span role="cell" className="text-right text-fg-1">
        {formatAmount(item.quantity, qtyDecimals)}
      </span>
      <span role="cell" className="text-right text-fg-2">
        {liquidationValue(item.value_usd, locale)}
      </span>
    </div>
  );
});

// A liquidation's identity is the service's primary key (core liquidationKey).
const keyOf = (l: Liquidation) => `${l.symbol}|${Date.parse(l.traded_at)}|${l.position_side}`;

export function LiquidationTape({ items, priceDecimals, qtyDecimals, labels, max = 30, rowHeight = 24, className }: LiquidationTapeProps) {
  const { locale, timeZone } = useFormatContext();
  const shown = items.slice(0, max);
  // The orders of the first list stay put; one that arrives later on top
  // slides in once, as its row mounts (the rows are keyed).
  const initial = useRef<Set<string> | null>(null);
  if (initial.current === null && shown.length > 0) initial.current = new Set(shown.map(keyOf));
  return (
    <div role="table" aria-rowcount={shown.length + 1} className={cn("flex flex-col", className)}>
      <div role="row" className="grid h-7 shrink-0 grid-cols-[1fr_1fr_1.2fr_1fr_1fr] items-center gap-2 px-3 text-xs text-fg-3">
        <span role="columnheader" className="text-left">
          {labels.time}
        </span>
        <span role="columnheader" className="text-left">
          {labels.side}
        </span>
        <span role="columnheader" className="text-right">
          {labels.price}
        </span>
        <span role="columnheader" className="text-right">
          {labels.quantity}
        </span>
        <span role="columnheader" className="text-right">
          {labels.value}
        </span>
      </div>
      <div role="rowgroup" className="min-h-0 flex-1 overflow-hidden">
        {shown.map((l, i) => {
          const key = keyOf(l);
          return (
            <Row
              key={key}
              item={l}
              priceDecimals={priceDecimals}
              qtyDecimals={qtyDecimals}
              labels={labels}
              locale={locale}
              timeZone={timeZone}
              height={rowHeight}
              animate={i === 0 && !initial.current?.has(key)}
            />
          );
        })}
      </div>
    </div>
  );
}
