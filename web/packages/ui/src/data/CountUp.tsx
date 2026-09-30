import { dec, formatDecimal } from "@exchange/core";
import { useReducedMotion } from "motion/react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../lib/cn";

export type CountUpProps = {
  /** The target: a decimal string. */
  value: string | null | undefined;
  /** Decimals shown on every frame (the number does not jitter in width). */
  decimals: number;
  /** Animation length in ms (default 600). */
  duration?: number;
  /** Thousands separators (default on). */
  group?: boolean;
  prefix?: ReactNode;
  suffix?: ReactNode;
  className?: string;
};

/**
 * interpolate returns from + (to − from) × progress, exactly, at the given
 * decimals; progress is a 0..1 float quantized to 1/10000 (drawing only).
 */
export function interpolate(from: string, to: string, progress: number, decimals: number): string {
  if (progress <= 0) return from;
  if (progress >= 1) return to;
  const p = dec.div(String(Math.round(progress * 10000)), "10000", 4);
  return dec.add(from, dec.round(dec.mul(dec.sub(to, from), p), decimals, "half"));
}

const easeOut = (t: number) => 1 - (1 - t) ** 3;

/**
 * CountUp rolls a number from its previous value to the new one over
 * ~600 ms (the home page stats, total assets after a transfer), with
 * requestAnimationFrame and exact decimal steps. Reduced motion jumps
 * straight to the value; screen readers only get the final value.
 */
export function CountUp({ value, decimals, duration = 600, group = true, prefix, suffix, className }: CountUpProps) {
  const reduced = useReducedMotion();
  const [shown, setShown] = useState(value);
  const shownRef = useRef(value);

  useEffect(() => {
    const from = shownRef.current;
    const set = (v: string | null | undefined) => {
      shownRef.current = v;
      setShown(v);
    };
    if (reduced || !value || !from || !dec.isDecimal(value) || !dec.isDecimal(from) || value === from || typeof requestAnimationFrame !== "function") {
      set(value);
      return;
    }
    let raf = 0;
    const start = performance.now();
    const frame = (now: number) => {
      const t = Math.min(1, (now - start) / duration);
      set(t >= 1 ? value : interpolate(from, value, easeOut(t), decimals));
      if (t < 1) raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);
    return () => cancelAnimationFrame(raf);
  }, [value, decimals, duration, reduced]);

  const fmt = (v: string | null | undefined) => formatDecimal(v, { decimals, rounding: "half", group });
  return (
    <span className={cn("tabular-nums", className)}>
      {prefix}
      <span aria-hidden>{fmt(shown)}</span>
      <span className="sr-only">{fmt(value)}</span>
      {suffix}
    </span>
  );
}
