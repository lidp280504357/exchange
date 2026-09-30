import { Progress as RProgress } from "radix-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type ProgressTone = "brand" | "up" | "down" | "info" | "warn" | "danger" | "success";

export type ProgressProps = {
  /** 0..max; null shows an indeterminate bar (work of unknown length). */
  value: number | null;
  max?: number;
  tone?: ProgressTone;
  size?: "xs" | "sm" | "md";
  /** A label above the bar (e.g. "今日已用额度"). */
  label?: ReactNode;
  /** Text at the right of the label, e.g. "3,200 / 10,000 USDT". */
  valueText?: ReactNode;
  className?: string;
  "aria-label"?: string;
};

const tones: Record<ProgressTone, string> = {
  brand: "bg-brand", up: "bg-up", down: "bg-down", info: "bg-info", warn: "bg-warn", danger: "bg-danger", success: "bg-success",
};

// The indeterminate sweep: a gradient of the tone across the whole track.
const sweeps: Record<ProgressTone, string> = {
  brand: "via-brand", up: "via-up", down: "via-down", info: "via-info", warn: "via-warn", danger: "via-danger", success: "via-success",
};

const heights = { xs: "h-1", sm: "h-1.5", md: "h-2" };

/**
 * Progress is a bar for limits used, confirmations and uploads. The fill
 * moves with a transform (never its width), so it animates without layout.
 */
export function Progress({ value, max = 100, tone = "brand", size = "sm", label, valueText, className, "aria-label": ariaLabel }: ProgressProps) {
  const pct = value === null ? null : Math.min(100, Math.max(0, (value / (max || 1)) * 100));
  return (
    <div className={cn("w-full", className)}>
      {(label || valueText) && (
        <div className="mb-1.5 flex items-center justify-between gap-2 text-xs">
          <span className="text-fg-3">{label}</span>
          <span className="tabular-nums text-fg-2">{valueText}</span>
        </div>
      )}
      <RProgress.Root
        value={value === null ? null : Math.min(max, Math.max(0, value))}
        max={max}
        aria-label={ariaLabel ?? (typeof label === "string" ? label : undefined)}
        className={cn("relative w-full overflow-hidden rounded-full bg-bg-3", heights[size])}
      >
        {pct === null ? (
          <span className={cn("absolute inset-0 animate-shimmer bg-gradient-to-r from-transparent to-transparent", sweeps[tone])} />
        ) : (
          <RProgress.Indicator
            className={cn("h-full w-full rounded-full transition-transform duration-[var(--t-slow)] ease-out", tones[tone])}
            style={{ transform: `translateX(-${100 - pct}%)` }}
          />
        )}
      </RProgress.Root>
    </div>
  );
}
