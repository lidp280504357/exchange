import { dec, formatAmount, formatPercent, formatPrice, type BookLevel, type BookView } from "@exchange/core";
import { ArrowDown, ArrowUp, LoaderCircle } from "lucide-react";
import { memo, useCallback, useRef, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { PriceText } from "../components/PriceText";
import { Select } from "../components/Select";
import { Skeleton } from "../components/Skeleton";
import { cn } from "../lib/cn";
import { FlashLayer, useFlash } from "../lib/useFlash";
import { DepthBars } from "../data/DepthBars";

export type BookMode = "both" | "bids" | "asks";

export type OrderBookProps = {
  /** The book cut by useOrderBook (core): best levels first, running totals. */
  view: BookView;
  priceDecimals: number;
  qtyDecimals: number;
  /**
   * Rows per side in "both" (default 20; 12 on the mobile site). A single
   * side shows twice as many, so request 2 × levels from the store then.
   */
  levels?: number;
  mode?: BookMode;
  onModeChange?: (mode: BookMode) => void;
  /** The middle row: last price, coloured by its last move, with the spread. */
  lastPrice?: string | null;
  lastDirection?: "up" | "down" | null;
  /** Futures: the mark price under the last price. */
  markPrice?: string | null;
  /** Aggregation steps (multiples of the tick) and the current one. */
  steps?: string[];
  step?: string;
  onStepChange?: (step: string) => void;
  /** A click fills the price; Shift+click also the cumulative quantity. */
  onPriceClick?: (price: string, cumulativeQuantity?: string) => void;
  /** The depth channel waits for a snapshot (core useSyncing). */
  syncing?: boolean;
  loading?: boolean;
  base?: string;
  quote?: string;
  /** Row height in px (default 20). */
  rowHeight?: number;
  /** The toolbar with the view switch and the step (default on). */
  toolbar?: boolean;
  className?: string;
};

type Pick = (price: string, total: string, shift: boolean) => void;

type RowProps = {
  side: "bid" | "ask";
  price: string;
  quantity: string;
  total: string;
  /** total ÷ the largest total, rounded to 0.001 (fewer re-renders). */
  ratio: number;
  priceDecimals: number;
  qtyDecimals: number;
  height: number;
  focusable: boolean;
  onPick: Pick;
};

// One level. Memoized with primitive props: a frame re-renders only the
// rows whose numbers changed. A new price mounts a new row (key = price),
// which fades in; a changed quantity flashes (the PriceText pattern).
const BookRow = memo(function BookRow({ side, price, quantity, total, ratio, priceDecimals, qtyDecimals, height, focusable, onPick }: RowProps) {
  const flash = useFlash(quantity);
  return (
    <button
      type="button"
      data-book-row
      tabIndex={focusable ? 0 : -1}
      onClick={(e) => onPick(price, total, e.shiftKey)}
      style={{ height }}
      className="relative grid w-full shrink-0 animate-fade-in grid-cols-[1fr_1fr_1fr] items-center px-3 text-xs tabular-nums hover:bg-bg-2 focus-visible:bg-bg-2 focus-visible:outline-none"
    >
      <DepthBars ratio={ratio} side={side === "bid" ? "buy" : "sell"} />
      <span className={cn("relative text-left", side === "bid" ? "text-up" : "text-down")}>{formatPrice(price, priceDecimals)}</span>
      <span className="relative text-right text-fg-1">
        <span className="relative isolate rounded-1 px-0.5">
          <FlashLayer flash={flash} />
          {formatAmount(quantity, qtyDecimals)}
        </span>
      </span>
      <span className="relative text-right text-fg-2">{formatAmount(total, qtyDecimals)}</span>
    </button>
  );
});

function ratioOf(total: string, max: number): number {
  if (!(max > 0)) return 0;
  return Math.round((dec.toNumber(total) / max) * 1000) / 1000;
}

// ModeIcon draws the view switch's little icons with the rise and fall tokens.
function ModeIcon({ mode }: { mode: BookMode }) {
  return (
    <span aria-hidden className="grid h-3 w-3.5 grid-rows-2 gap-px">
      <span className={cn("rounded-[1px]", mode === "bids" ? "bg-up" : "bg-down")} />
      <span className={cn("rounded-[1px]", mode === "asks" ? "bg-down" : "bg-up")} />
    </span>
  );
}

/**
 * OrderBook renders a BookView: asks on top with the lowest ask next to the
 * middle row (last price and spread), bids below, each row with a depth
 * bar. Built for 20 levels a side at 60 fps: memoized rows, fixed section
 * heights (no layout shift), flashes and bars by CSS. Arrow keys move
 * between rows; Enter fills the price, Shift+Enter or Shift+click also the
 * cumulative quantity.
 */
export function OrderBook({
  view, priceDecimals, qtyDecimals, levels = 20, mode = "both", onModeChange, lastPrice, lastDirection, markPrice, steps, step, onStepChange,
  onPriceClick, syncing, loading, base, quote, rowHeight = 20, toolbar = true, className,
}: OrderBookProps) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  const pickRef = useRef(onPriceClick);
  pickRef.current = onPriceClick;
  const onPick = useCallback<Pick>((price, total, shift) => pickRef.current?.(price, shift ? total : undefined), []);

  const perSide = mode === "both" ? levels : levels * 2;
  const max = dec.toNumber(view.maxTotal);
  const asks = mode === "bids" ? [] : view.asks.slice(0, perSide).reverse();
  const bids = mode === "asks" ? [] : view.bids.slice(0, perSide);
  const bestAsk = view.asks[0]?.price;
  const spreadPct = view.spread && bestAsk && dec.sign(bestAsk) > 0 ? dec.div(view.spread, bestAsk, 8) : null;
  const empty = view.asks.length === 0 && view.bids.length === 0;

  const keyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
    const rows = Array.from(ref.current?.querySelectorAll<HTMLButtonElement>("[data-book-row]") ?? []);
    const at = rows.indexOf(document.activeElement as HTMLButtonElement);
    if (at < 0) return;
    e.preventDefault();
    rows[Math.max(0, Math.min(rows.length - 1, at + (e.key === "ArrowDown" ? 1 : -1)))]?.focus();
  };

  const section = (list: BookLevel[], side: "bid" | "ask") => (
    <div
      role="group"
      aria-label={side === "ask" ? t("ui.book.asks") : t("ui.book.bids")}
      className={cn("flex flex-col overflow-hidden", side === "ask" && "justify-end")}
      style={{ height: perSide * rowHeight }}
    >
      {loading && empty
        ? Array.from({ length: Math.min(perSide, 8) }, (_, i) => (
            <div key={`sk-${i}`} className="flex items-center gap-3 px-3" style={{ height: rowHeight }}>
              <Skeleton className="h-3 flex-1" />
              <Skeleton className="h-3 flex-1" />
              <Skeleton className="h-3 flex-1" />
            </div>
          ))
        : list.map((l, i) => (
            <BookRow
              key={l.price}
              side={side}
              price={l.price}
              quantity={l.quantity}
              total={l.total}
              ratio={ratioOf(l.total, max)}
              priceDecimals={priceDecimals}
              qtyDecimals={qtyDecimals}
              height={rowHeight}
              focusable={side === "ask" ? i === list.length - 1 : i === 0}
              onPick={onPick}
            />
          ))}
    </div>
  );

  const middle = (
    <div className="flex h-9 shrink-0 items-center gap-2 border-y border-line-1 px-3">
      <button
        type="button"
        disabled={!lastPrice}
        onClick={() => lastPrice && onPriceClick?.(lastPrice)}
        className="flex items-center gap-1 text-md font-semibold disabled:cursor-default"
      >
        <PriceText value={lastPrice} decimals={priceDecimals} tone={lastDirection ?? "neutral"} />
        {lastDirection === "up" && <ArrowUp size={14} className="text-up" aria-hidden />}
        {lastDirection === "down" && <ArrowDown size={14} className="text-down" aria-hidden />}
      </button>
      {markPrice && (
        <span className="text-xs text-fg-3 tabular-nums" title={t("ui.book.mark")}>
          {formatPrice(markPrice, priceDecimals)}
        </span>
      )}
      <span className="ml-auto text-xs text-fg-3 tabular-nums">
        {t("ui.book.spread")} {formatPrice(view.spread, priceDecimals)}
        {spreadPct && <span className="ml-1">({formatPercent(spreadPct, 3, false)})</span>}
      </span>
    </div>
  );

  return (
    <div ref={ref} onKeyDown={keyDown} className={cn("relative flex min-w-0 flex-col bg-bg-1 text-fg-1", className)}>
      {toolbar && (
        <div className="flex h-9 shrink-0 items-center gap-1 px-2">
          <div role="group" aria-label={t("ui.book.view")} className="flex items-center gap-0.5">
            {(["both", "bids", "asks"] as const).map((m) => (
              <button
                key={m}
                type="button"
                aria-pressed={mode === m}
                aria-label={t(`ui.book.${m}`)}
                title={t(`ui.book.${m}`)}
                onClick={() => onModeChange?.(m)}
                className={cn("grid size-6 place-items-center rounded-1 transition-opacity", mode === m ? "bg-bg-3 opacity-100" : "opacity-50 hover:opacity-100")}
              >
                <ModeIcon mode={m} />
              </button>
            ))}
          </div>
          {steps && steps.length > 0 && (
            <Select
              size="xs"
              variant="ghost"
              className="ml-auto"
              value={step ?? steps[0]}
              onValueChange={onStepChange}
              options={steps.map((s) => ({ value: s, label: s }))}
              aria-label={t("ui.book.step")}
            />
          )}
        </div>
      )}
      <div className="grid h-6 shrink-0 grid-cols-[1fr_1fr_1fr] items-center px-3 text-xs text-fg-3">
        <span>
          {t("common.price")}
          {quote && `(${quote})`}
        </span>
        <span className="text-right">
          {t("common.amount")}
          {base && `(${base})`}
        </span>
        <span className="text-right">
          {t("ui.book.cumulative")}
          {base && `(${base})`}
        </span>
      </div>
      <div className={cn("flex flex-col transition-opacity duration-[var(--t-base)]", syncing && "opacity-50")}>
        {mode !== "bids" && section(asks, "ask")}
        {middle}
        {mode !== "asks" && section(bids, "bid")}
      </div>
      {syncing && (
        <div aria-live="polite" className="pointer-events-none absolute inset-x-0 top-1/2 flex -translate-y-1/2 justify-center">
          <span className="flex items-center gap-1.5 rounded-full border border-line-2 bg-bg-2 px-3 py-1 text-xs text-fg-2 shadow-pop">
            <LoaderCircle size={12} className="animate-spin" />
            {t("common.syncing")}
          </span>
        </div>
      )}
    </div>
  );
}
