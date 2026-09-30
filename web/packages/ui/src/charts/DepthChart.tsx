import { dec, formatAmount, formatPrice, type BookLevel, type BookView } from "@exchange/core";
import { useEffect, useMemo, useRef, useState, type PointerEvent } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";
import { numToDecimal } from "./numbers";

export type DepthChartProps = {
  /** The book cut by useOrderBook (core): best levels first, running totals. */
  view: BookView;
  priceDecimals: number;
  qtyDecimals: number;
  /** Height in px (default 220); the width follows the container. */
  height?: number;
  base?: string;
  className?: string;
};

type Point = { price: number; total: number; level: BookLevel };

export type DepthGeometry = {
  bids: Point[];
  asks: Point[];
  /** The price domain, centred on the mid price. */
  min: number;
  max: number;
  mid: number;
  /** The top of the value axis (the deeper side's total plus headroom). */
  top: number;
};

/** depthGeometry turns a book into the numbers the chart draws (floats: drawing only). */
export function depthGeometry(view: BookView): DepthGeometry | null {
  const pt = (l: BookLevel): Point => ({ price: dec.toNumber(l.price), total: dec.toNumber(l.total), level: l });
  const bids = view.bids.map(pt);
  const asks = view.asks.map(pt);
  const bestBid = bids[0]?.price;
  const bestAsk = asks[0]?.price;
  if (bestBid === undefined && bestAsk === undefined) return null;
  const mid = bestBid !== undefined && bestAsk !== undefined ? (bestBid + bestAsk) / 2 : (bestBid ?? bestAsk ?? 0);
  const low = bids[bids.length - 1]?.price ?? mid;
  const high = asks[asks.length - 1]?.price ?? mid;
  let half = Math.max(mid - low, high - mid);
  if (!(half > 0)) half = Math.abs(mid) * 0.01 || 1;
  const deepest = Math.max(bids[bids.length - 1]?.total ?? 0, asks[asks.length - 1]?.total ?? 0);
  return { bids, asks, min: mid - half, max: mid + half, mid, top: deepest > 0 ? deepest * 1.12 : 1 };
}

const PAD_TOP = 8;
const PAD_BOTTOM = 20;

/**
 * DepthChart draws the cumulative bids (left, rise colour) and asks (right,
 * fall colour) of a BookView as stepped areas in SVG. Hovering shows a
 * crosshair with the level's price and cumulative amount.
 */
