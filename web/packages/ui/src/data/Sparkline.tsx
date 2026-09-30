import { dec } from "@exchange/core";
import { memo, useId, useMemo } from "react";
import { cn } from "../lib/cn";

export type SparklineTone = "auto" | "up" | "down" | "neutral" | "brand";

export type SparklineProps = {
  /** Values oldest first: decimal strings or numbers (drawing only). */
  data: readonly (string | number)[];
  width?: number;
  height?: number;
  /** auto: green when the last value is not below the first, else red. */
  tone?: SparklineTone;
  /** Fill under the line with a fading gradient (default on). */
  area?: boolean;
  strokeWidth?: number;
  /** Stretch to the parent's width (the viewBox scales, the stroke does not). */
  fluid?: boolean;
  className?: string;
  "aria-label"?: string;
};

const tones: Record<Exclude<SparklineTone, "auto">, string> = {
  up: "text-up",
  down: "text-down",
  neutral: "text-fg-3",
  brand: "text-brand",
};

/** sparkPoints maps values to SVG coordinates inside width × height (with a stroke margin). */
export function sparkPoints(values: readonly number[], width: number, height: number, margin = 2): [number, number][] {
  if (values.length === 0) return [];
  let min = Infinity;
  let max = -Infinity;
  for (const v of values) {
    if (v < min) min = v;
    if (v > max) max = v;
  }
  const span = max - min || 1;
  const stepX = values.length > 1 ? width / (values.length - 1) : 0;
  const usable = height - margin * 2;
  return values.map((v, i) => [i * stepX, max === min ? height / 2 : margin + (1 - (v - min) / span) * usable]);
}

// tenth rounds a coordinate to 0.1 px: shorter markup, same picture.
function tenth(n: number): number {
  return Math.round(n * 10) / 10;
}

/**
 * Sparkline is a tiny trend line (market lists, stat cards): an SVG
 * polyline with an optional gradient area, no axes. It re-renders only
 * when its data or size change.
 */
export const Sparkline = memo(function Sparkline({
  data, width = 96, height = 32, tone = "auto", area = true, strokeWidth = 1.5, fluid, className, "aria-label": ariaLabel,
}: SparklineProps) {
  const gid = `spark-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const { line, fill, resolved } = useMemo(() => {
    const nums = data.map((v) => (typeof v === "number" ? v : dec.toNumber(v)));
    const pts = sparkPoints(nums, width, height);
    const first = nums[0] ?? 0;
    const last = nums[nums.length - 1] ?? 0;
    const t: Exclude<SparklineTone, "auto"> = tone === "auto" ? (nums.length < 2 ? "neutral" : last >= first ? "up" : "down") : tone;
    const coords = pts.map(([x, y]) => `${tenth(x)},${tenth(y)}`);
    const lineStr = coords.join(" ");
    const fillStr = pts.length > 1 ? `M0,${height} L${coords.join(" L")} L${width},${height} Z` : "";
    return { line: lineStr, fill: fillStr, resolved: t };
  }, [data, width, height, tone]);

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      width={fluid ? "100%" : width}
      height={height}
      preserveAspectRatio="none"
      role={ariaLabel ? "img" : undefined}
      aria-label={ariaLabel}
      aria-hidden={ariaLabel ? undefined : true}
      className={cn("block shrink-0 overflow-visible", tones[resolved], className)}
    >
      {area && fill && (
        <>
          <defs>
            <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="currentColor" stopOpacity={0.28} />
              <stop offset="100%" stopColor="currentColor" stopOpacity={0} />
            </linearGradient>
          </defs>
          <path d={fill} fill={`url(#${gid})`} />
        </>
      )}
      {line && (
        <polyline
          points={line}
          fill="none"
          stroke="currentColor"
          strokeWidth={strokeWidth}
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      )}
    </svg>
  );
});
