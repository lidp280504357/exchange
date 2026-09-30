import { Slider as RSlider } from "radix-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";

export type SliderTone = "brand" | "up" | "down";

export type SliderProps = {
  value: number;
  onValueChange: (value: number) => void;
  /** Called once when a drag or key press ends. */
  onValueCommit?: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  /** Values marked with a dot on the track (e.g. 0/25/50/75/100). */
  marks?: number[];
  /** Shows the marks as clickable labels under the track. */
  markLabels?: boolean;
  formatMark?: (value: number) => ReactNode;
  /** The value read out by screen readers (aria-valuetext). */
  formatValue?: (value: number) => string;
  tone?: SliderTone;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
};

const tones: Record<SliderTone, { range: string; thumb: string; dot: string }> = {
  brand: { range: "bg-brand", thumb: "border-brand", dot: "border-brand bg-brand" },
  up: { range: "bg-up", thumb: "border-up", dot: "border-up bg-up" },
  down: { range: "bg-down", thumb: "border-down", dot: "border-down bg-down" },
};

/**
 * Slider is a single-thumb range (percent of a balance, leverage) on Radix
 * Slider: keyboard arrows, page keys, marks with dots and optional labels
 * that jump to exact values.
 */
export function Slider({
  value, onValueChange, onValueCommit, min = 0, max = 100, step = 1, marks = [], markLabels, formatMark, formatValue, tone = "brand",
  disabled, className, "aria-label": ariaLabel,
}: SliderProps) {
  const c = tones[tone];
  const span = max - min || 1;
  const pct = (v: number) => ((Math.min(max, Math.max(min, v)) - min) / span) * 100;
  return (
    <div className={cn("w-full select-none", className)}>
      <RSlider.Root
        value={[value]}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        onValueChange={(v) => onValueChange(v[0] ?? min)}
        onValueCommit={(v) => onValueCommit?.(v[0] ?? min)}
        className={cn("relative flex h-5 w-full touch-none items-center", disabled && "opacity-50")}
      >
        <RSlider.Track className="relative h-1 grow rounded-full bg-bg-3">
          <RSlider.Range className={cn("absolute h-full rounded-full", c.range)} />
        </RSlider.Track>
        {marks.map((m) => (
          <span
            key={m}
            aria-hidden
            className={cn(
              "pointer-events-none absolute top-1/2 size-2 -translate-x-1/2 -translate-y-1/2 rotate-45 rounded-[2px] border",
              value >= m ? c.dot : "border-line-2 bg-bg-1",
            )}
            style={{ left: `${pct(m)}%` }}
          />
        ))}
        <RSlider.Thumb
          aria-label={ariaLabel}
          aria-valuetext={formatValue?.(value)}
          className={cn(
            "block size-4 rounded-full border-2 bg-bg-1 shadow-pop transition-transform duration-[var(--t-fast)] hover:scale-110 focus-visible:scale-110",
            c.thumb,
          )}
        />
      </RSlider.Root>
      {markLabels && marks.length > 0 && (
        <div className="relative mt-1 h-4">
          {marks.map((m, i) => (
            <button
              key={m}
              type="button"
              disabled={disabled}
              onClick={() => {
                onValueChange(m);
                onValueCommit?.(m);
              }}
              className={cn(
                "absolute top-0 text-xs text-fg-3 hover:text-fg-1",
                i === 0 ? "translate-x-0" : i === marks.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
                value === m && "text-fg-1",
              )}
              style={{ left: `${pct(m)}%` }}
            >
              {formatMark ? formatMark(m) : m}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
