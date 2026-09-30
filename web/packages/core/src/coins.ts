import type { Locale } from "./settings/store";

// Coin profiles for the coin pages (design §3 #11, §8.7): names, a short
// introduction and links, kept in the repository (assets/coins/*.json) and
// maintained by hand after a one-off generation; never fetched at run time.

export type CoinProfile = {
  symbol: string;
  name: Record<Locale, string>;
  intro: Record<Locale, string>;
  links: { website?: string; explorer?: string; whitepaper?: string };
  /** A palette name for the letter icon when there is no logo. */
  color: string;
  /** When the profile was written. */
  asOf: string;
};

const files = import.meta.glob<CoinProfile>("../assets/coins/*.json", { eager: true, import: "default" });

const profiles = new Map<string, CoinProfile>(Object.values(files).map((p) => [p.symbol, p]));

/** coinProfile returns a coin's profile; "1000PEPE" finds PEPE's. */
export function coinProfile(symbol: string): CoinProfile | undefined {
  return profiles.get(symbol) ?? profiles.get(symbol.replace(/^1000+/, ""));
}

/** coinName returns a coin's name in a language, or its symbol. */
export function coinName(symbol: string, locale: Locale): string {
  return coinProfile(symbol)?.name[locale] ?? symbol;
}