export function DepthChart({ view, priceDecimals, qtyDecimals, height = 220, base, className }: DepthChartProps) {
  const { t } = useTranslation();
  const boxRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);
  const [hover, setHover] = useState<{ x: number; side: "bid" | "ask"; point: Point } | null>(null);

  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    setWidth(el.clientWidth || 600);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w) setWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const geo = useMemo(() => depthGeometry(view), [view]);
  const plotH = height - PAD_BOTTOM;

  const paths = useMemo(() => {
    if (!geo) return null;
    const span = geo.max - geo.min || 1;
    const x = (p: number) => ((p - geo.min) / span) * width;
    const y = (v: number) => plotH - (v / geo.top) * (plotH - PAD_TOP);
    const r = (n: number) => Math.round(n * 10) / 10;
    const steps = (list: Point[], edge: number) => {
      const out: string[] = [];
      list.forEach((p, i) => {
        const prev = list[i - 1];
        if (i === 0) out.push(`${r(x(p.price))},${r(y(0))}`, `${r(x(p.price))},${r(y(p.total))}`);
        else if (prev) out.push(`${r(x(p.price))},${r(y(prev.total))}`, `${r(x(p.price))},${r(y(p.total))}`);
      });
      const last = list[list.length - 1];
      if (last) out.push(`${r(edge)},${r(y(last.total))}`);
      return out;
    };
    const bidLine = steps(geo.bids, 0);
    const askLine = steps(geo.asks, width);
    const area = (line: string[], edge: number) => (line.length ? `M${line.join(" L")} L${r(edge)},${r(y(0))} Z` : "");
    return {
      bidLine: bidLine.join(" "),
      askLine: askLine.join(" "),
      bidArea: area(bidLine, 0),
      askArea: area(askLine, width),
      x,
      y,
    };
  }, [geo, width, plotH]);

  const move = (e: PointerEvent<SVGSVGElement>) => {
    if (!geo || !paths) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const px = Math.min(width, Math.max(0, e.clientX - rect.left));
    const price = geo.min + (px / (width || 1)) * (geo.max - geo.min);
    if (price <= geo.mid && geo.bids.length) {
      // Bids: the deepest level still at or above the cursor's price.
      let j = 0;
      while (j + 1 < geo.bids.length && (geo.bids[j + 1]?.price ?? -Infinity) >= price) j++;
      const point = geo.bids[j];
      if (point) setHover({ x: px, side: "bid", point });
    } else if (geo.asks.length) {
      let j = 0;
      while (j + 1 < geo.asks.length && (geo.asks[j + 1]?.price ?? Infinity) <= price) j++;
      const point = geo.asks[j];
      if (point) setHover({ x: px, side: "ask", point });
    }
  };

  const tipLeft = hover ? (hover.x > width / 2 ? hover.x - 12 : hover.x + 12) : 0;

  return (
    <div ref={boxRef} className={cn("relative w-full select-none bg-bg-1", className)} style={{ height }}>
      {!geo || !paths ? (
        <div className="grid h-full place-items-center text-sm text-fg-3">{t("state.emptyTitle")}</div>
      ) : (
        <>
          <svg
            width={width}
            height={height}
            role="img"
            aria-label={t("ui.depth.title")}
            onPointerMove={move}
            onPointerLeave={() => setHover(null)}
            className="block touch-none"
          >
            <path d={paths.bidArea} className="fill-up/15" />
            <path d={paths.askArea} className="fill-down/15" />
            <polyline points={paths.bidLine} fill="none" strokeWidth={1.5} className="stroke-up" />
            <polyline points={paths.askLine} fill="none" strokeWidth={1.5} className="stroke-down" />
            <line x1={0} x2={width} y1={plotH} y2={plotH} strokeWidth={1} className="stroke-line-1" />
            {hover && (
              <g className={hover.side === "bid" ? "text-up" : "text-down"}>
                <line x1={hover.x} x2={hover.x} y1={PAD_TOP} y2={plotH} strokeDasharray="3 3" strokeWidth={1} className="stroke-fg-3" />
                <circle cx={hover.x} cy={paths.y(hover.point.total)} r={3.5} fill="currentColor" className="stroke-bg-1" strokeWidth={1.5} />
              </g>
            )}
          </svg>
          <div className="pointer-events-none absolute inset-x-2 bottom-0.5 flex justify-between text-xs tabular-nums text-fg-3">
            <span>{formatPrice(numToDecimal(geo.min, priceDecimals), priceDecimals)}</span>
            <span className="text-fg-2">{formatPrice(numToDecimal(geo.mid, priceDecimals), priceDecimals)}</span>
            <span>{formatPrice(numToDecimal(geo.max, priceDecimals), priceDecimals)}</span>
          </div>
          <div className="pointer-events-none absolute right-2 top-1 text-xs tabular-nums text-fg-3">
            {formatAmount(view.maxTotal, qtyDecimals)}
            {base && ` ${base}`}
          </div>
          <div className="pointer-events-none absolute left-2 top-1 flex gap-3 text-xs">
            <span className="flex items-center gap-1 text-fg-3">
              <span aria-hidden className="size-2 rounded-[2px] bg-up" />
              {t("ui.depth.bids")}
            </span>
            <span className="flex items-center gap-1 text-fg-3">
              <span aria-hidden className="size-2 rounded-[2px] bg-down" />
              {t("ui.depth.asks")}
            </span>
          </div>
          {hover && (
            <div
              className={cn(
                "pointer-events-none absolute top-6 z-10 rounded-2 border border-line-2 bg-bg-2 px-2.5 py-1.5 text-xs shadow-pop",
                hover.x > width / 2 && "-translate-x-full",
              )}
              style={{ left: tipLeft }}
            >
              <div className={cn("mb-0.5 font-medium", hover.side === "bid" ? "text-up" : "text-down")}>
                {hover.side === "bid" ? t("ui.depth.bids") : t("ui.depth.asks")}
              </div>
              <div className="flex justify-between gap-4 tabular-nums">
                <span className="text-fg-3">{t("common.price")}</span>
                <span className="text-fg-1">{formatPrice(hover.point.level.price, priceDecimals)}</span>
              </div>
              <div className="flex justify-between gap-4 tabular-nums">
                <span className="text-fg-3">{t("ui.depth.cumulative")}</span>
                <span className="text-fg-1">
                  {formatAmount(hover.point.level.total, qtyDecimals)}
                  {base && ` ${base}`}
                </span>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}
