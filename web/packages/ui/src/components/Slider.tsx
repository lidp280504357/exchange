import { Slider as RSlider } from "radix-ui";
import { type ReactNode, useEffect, useRef, useState } from "react";
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
  /**
   * Plays the arrival effect once as the value reaches max (dragged, clicked
   * or typed there), not again until it has left max; a still halo instead
   * under prefers-reduced-motion. On by default (the order forms); the
   * leverage dialog turns it off.
   */
  pulseAtMax?: boolean;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
};

const tones: Record<SliderTone, { range: string; text: string; thumb: string; dot: string; glow: string }> = {
  brand: { range: "bg-brand", text: "text-brand", thumb: "border-brand", dot: "border-brand bg-brand", glow: "bg-brand" },
  up: { range: "bg-up", text: "text-up", thumb: "border-up", dot: "border-up bg-up", glow: "bg-up" },
  down: { range: "bg-down", text: "text-down", thumb: "border-down", dot: "border-down bg-down", glow: "bg-down" },
};

// The arrival at max (B173; the user 2026-10-10): about 1.1 s of transform
// and opacity only, never in the pointer's way. The dot pulses with a halo
// (PULSE ms); two sparks then run back from the end to the start (RUN ms)
// on sine paths half a period apart, one above the track and one below,
// crossing it at every quarter (the marks), each with a fading trail of
// ghosts GAP ms behind; each mark flashes as they cross it, and a bright
// band sweeps the fill with them and is gone FADE ms after. Keyframes in
// styles/theme.css.
const PULSE = 250;
const RUN = 700;
const FADE = 300;
const GAP = 26;
const GHOSTS = [0, 1, 2, 3, 4];
const FLASH = 260; // a mark's flash, brightest at 35%
const ms = (n: number) => `${Math.round(n)}ms`;

/**
 * Slider is a single-thumb range (percent of a balance, leverage) on Radix
 * Slider: keyboard arrows, page keys, marks with dots and optional labels
 * that jump to exact values.
 *
 * The thumb's centre is the value, as the fill's end and the marks are
 * (B173): Radix keeps a thumb inside the track by shifting it up to half
 * its width, so at 0% a dot's left edge, not its centre, sat on the
 * track's start. The thumb Radix measures is a zero-size point and the dot
 * is drawn around it; the track is inset by the dot's radius (0.5rem, mx-2)
 * so the half of the dot past either end stays inside the component.
 */
export function Slider({
  value, onValueChange, onValueCommit, min = 0, max = 100, step = 1, marks = [], markLabels, formatMark, formatValue, tone = "brand",
  pulseAtMax = true, disabled, className, "aria-label": ariaLabel,
}: SliderProps) {
  const c = tones[tone];
  const span = max - min || 1;
  const pct = (v: number) => ((Math.min(max, Math.max(min, v)) - min) / span) * 100;
  // Counts the arrivals at max: the effect is mounted afresh on each and
  // unmounted on leaving, so it plays once per arrival; a value already at
  // max when shown plays nothing.
  const atMax = value >= max;
  const wasAtMax = useRef(atMax);
  const [peaks, setPeaks] = useState(0);
  useEffect(() => {
    if (atMax && !wasAtMax.current && pulseAtMax && !disabled) setPeaks((n) => n + 1);
    wasAtMax.current = atMax;
  }, [atMax, pulseAtMax, disabled]);
  const peaking = atMax && peaks > 0 && pulseAtMax && !disabled;
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
        className={cn("relative mx-2 flex h-5 touch-none items-center", disabled && "opacity-50")}
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
        {peaking && (
          <span key={peaks} aria-hidden className={cn("pointer-events-none absolute inset-0 motion-reduce:hidden", c.text)}>
            <span className="absolute inset-x-0 top-1/2 h-1 -translate-y-1/2 overflow-hidden rounded-full">
              <span
                className="slider-sweep absolute inset-y-0 left-0 w-full"
                style={{ animation: `slider-sweep-x ${ms(RUN)} linear ${ms(PULSE)} both, slider-fade-out ${ms(FADE)} ease-out ${ms(PULSE + RUN)} forwards` }}
              />
            </span>
            {marks
              .filter((m) => m < max)
              .map((m) => (
                <span
                  key={m}
                  className="absolute top-1/2 size-2 -translate-x-1/2 -translate-y-1/2 rotate-45 rounded-[2px] bg-current opacity-0"
                  style={{ left: `${pct(m)}%`, animation: `slider-mark-flash ${ms(FLASH)} ease-out ${ms(PULSE + RUN * (1 - pct(m) / 100) - FLASH * 0.35)} both` }}
                />
              ))}
            {(["up", "down"] as const).map((dir) =>
              GHOSTS.map((k) => (
                <span
                  key={`${dir}${k}`}
                  className="absolute inset-x-0 top-1/2 h-0"
                  style={{ opacity: 1 - k * 0.19, animation: `slider-spark-x ${ms(RUN)} linear ${ms(PULSE + k * GAP)} both` }}
                >
                  <span
                    className="absolute top-0 left-0 block size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-current shadow-pop"
                    style={{
                      scale: String(1 - k * 0.12),
                      animation: `slider-spark-${dir} ${ms(RUN)} linear ${ms(PULSE + k * GAP)} both, slider-spark-fade ${ms(RUN)} linear ${ms(PULSE + k * GAP)} both`,
                    }}
                  />
                </span>
              )),
            )}
          </span>
        )}
        <RSlider.Thumb
          aria-label={ariaLabel}
          aria-valuetext={formatValue?.(value)}
          className="group relative block size-0 outline-none"
        >
          {peaking && (
            <span
              key={peaks}
              aria-hidden
              className={cn(
                "pointer-events-none absolute top-0 left-0 size-4 -translate-x-1/2 -translate-y-1/2 rounded-full opacity-0 motion-safe:animate-slider-peak motion-reduce:opacity-30",
                c.glow,
              )}
            />
          )}
          <span
            aria-hidden
            className={cn(
              "absolute top-0 left-0 block size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 bg-bg-1 shadow-pop",
              "transition-transform duration-[var(--t-fast)] group-hover:scale-110 group-focus-visible:scale-110",
              peaking && "motion-safe:animate-slider-dot-pulse",
              c.thumb,
            )}
          />
        </RSlider.Thumb>
      </RSlider.Root>
      {markLabels && marks.length > 0 && (
        <div className="relative mx-2 mt-1 h-4">
          {marks.map((m, i) => {
            // The ends line up with the dot's outer edges (the dot's radius
            // past the track), the others are centred under their mark.
            const first = i === 0;
            const last = i === marks.length - 1;
            return (
              <button
                key={m}
                type="button"
                disabled={disabled}
                onClick={() => {
                  onValueChange(m);
                  onValueCommit?.(m);
                }}
                className={cn(
                  "absolute top-0 text-xs whitespace-nowrap text-fg-3 hover:text-fg-1",
                  first ? "translate-x-0" : last ? "-translate-x-full" : "-translate-x-1/2",
                  value === m && "text-fg-1",
                )}
                style={{ left: first ? `calc(${pct(m)}% - 0.5rem)` : last ? `calc(${pct(m)}% + 0.5rem)` : `${pct(m)}%` }}
              >
                {formatMark ? formatMark(m) : m}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
