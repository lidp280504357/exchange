import { coinProfile, useCoinLogo } from "@exchange/core";
import { Avatar as RAvatar } from "radix-ui";
import { cn } from "../lib/cn";
import { identityClass } from "../lib/identity";

export type CoinIconProps = {
  /** A coin symbol: "BTC", "1000PEPE" (the multiplier is ignored). */
  symbol: string;
  /** Diameter in px (default 24). */
  size?: number;
  /** A logo; the letter icon shows until it loads or when it fails. */
  src?: string;
  /** Makes the icon an image with this name (default: decorative). */
  label?: string;
  className?: string;
};

/** coinBase strips a low-price multiplier: "1000PEPE" → "PEPE" (ADR-0014). */
export function coinBase(symbol: string): string {
  return symbol.replace(/^1000+/, "") || symbol;
}

/**
 * CoinIcon is a coin's round icon: its logo (the one given, else the one
 * operators uploaded, which the market queries bring), otherwise its first
 * letter on a colour that is stable per symbol (the coin profile's palette
 * name when it has one).
 */
export function CoinIcon({ symbol, size = 24, src, label, className }: CoinIconProps) {
  const base = coinBase(symbol.toUpperCase());
  const uploaded = useCoinLogo(symbol.toUpperCase(), base);
  const logo = src ?? uploaded;
  const letter = base.charAt(0) || "?";
  const color = identityClass(base, coinProfile(base)?.color);
  return (
    <RAvatar.Root
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      className={cn("relative inline-flex shrink-0 select-none items-center justify-center overflow-hidden rounded-full align-middle", className)}
      style={{ width: size, height: size }}
    >
      {logo && <RAvatar.Image src={logo} alt="" className="size-full object-cover" />}
      <RAvatar.Fallback
        delayMs={logo ? 250 : undefined}
        className={cn("flex size-full items-center justify-center font-semibold leading-none text-white", color)}
        style={{ fontSize: Math.max(9, Math.round(size * 0.46)) }}
      >
        {letter}
      </RAvatar.Fallback>
    </RAvatar.Root>
  );
}
