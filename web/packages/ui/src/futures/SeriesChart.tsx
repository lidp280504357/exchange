import { useEffect, useMemo, useRef, useState, type FocusEvent, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { cn } from "../lib/cn";
import { useFormatContext } from "../lib/settings";
import { columnPath, formatTicks, indexAt, labelIndexes, niceTicks, seriesDomain, type AxisStyle, type SeriesForm, type SeriesPoint } from "./scale";

// A small time-series chart for the contracts' futures data (design
// 2026-10-06 §3.3): a line (over a wash, or alone), columns from zero
// coloured by sign, two shares stacked to 100%, or two volumes mirrored
// about zero, on one y axis (on the right, like the candle chart's price
// scale) over the points' times. Thin marks (columns at most 24 px with a
// 2 px gap and a rounded data end, 2 px lines, a 10% wash), hairline grid,
// and a crosshair with a tooltip that follows the pointer, a touch or the
// arrow keys. SVG sized to its container; no chart library.

export type SeriesChartProps = {
  points: readonly SeriesPoint[];
  form: SeriesForm;
  /** How the y axis reads its ticks. */
  axis: AxisStyle;
  /** The chart's height with its time axis, px. */
  height?: number;
  /** The label of a point's time on the axis. */
  formatX: (t: number) => string;
  /** The tooltip of point i: its time and every value of it (values first, names after). */
  tooltip: (index: number) => ReactNode;
  /** What the chart shows, for screen readers (the table view has the values). */
  "aria-label": string;
  /** The points are another period's, shown until the right ones arrive: dimmed. */
  stale?: boolean;
  className?: string;
};

/** The time axis's band under the plot, px. */
const AXIS_BAND = 18;
const TOP = 6;
/** Width of a character of a 10 px tick label (tabular figures), px. */
const CHAR = 6.2;

export function SeriesChart({ points, form, axis, height = 160, formatX, tooltip, "aria-label": label, stale, className }: SeriesChartProps) {
  const { locale } = useFormatContext();
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [active, setActive] = useState<number | null>(null);
  const keyboard = useRef(false);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    setWidth(Math.floor(el.getBoundingClientRect().width));
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e?.contentRect.width ?? 0)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const n = points.length;
  const scale = useMemo(() => {
    const [lo, hi] = seriesDomain(form, points);
    return niceTicks(lo, hi, form.kind === "mirror" ? 5 : 4, form.kind === "line" ? 0.08 : 0);
  }, [form, points]);
  // Mirrored volumes are sizes on both sides of zero: no minus signs.
  const mirrored = form.kind === "mirror";
  const tickLabels = useMemo(
    () => formatTicks(scale.ticks, scale.step, axis, locale).map((l) => (mirrored ? l.replace(/^-/, "") : l)),
    [scale, axis, locale, mirrored],
  );
  const right = Math.ceil(Math.max(1, ...tickLabels.map((l) => l.length)) * CHAR) + 10;
  const plotW = Math.max(0, width - right);
  const plotH = Math.max(0, height - AXIS_BAND - TOP);
  const step = n > 0 ? plotW / n : 0;
  const x = (i: number) => step * i + step / 2;
  const span = scale.max - scale.min || 1;
  const y = (v: number) => TOP + plotH - ((v - scale.min) / span) * plotH;
  const zero = Math.min(TOP + plotH, Math.max(TOP, y(0)));
  const barW = Math.max(1, Math.min(24, step - 2));

  // The marks only change with the data and the size, not with the pointer.
  const marks = useMemo(() => {
    if (plotW <= 0 || n === 0) return null;
    const val = (p: SeriesPoint, key: string) => {
      const v = p.v[key];
      return v !== undefined && Number.isFinite(v) ? v : null;
    };
    switch (form.kind) {
      case "line": {
        const pts = points.map((p, i) => [x(i), val(p, form.key)] as const).filter((q): q is readonly [number, number] => q[1] !== null);
        if (pts.length === 0) return null;
        const line = pts.map(([px, v], i) => `${i ? "L" : "M"}${px},${y(v)}`).join(" ");
        const area = `${line} L${pts.at(-1)![0]},${TOP + plotH} L${pts[0]![0]},${TOP + plotH} Z`;
        return (
          <g>
            {form.area && <path d={area} className="fill-chart-1 opacity-10" />}
            <path d={line} fill="none" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" className="stroke-chart-1" />
            {pts.length === 1 && <circle cx={pts[0]![0]} cy={y(pts[0]![1])} r={4} className="fill-chart-1" />}
          </g>
        );
      }
      case "columns":
        return (
          <g>
            {points.map((p, i) => {
              const v = val(p, form.key);
              if (v === null || v === 0) return null;
              return <path key={p.t} d={columnPath(x(i), zero, y(v), barW)} className={v > 0 ? "fill-up" : "fill-down"} />;
            })}
          </g>
        );
      case "share":
        return (
          <g>
            {points.map((p, i) => {
              const up = val(p, form.up) ?? 0;
              const down = val(p, form.down) ?? 0;
              if (!(up + down > 0)) return null;
              const mid = y(up / (up + down));
              return (
                <g key={p.t}>
                  {up > 0 && <path d={columnPath(x(i), y(0), Math.min(y(0), mid + 1), barW, 0)} className="fill-up" />}
                  {down > 0 && <path d={columnPath(x(i), Math.max(y(1), mid - 1), y(1), barW)} className="fill-down" />}
                </g>
              );
            })}
          </g>
        );
      case "mirror":
        return (
          <g>
            {points.map((p, i) => {
              const up = val(p, form.up) ?? 0;
              const down = val(p, form.down) ?? 0;
              return (
                <g key={p.t}>
                  {up > 0 && <path d={columnPath(x(i), zero - 1, Math.min(zero - 1, y(up)), barW)} className="fill-up" />}
                  {down > 0 && <path d={columnPath(x(i), zero + 1, Math.max(zero + 1, y(-down)), barW)} className="fill-down" />}
                </g>
              );
            })}
          </g>
        );
    }
    // x, y and zero follow from the size and the scale listed here.
  }, [points, form, plotW, plotH, n, scale, barW, zero]);

  const pick = (e: PointerEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    setActive(indexAt(e.clientX - r.left, plotW, n));
  };
  const leave = (e: PointerEvent<SVGSVGElement>) => {
    // A touch keeps its point shown until the next one; a mouse leaving clears it.
    if (e.pointerType === "mouse" && !keyboard.current) setActive(null);
  };
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (n === 0) return;
    const at = active ?? n;
    const next = e.key === "ArrowLeft" ? Math.max(0, at - 1) : e.key === "ArrowRight" ? Math.min(n - 1, at + 1) : e.key === "Home" ? 0 : e.key === "End" ? n - 1 : undefined;
    if (e.key === "Escape") setActive(null);
    if (next === undefined) return;
    e.preventDefault();
    keyboard.current = true;
    setActive(next);
  };
  const onFocus = (e: FocusEvent<HTMLDivElement>) => {
    // From the keyboard the latest point shows at once; a click shows the one under the pointer.
    if (e.currentTarget.matches(":focus-visible") && n > 0) {
      keyboard.current = true;
      setActive((a) => a ?? n - 1);
    }
  };
  const onBlur = () => {
    keyboard.current = false;
    setActive(null);
  };

  const shown = active !== null && active < n ? active : null;
  const lineValue = shown !== null && form.kind === "line" ? points[shown]?.v[form.key] : undefined;
  const ax = shown !== null ? x(shown) : 0;

  return (
    <div
      ref={box}
      role="img"
      aria-label={label}
      tabIndex={n > 0 ? 0 : -1}
      onKeyDown={onKey}
      onFocus={onFocus}
      onBlur={onBlur}
      // A drag across the chart reads its points; it must not also switch
      // the tabs of a swipeable panel around it (the phone's terminal).
      onTouchStart={(e) => e.stopPropagation()}
      onTouchEnd={(e) => e.stopPropagation()}
      className={cn(
        "relative w-full select-none rounded-1 outline-none transition-opacity duration-[var(--t-base)] focus-visible:ring-1 focus-visible:ring-brand",
        stale && "opacity-50",
        className,
      )}
      style={{ height }}
    >
      {width > 0 && (
        <svg width={width} height={height} aria-hidden onPointerMove={pick} onPointerDown={pick} onPointerLeave={leave} style={{ touchAction: "pan-y" }}>
          {shown !== null && form.kind !== "line" && <rect x={step * shown} y={TOP} width={step} height={plotH} className="fill-bg-2" />}
          {scale.ticks.map((tick, i) => (
            <g key={tick}>
              <line x1={0} x2={plotW} y1={y(tick)} y2={y(tick)} shapeRendering="crispEdges" className={tick === 0 && scale.min < 0 ? "stroke-line-2" : "stroke-line-1"} />
              <text x={plotW + 6} y={y(tick) + 3.5} className="fill-fg-3 text-[10px] tabular-nums">
                {tickLabels[i]}
              </text>
            </g>
          ))}
          {marks}
          {shown !== null && form.kind === "line" && (
            <g>
              <line x1={ax} x2={ax} y1={TOP} y2={TOP + plotH} shapeRendering="crispEdges" className="stroke-fg-3" />
              {lineValue !== undefined && Number.isFinite(lineValue) && <circle cx={ax} cy={y(lineValue)} r={4} strokeWidth={2} className="fill-chart-1 stroke-bg-1" />}
            </g>
          )}
          {labelIndexes(n, plotW, 84).map((i) => {
            const px = x(i);
            const anchor = px < 28 ? "start" : px > plotW - 28 ? "end" : "middle";
            return (
              <text key={points[i]!.t} x={anchor === "start" ? 0 : anchor === "end" ? plotW : px} y={height - 5} textAnchor={anchor} className="fill-fg-3 text-[10px] tabular-nums">
                {formatX(points[i]!.t)}
              </text>
            );
          })}
        </svg>
      )}
      {shown !== null && (
        <div
          className="pointer-events-none absolute top-1 z-10 min-w-32 rounded-2 border border-line-1 bg-bg-1 px-2.5 py-2 text-xs shadow-pop"
          style={ax > plotW / 2 ? { right: Math.max(0, width - ax + 10) } : { left: ax + 10 }}
        >
          {tooltip(shown)}
        </div>
      )}
    </div>
  );
}
