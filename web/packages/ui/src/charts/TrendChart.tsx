import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../lib/cn";

// A small chart for figures over days (the admin overview and reports,
// design §10.3): bars and lines over the same x axis, bars on the left
// scale and lines on the right, a guide and the values under the pointer.
// SVG sized to its container; no chart library.

export type TrendColor = "chart-1" | "chart-2" | "chart-3" | "chart-4" | "chart-5";

export type TrendSeries = {
  key: string;
  label: string;
  kind: "bar" | "line";
  color: TrendColor;
  /** Formats a value in the tooltip and on the axis. */
  format?: (v: number) => string;
};

export type TrendPoint = { x: string; label?: string; values: Record<string, number> };

export type TrendChartProps = {
  data: TrendPoint[];
  series: TrendSeries[];
  height?: number;
  className?: string;
  "aria-label"?: string;
};

const fills: Record<TrendColor, string> = { "chart-1": "fill-chart-1", "chart-2": "fill-chart-2", "chart-3": "fill-chart-3", "chart-4": "fill-chart-4", "chart-5": "fill-chart-5" };
const strokes: Record<TrendColor, string> = {
  "chart-1": "stroke-chart-1", "chart-2": "stroke-chart-2", "chart-3": "stroke-chart-3", "chart-4": "stroke-chart-4", "chart-5": "stroke-chart-5",
};
const dots: Record<TrendColor, string> = { "chart-1": "bg-chart-1", "chart-2": "bg-chart-2", "chart-3": "bg-chart-3", "chart-4": "bg-chart-4", "chart-5": "bg-chart-5" };

const PAD = { top: 12, right: 48, bottom: 24, left: 48 };

/** niceMax rounds a maximum up to 1, 2 or 5 times a power of ten. */
export function niceMax(v: number): number {
  if (!(v > 0)) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  const m = v / p;
  return (m <= 1 ? 1 : m <= 2 ? 2 : m <= 5 ? 5 : 10) * p;
}

const plain = (v: number) => (Math.abs(v) >= 1000 ? Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(v) : String(Math.round(v * 100) / 100));

export function TrendChart({ data, series, height = 220, className, "aria-label": ariaLabel }: TrendChartProps) {
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [hover, setHover] = useState<number | null>(null);
  useEffect(() => {
    const el = box.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e?.contentRect.width ?? 0)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const bars = series.filter((s) => s.kind === "bar");
  const lines = series.filter((s) => s.kind === "line");
  const scale = useMemo(() => {
    const max = (list: TrendSeries[]) => niceMax(Math.max(0, ...data.flatMap((d) => list.map((s) => d.values[s.key] ?? 0))));
    return { left: max(bars), right: max(lines) };
  }, [data, bars, lines]);

  const innerW = Math.max(0, width - PAD.left - PAD.right);
  const innerH = height - PAD.top - PAD.bottom;
  const step = data.length ? innerW / data.length : 0;
  const x = (i: number) => PAD.left + step * i + step / 2;
  const yL = (v: number) => PAD.top + innerH - (v / scale.left) * innerH;
  const yR = (v: number) => PAD.top + innerH - (v / scale.right) * innerH;
  const barW = bars.length ? Math.max(2, Math.min(28, (step * 0.7) / bars.length)) : 0;
  const every = Math.max(1, Math.ceil(data.length / Math.max(1, Math.floor(innerW / 64))));
  const fmtL = bars[0]?.format ?? plain;
  const fmtR = lines[0]?.format ?? plain;

  return (
    <div ref={box} className={cn("relative w-full select-none", className)} style={{ height }}>
      {width > 0 && (
        <svg
          width={width}
          height={height}
          role="img"
          aria-label={ariaLabel}
          onMouseMove={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            const i = Math.floor((e.clientX - r.left - PAD.left) / (step || 1));
            setHover(i >= 0 && i < data.length ? i : null);
          }}
          onMouseLeave={() => setHover(null)}
        >
          {[0, 0.5, 1].map((f) => (
            <g key={f}>
              <line x1={PAD.left} x2={width - PAD.right} y1={PAD.top + innerH * (1 - f)} y2={PAD.top + innerH * (1 - f)} className="stroke-line-1" />
              {bars.length > 0 && (
                <text x={PAD.left - 6} y={PAD.top + innerH * (1 - f) + 4} textAnchor="end" className="fill-fg-3 text-[10px]">
                  {fmtL(scale.left * f)}
                </text>
              )}
              {lines.length > 0 && (
                <text x={width - PAD.right + 6} y={PAD.top + innerH * (1 - f) + 4} className="fill-fg-3 text-[10px]">
                  {fmtR(scale.right * f)}
                </text>
              )}
            </g>
          ))}
          {hover !== null && <rect x={PAD.left + step * hover} y={PAD.top} width={step} height={innerH} className="fill-bg-2" />}
          {data.map((d, i) =>
            bars.map((s, j) => {
              const v = d.values[s.key] ?? 0;
              const top = yL(v);
              return (
                <rect
                  key={`${d.x}-${s.key}`}
                  x={x(i) - (barW * bars.length) / 2 + j * barW}
                  y={top}
                  width={barW - 1}
                  height={Math.max(0, PAD.top + innerH - top)}
                  rx={1}
                  className={fills[s.color]}
                />
              );
            }),
          )}
          {lines.map((s) => (
            <polyline
              key={s.key}
              fill="none"
              strokeWidth={2}
              className={strokes[s.color]}
              points={data.map((d, i) => `${x(i)},${yR(d.values[s.key] ?? 0)}`).join(" ")}
            />
          ))}
          {data.map((d, i) =>
            i % every === 0 ? (
              <text key={d.x} x={x(i)} y={height - 6} textAnchor="middle" className="fill-fg-3 text-[10px]">
                {d.label ?? d.x}
              </text>
            ) : null,
          )}
        </svg>
      )}
      {hover !== null && data[hover] && (
        <div
          className="pointer-events-none absolute top-2 z-10 min-w-36 rounded-2 border border-line-1 bg-bg-1 px-3 py-2 text-xs shadow-pop"
          style={{ left: Math.min(Math.max(0, x(hover) + 12), Math.max(0, width - 170)) }}
        >
          <div className="mb-1 font-medium text-fg-1">{data[hover].label ?? data[hover].x}</div>
          {series.map((s) => (
            <div key={s.key} className="flex items-center gap-2 text-fg-2">
              <span className={cn("size-2 rounded-full", dots[s.color])} />
              <span className="flex-1">{s.label}</span>
              <span className="font-mono tabular-nums text-fg-1">{(s.format ?? plain)(data[hover]!.values[s.key] ?? 0)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
