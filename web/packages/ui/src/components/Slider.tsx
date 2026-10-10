import { Slider as RSlider } from "radix-ui";
import { type CSSProperties, type ReactNode, useEffect, useRef, useState } from "react";
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
   * or typed there), not again until it has left max; one soft glow instead
   * under prefers-reduced-motion. On by default (the order forms); the
   * leverage dialog turns it off.
   */
  pulseAtMax?: boolean;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
};

const tones: Record<SliderTone, { text: string; thumb: string; dot: string }> = {
  brand: { text: "text-brand", thumb: "border-brand", dot: "border-brand bg-brand" },
  up: { text: "text-up", thumb: "border-up", dot: "border-up bg-up" },
  down: { text: "text-down", thumb: "border-down", dot: "border-down bg-down" },
};

// The arrival at max (B176, the user 2026-10-10: an energy bar's glow, as
// in Blade & Soul, in place of B173's sparks; B194: bigger and brighter,
// white-hot cores in a glow of the tone): about 1.5 s of light, transform
// and opacity only, never in the pointer's way. The layers, their
// keyframes and what plays when are in styles/theme.css; the timings that
// depend on the marks are set here.
const LEAD = 120; // the track's flash, white cooling to the tone, before the charge
const CHARGE = 200; // the glow reaches the end, which flares
const RUN = 760; // the bands' run back to the start, done by about 1100 ms
const MARK_GLOW = 480; // a mark's glow, brightest at 35% as the first band passes
// The second band follows the first a little smaller and fainter, rising
// where the first falls.
const BANDS = [
  { wave: "a", lag: 0, scale: "1", opacity: 1 },
  { wave: "b", lag: 90, scale: "0.8", opacity: 0.75 },
] as const;
// Sparks: flung off the flare at the end (x 100%), never further past it
// than the clip, or shed by the first band halfway (x 50%), to (dx, dy)
// px; size px, duration and delay ms after the flash.
const SPARKS = [
  { x: 100, dx: -34, dy: -18, size: 12, dur: 560, delay: 185 },
  { x: 100, dx: -18, dy: -27, size: 10, dur: 500, delay: 195 },
  { x: 100, dx: 5, dy: -22, size: 10, dur: 460, delay: 190 },
  { x: 100, dx: -48, dy: -4, size: 14, dur: 640, delay: 200 },
  { x: 100, dx: -40, dy: 14, size: 12, dur: 600, delay: 192 },
  { x: 100, dx: -16, dy: 25, size: 10, dur: 520, delay: 205 },
  { x: 100, dx: 5, dy: 20, size: 10, dur: 470, delay: 198 },
  { x: 100, dx: -28, dy: 3, size: 12, dur: 700, delay: 230 },
  { x: 50, dx: -8, dy: -16, size: 10, dur: 560, delay: CHARGE + RUN / 2 },
  { x: 50, dx: 6, dy: 15, size: 10, dur: 560, delay: CHARGE + RUN / 2 + 40 },
];
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
  // unmounted when it has played (or on leaving), so it plays once per
  // arrival; a value already at max when shown plays nothing (B183).
  // Within half a step of max is max: a form whose balance is no whole
  // number of lots settles at 99.99% once a drag to 100% ends (①). Only an
  // arrival the user made plays - a drag, a key, a mark's label - not one
  // a live price or balance brings (②).
  const reached = (m: number) => value >= m - step / 2;
  const atMax = reached(max);
  const wasAtMax = useRef(atMax);
  const touched = useRef(false);
  const [peaks, setPeaks] = useState(0);
  const [played, setPlayed] = useState(0);
  // After every render: a touch counts for the render it caused only; an
  // arrival whose effect is not showing - cut short by disabled, by
  // pulseAtMax off or by leaving max - counts as played, so it does not
  // come back with them (B192).
  useEffect(() => {
    if (atMax && !wasAtMax.current && touched.current && pulseAtMax && !disabled) setPeaks((n) => n + 1);
    else if (peaks > played && !(atMax && pulseAtMax && !disabled)) setPlayed(peaks);
    wasAtMax.current = atMax;
    touched.current = false;
  });
  const change = (v: number) => {
    touched.current = true;
    onValueChange(v);
  };
  // Once played it is gone (④), so turning disabled or pulseAtMax off and
  // on at max does not play it again (③).
  const peaking = atMax && peaks > played && pulseAtMax && !disabled;
  return (
    <div className={cn("w-full select-none", className)}>
      <RSlider.Root
        value={[value]}
        min={min}
        max={max}
        step={step}
        disabled={disabled}
        onValueChange={(v) => change(v[0] ?? min)}
        onValueCommit={(v) => onValueCommit?.(v[0] ?? min)}
        className={cn("relative mx-2 flex h-5 touch-none items-center", disabled && "opacity-50")}
      >
        <RSlider.Track className="relative h-1.5 grow rounded-full bg-bg-3">
          <RSlider.Range className={cn("slider-fill absolute h-full rounded-full", c.text)} />
        </RSlider.Track>
        {marks.map((m) => (
          <span
            key={m}
            aria-hidden
            className={cn(
              "pointer-events-none absolute top-1/2 size-2 -translate-x-1/2 -translate-y-1/2 rotate-45 rounded-[2px] border",
              reached(m) ? c.dot : "border-line-2 bg-bg-1",
            )}
            style={{ left: `${pct(m)}%` }}
          />
        ))}
        {peaking && (
          // The light is clipped a little past the track's ends, so it
          // never widens the panel the slider sits in (B194: theme.css).
          <span
            key={peaks}
            aria-hidden
            className="slider-fx-clip pointer-events-none absolute"
            onAnimationEnd={(e) => {
              // The outer glow ends last (its soft fade under reduced motion).
              if (e.animationName === "slider-glow-outer" || e.animationName === "slider-soft-glow") setPlayed(peaks);
            }}
          >
            <span className={cn("slider-fx absolute", c.text)}>
              <span className="slider-glow-2" />
              <span className="slider-thumb-halo" />
              <span className="slider-glow-1" />
              <span className="slider-fx-motion">
                <span className="slider-flash-glow" />
                <span className="slider-flash" />
                {marks
                  .filter((m) => m < max)
                  .map((m) => (
                    <span
                      key={m}
                      className="slider-mark-glow"
                      style={{
                        left: `${pct(m)}%`,
                        animation: `slider-mark-glow ${ms(MARK_GLOW)} ease-out ${ms(LEAD + CHARGE + RUN * (1 - pct(m) / 100) - MARK_GLOW * 0.35)} both`,
                      }}
                    />
                  ))}
                <span className="slider-bands">
                  {BANDS.map((b) => (
                    <span
                      key={b.wave}
                      className="slider-band"
                      style={{
                        animation: `slider-band-x ${ms(RUN)} linear ${ms(LEAD + CHARGE + b.lag)} both, slider-band-fade ${ms(RUN)} linear ${ms(LEAD + CHARGE + b.lag)} both`,
                      }}
                    >
                      <span
                        className="absolute top-0 left-0"
                        style={{ scale: b.scale, opacity: b.opacity, animation: `slider-wave-${b.wave} ${ms(RUN)} ease-in-out ${ms(LEAD + CHARGE + b.lag)} both` }}
                      >
                        <span className="slider-band-trail" />
                        <span className="slider-band-head" />
                      </span>
                    </span>
                  ))}
                </span>
                <span className="slider-burst-bloom" />
                <span className="slider-burst-streak" />
                <span className="slider-burst-ray" />
                <span className="slider-burst" />
                {SPARKS.map((p, i) => (
                  <span
                    key={i}
                    className="slider-spark"
                    style={
                      {
                        left: `${p.x}%`,
                        width: p.size,
                        height: p.size,
                        margin: `${-p.size / 2}px 0 0 ${-p.size / 2}px`,
                        "--dx": `${p.dx}px`,
                        "--dy": `${p.dy}px`,
                        animation: `slider-spark-fly ${ms(p.dur)} cubic-bezier(0.15, 0.75, 0.35, 1) ${ms(LEAD + p.delay)} both`,
                      } as CSSProperties
                    }
                  />
                ))}
              </span>
            </span>
          </span>
        )}
        <RSlider.Thumb
          aria-label={ariaLabel}
          aria-valuetext={formatValue?.(value)}
          className="group relative block size-0 outline-none"
        >
          <span
            aria-hidden
            className={cn(
              "absolute top-0 left-0 block size-4 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 bg-bg-1 shadow-pop",
              "transition-transform duration-[var(--t-fast)] group-hover:scale-110 group-focus-visible:scale-110",
              // The thumb that takes the focus has no size: the dot shows it (B175).
              "group-focus-visible:ring-2 group-focus-visible:ring-brand group-focus-visible:ring-offset-2 group-focus-visible:ring-offset-bg-1",
              peaking && "slider-dot-burst",
              c.thumb,
            )}
          >
            {/* The dot is drawn over the effect: it flares white-hot itself, the flare's core (B194). */}
            {peaking && <span key={peaks} className={cn("slider-dot-flash", c.text)} />}
          </span>
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
                  change(m);
                  onValueCommit?.(m);
                }}
                className={cn(
                  "absolute top-0 text-xs whitespace-nowrap text-fg-3 hover:text-fg-1",
                  first ? "translate-x-0" : last ? "-translate-x-full" : "-translate-x-1/2",
                  Math.abs(value - m) < step / 2 && "text-fg-1",
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
