import { cn } from "@exchange/ui";
import { useEffect, useMemo, useRef, useState } from "react";

// Prices over time (the simulated market's target and last price, ASTRA
// design §6.1): lines on a scale from the lowest to the highest price
// shown, not from zero, with a guide and the values under the pointer.
// SVG sized to its container, like TrendChart.

export type PriceLine = { key: string; label: string; className: string; dotClassName: string };
export type PricePoint = { at: number; label: string; values: Record<string, number | null> };

const PAD = { top: 12, right: 64, bottom: 24, left: 12 };

export function PriceLines({
  data, lines, height = 260, format = (v: number) => String(v), className, "aria-label": ariaLabel,
}: {
  data: PricePoint[];
  lines: PriceLine[];
  height?: number;
  format?: (v: number) => string;
  className?: string;
  "aria-label"?: string;
}) {
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
  const { lo, hi } = useMemo(() => {
    const all = data.flatMap((d) => lines.map((l) => d.values[l.key])).filter((v): v is number => v !== null && Number.isFinite(v));
    if (!all.length) return { lo: 0, hi: 1 };
    const min = Math.min(...all);
    const max = Math.max(...all);
    const pad = (max - min) * 0.08 || Math.abs(max) * 0.01 || 1;
    return { lo: min - pad, hi: max + pad };
  }, [data, lines]);
  const innerW = Math.max(0, width - PAD.left - PAD.right);
  const innerH = height - PAD.top - PAD.bottom;
  const x = (i: number) => PAD.left + (data.length > 1 ? (innerW * i) / (data.length - 1) : innerW / 2);
  const y = (v: number) => PAD.top + ((hi - v) / (hi - lo)) * innerH;
  const every = Math.max(1, Math.ceil(data.length / Math.max(1, Math.floor(innerW / 72))));
  const path = (key: string) => {
    let d = "";
    let pen = false;
    data.forEach((p, i) => {
      const v = p.values[key];
      if (v === null || v === undefined || !Number.isFinite(v)) {
        pen = false;
        return;
      }
      d += `${pen ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
      pen = true;
    });
    return d;
  };
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
            const i = Math.round(((e.clientX - r.left - PAD.left) / (innerW || 1)) * (data.length - 1));
            setHover(i >= 0 && i < data.length ? i : null);
          }}
          onMouseLeave={() => setHover(null)}
        >
          {[0, 0.5, 1].map((f) => {
            const v = lo + (hi - lo) * f;
            return (
              <g key={f}>
                <line x1={PAD.left} x2={width - PAD.right} y1={y(v)} y2={y(v)} className="stroke-line-1" />
                <text x={width - PAD.right + 6} y={y(v) + 4} className="fill-fg-3 text-[10px]">
                  {format(v)}
                </text>
              </g>
            );
          })}
          {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={PAD.top + innerH} className="stroke-line-2" />}
          {lines.map((l) => (
            <path key={l.key} d={path(l.key)} fill="none" strokeWidth={1.75} className={l.className} />
          ))}
          {data.map((d, i) =>
            i % every === 0 ? (
              <text key={d.at} x={x(i)} y={height - 6} textAnchor="middle" className="fill-fg-3 text-[10px]">
                {d.label}
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
          <div className="mb-1 font-medium text-fg-1">{data[hover].label}</div>
          {lines.map((l) => {
            const v = data[hover]!.values[l.key];
            return (
              <div key={l.key} className="flex items-center gap-2 text-fg-2">
                <span className={cn("size-2 rounded-full", l.dotClassName)} />
                <span className="flex-1">{l.label}</span>
                <span className="font-mono tabular-nums text-fg-1">{v === null || v === undefined ? "—" : format(v)}</span>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
