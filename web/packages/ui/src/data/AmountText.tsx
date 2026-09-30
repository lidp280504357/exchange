import { formatCompact, formatDecimal, useSettings } from "@exchange/core";
import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { toneOf } from "../components/PriceText";

export type AmountTextProps = {
  value: string | null | undefined;
  /** The asset's decimals; extra digits are cut, never rounded up. */
  decimals?: number;
  /** The unit after the number ("USDT"). */
  asset?: ReactNode;
  /** Force hiding (or showing); defaults to the eye toggle in the settings. */
  hidden?: boolean;
  /** 1.23万 / 4.56M for lists. */
  compact?: boolean;
  /** A leading + on gains (PnL). */
  sign?: boolean;
  /** Colour by sign ("auto") or a fixed tone. */
  tone?: "auto" | "up" | "down" | "muted";
  className?: string;
};

const toneClass = { up: "text-up", down: "text-down", neutral: "", muted: "text-fg-3" };

/** The mask shown for hidden amounts. */
export const HIDDEN_AMOUNT = "****";

/**
 * AmountText shows an amount at its asset's decimals (rounded down, so a
 * balance is never overstated) and hides it behind **** when the user
 * turned the eye off (settings.hideAmounts).
 */
export function AmountText({ value, decimals, asset, hidden, compact, sign, tone, className }: AmountTextProps) {
  const hideAll = useSettings((s) => s.hideAmounts);
  const locale = useSettings((s) => s.locale);
  const hide = hidden ?? hideAll;
  const text = hide
    ? HIDDEN_AMOUNT
    : compact
      ? formatCompact(value, locale)
      : formatDecimal(value, { decimals, rounding: "down", sign });
  const t = tone === "auto" ? (hide ? "neutral" : toneOf(value)) : (tone ?? "neutral");
  return (
    <span className={cn("tabular-nums", toneClass[t], className)}>
      {text}
      {asset !== undefined && asset !== null && <span className="ml-1 text-[0.9em] text-fg-3">{asset}</span>}
    </span>
  );
}
