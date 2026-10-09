import { cn, niceMax, type TrendChartProps, type TrendColor, type TrendSeries } from "@exchange/ui";
import { useEffect, useMemo, useRef, useState } from "react";

// TrendChart for figures that go below zero (results): bars on the left
// scale and lines on the right, each from its lowest to its highest value
// with zero in between and drawn; bars grow from zero up or down. Same
// look, guide and tooltip as TrendChart.

const fills: Record<TrendColor, string> = { "chart-1": "fill-chart-1", "chart-2": "fill-chart-2", "chart-3": "fill-chart-3", "chart-4": "fill-chart-4", "chart-5": "fill-chart-5" };
const strokes: Record<TrendColor, string> = {
  "chart-1": "stroke-chart-1", "chart-2": "stroke-chart-2", "chart-3": "stroke-chart-3", "chart-4": "stroke-chart-4", "chart-5": "stroke-chart-5",
};
const dots: Record<TrendColor, string> = { "chart-1": "bg-chart-1", "chart-2": "bg-chart-2", "chart-3": "bg-chart-3", "chart-4": "bg-chart-4", "chart-5": "bg-chart-5" };

const PAD = { top: 12, right: 56, bottom: 24, left: 56 };

const plain = (v: number) => (Math.abs(v) >= 1000 ? Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(v) : String(Math.round(v * 100) / 100));

/** span is a scale's bounds: nice values around the series' lowest and highest, zero always within. */
function span(values: number[]): { lo: number; hi: number } {
  const hi = Math.max(0, ...values);
  const lo = Math.min(0, ...values);
  if (hi === 0 && lo === 0) return { lo: 0, hi: 1 };
  return { lo: lo < 0 ? -niceMax(-lo) : 0, hi: hi > 0 ? niceMax(hi) : 0 };
}

export function SignedChart({ data, series, height = 240, className, "aria-label": ariaLabel }: TrendChartProps) {
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
    const of = (list: TrendSeries[]) => span(data.flatMap((d) => list.map((s) => d.values[s.key] ?? 0)));
    return { left: of(bars), right: of(lines) };
  }, [data, bars, lines]);

  const innerW = Math.max(0, width - PAD.left - PAD.right);
  const innerH = height - PAD.top - PAD.bottom;
  const step = data.length ? innerW / data.length : 0;
  const x = (i: number) => PAD.left + step * i + step / 2;
  const y = (s: { lo: number; hi: number }) => (v: number) => PAD.top + ((s.hi - v) / (s.hi - s.lo)) * innerH;
  const yL = y(scale.left);
  const yR = y(scale.right);
  const barW = bars.length ? Math.max(2, Math.min(28, (step * 0.7) / bars.length)) : 0;
  const every = Math.max(1, Math.ceil(data.length / Math.max(1, Math.floor(innerW / 64))));
  const fmtL = bars[0]?.format ?? plain;
  const fmtR = lines[0]?.format ?? plain;
  const ticks = (s: { lo: number; hi: number }) => [...new Set([s.lo, 0, s.hi])];

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
          {bars.length > 0 &&
            ticks(scale.left).map((v) => (
              <g key={`l${v}`}>
                <line x1={PAD.left} x2={width - PAD.right} y1={yL(v)} y2={yL(v)} className={v === 0 ? "stroke-line-2" : "stroke-line-1"} />
                <text x={PAD.left - 6} y={yL(v) + 4} textAnchor="end" className="fill-fg-3 text-[10px]">
                  {fmtL(v)}
                </text>
              </g>
            ))}
          {lines.length > 0 &&
            ticks(scale.right).map((v) => (
              <text key={`r${v}`} x={width - PAD.right + 6} y={yR(v) + 4} className="fill-fg-3 text-[10px]">
                {fmtR(v)}
              </text>
            ))}
          {hover !== null && <rect x={PAD.left + step * hover} y={PAD.top} width={step} height={innerH} className="fill-bg-2" />}
          {data.map((d, i) =>
            bars.map((s, j) => {
              const v = d.values[s.key] ?? 0;
              const top = Math.min(yL(v), yL(0));
              return (
                <rect
                  key={`${d.x}-${s.key}`}
                  x={x(i) - (barW * bars.length) / 2 + j * barW}
                  y={top}
                  width={barW - 1}
                  height={Math.abs(yL(v) - yL(0))}
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
          className="pointer-events-none absolute top-2 z-10 min-w-40 rounded-2 border border-line-1 bg-bg-1 px-3 py-2 text-xs shadow-pop"
          style={{ left: Math.min(Math.max(0, x(hover) + 12), Math.max(0, width - 190)) }}
        >
          <div className="mb-1 font-medium text-fg-1">{data[hover].label ?? data[hover].x}</div>
          {series.map((s) => (
            <div key={s.key} className="flex items-center gap-2 text-fg-2">
              <span className={cn("size-2 rounded-full", dots[s.color])} />
              <span className="flex-1">{s.label}</span>
              <span className="tabular-nums text-fg-1">{(s.format ?? plain)(data[hover]!.values[s.key] ?? 0)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
