import { dec } from "@exchange/core";
import { cn } from "../lib/cn";

export type DepthBarsProps = {
  /** The level's amount (or running total) and the largest one shown. */
  value?: string | number;
  max?: string | number;
  /** Or the fraction directly, 0..1 (the order book computes it once). */
  ratio?: number;
  side: "buy" | "sell";
  /** The edge the bar grows from (default right, like the order book). */
  align?: "left" | "right";
  className?: string;
};

/** depthRatio is value / max clamped to 0..1, for drawing. */
export function depthRatio(value: string | number | undefined, max: string | number | undefined): number {
  const v = typeof value === "number" ? value : dec.toNumber(value);
  const m = typeof max === "number" ? max : dec.toNumber(max);
  if (!(m > 0) || !(v > 0)) return 0;
  return Math.min(1, v / m);
}

/**
 * DepthBars is the depth bar behind an order book row: an absolutely
 * placed span scaled on X, green for bids and red for asks. Put it in a
 * relative parent. It does not ease: the depth changes ten times a second,
 * and bars always sliding read as flicker.
 */
export function DepthBars({ value, max, ratio, side, align = "right", className }: DepthBarsProps) {
  const r = ratio ?? depthRatio(value, max);
  return (
    <span
      aria-hidden
      className={cn(
        "pointer-events-none absolute inset-y-0 w-full",
        align === "right" ? "right-0 origin-right" : "left-0 origin-left",
        side === "buy" ? "bg-up/15" : "bg-down/15",
        className,
      )}
      style={{ transform: `scaleX(${Math.min(1, Math.max(0, r))})` }}
    />
  );
}
