import { formatPercent } from "@exchange/core";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { toneOf } from "../components/PriceText";
import { Skeleton } from "../components/Skeleton";
import { Tooltip } from "../components/Tooltip";
import { Sparkline } from "./Sparkline";

export type StatProps = {
  label: ReactNode;
  /** The figure: text, AmountText or a CountUp. */
  value: ReactNode;
  /** The unit after the figure ("USDT"). */
  unit?: ReactNode;
  /** A change as a fraction ("0.0123" → +1.23%), coloured by sign. */
  change?: string | null;
  /** Text before the change ("今日"). */
  changeLabel?: ReactNode;
  /** Values for a sparkline at the right. */
  sparkline?: readonly (string | number)[];
  /** A tip next to the label. */
  hint?: ReactNode;
  icon?: ReactNode;
  loading?: boolean;
  size?: "sm" | "md" | "lg";
  className?: string;
};

const valueSize = { sm: "text-md", md: "text-xl", lg: "text-2xl" };

/**
 * Stat is a key figure with its change and an optional sparkline (asset
 * cards, the console overview). While loading it shows skeletons of the
 * same size, so nothing moves when the numbers arrive.
 */
export function Stat({ label, value, unit, change, changeLabel, sparkline, hint, icon, loading, size = "md", className }: StatProps) {
  const tone = toneOf(change);
  return (
    <div className={cn("flex min-w-0 items-end justify-between gap-3", className)}>
      <div className="min-w-0">
        <div className="flex items-center gap-1.5 text-sm text-fg-3">
          {icon}
          {hint ? (
            <Tooltip content={hint}>
              <span tabIndex={0} className="cursor-help border-b border-dashed border-line-2">
                {label}
              </span>
            </Tooltip>
          ) : (
            <span>{label}</span>
          )}
        </div>
        {loading ? (
          <Skeleton className={cn("mt-2 w-32", size === "lg" ? "h-8" : size === "md" ? "h-6" : "h-5")} />
        ) : (
          <div className={cn("mt-1 flex items-baseline gap-1.5 font-semibold tabular-nums text-fg-1", valueSize[size])}>
            <span className="truncate">{value}</span>
            {unit && <span className="text-sm font-normal text-fg-3">{unit}</span>}
          </div>
        )}
        {change !== undefined && (
          <div className="mt-1 flex items-center gap-1.5 text-xs">
            {changeLabel && <span className="text-fg-3">{changeLabel}</span>}
            {loading ? (
              <Skeleton className="h-3 w-12" />
            ) : (
              <span className={cn("tabular-nums", tone === "up" ? "text-up" : tone === "down" ? "text-down" : "text-fg-2")}>{formatPercent(change)}</span>
            )}
          </div>
        )}
      </div>
      {sparkline && sparkline.length > 1 && !loading && <Sparkline data={sparkline} width={88} height={36} tone={change ? (tone === "neutral" ? "neutral" : tone) : "auto"} />}
    </div>
  );
}
