import { dec, formatPrice } from "@exchange/core";
import { cn } from "../lib/cn";
import { FlashLayer, useFlash } from "../lib/useFlash";

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
 * and flashes its background for 400 ms when it moves: a CSS animation on
 * a layer behind the text, restarted by remounting that layer (no
 * timers). The text itself stays the same element, so a moving price is
 * not a new largest paint to Lighthouse every time it changes.
 */
export function PriceText({ value, decimals, tone, change, flash = true, className }: PriceTextProps) {
  const moved = useFlash(value, flash);
  const t = tone ?? (change !== undefined ? toneOf(change) : "neutral");
  return (
    <span className={cn("relative isolate rounded-1 tabular-nums", toneClass[t], className)}>
      <FlashLayer flash={moved} />
      {formatPrice(value, decimals)}
    </span>
  );
}
