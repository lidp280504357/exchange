import { dec, formatPrice } from "@exchange/core";
import { useRef } from "react";
import { cn } from "../lib/cn";

export type Tone = "up" | "down" | "neutral";

export type PriceTextProps = {
  value: string | null | undefined;
  /** The pair's price decimals. */
  decimals?: number;
  /** The colour: up, down, or from the sign of `change` when given. */
  tone?: Tone;
  change?: string | null;
  /** Flash green or red when the value rises or falls (default on). */
  flash?: boolean;
  className?: string;
};

/** toneOf returns the tone of a signed decimal (a change, a PnL). */
export function toneOf(v: string | null | undefined): Tone {
  if (!v || !dec.isDecimal(v)) return "neutral";
  const s = dec.sign(v);
  return s > 0 ? "up" : s < 0 ? "down" : "neutral";
}

const toneClass: Record<Tone, string> = { up: "text-up", down: "text-down", neutral: "text-fg-1" };

/**
 * PriceText shows a price at the pair's decimals, coloured by direction,
 * and flashes its background for 400 ms when it moves (a CSS animation
 * restarted by remounting the span, no timers).
 */
export function PriceText({ value, decimals, tone, change, flash = true, className }: PriceTextProps) {
  const prev = useRef<string | null | undefined>(value);
  const gen = useRef(0);
  const dir = useRef<"up" | "down" | null>(null);
  if (value !== prev.current) {
    if (flash && value && prev.current && dec.isDecimal(value) && dec.isDecimal(prev.current)) {
      const c = dec.cmp(value, prev.current);
      if (c !== 0) {
        dir.current = c > 0 ? "up" : "down";
        gen.current++;
      }
    }
    prev.current = value;
  }
  const t = tone ?? (change !== undefined ? toneOf(change) : "neutral");
  return (
    <span
      key={gen.current}
      className={cn(
        "rounded-1 tabular-nums",
        toneClass[t],
        dir.current === "up" && gen.current > 0 && "animate-flash-up",
        dir.current === "down" && gen.current > 0 && "animate-flash-down",
        className,
      )}
    >
      {formatPrice(value, decimals)}
    </span>
  );
}
