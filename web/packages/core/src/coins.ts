import { apiProfile } from "./markets/profiles";
import type { Locale } from "./settings/store";

// Coin profiles for the coin pages (design §3 #11, §8.7): names, a short
// introduction and links, kept in the repository (assets/coins/*.json) and
// maintained by hand after a one-off generation. What operators set through
// the API (ASTRA design §5.3: display name, introductions, links, logo)
// goes over them as the market queries bring it (markets/profiles).

export type CoinProfile = {
  symbol: string;
  name: Record<Locale, string>;
  intro: Record<Locale, string>;
  links: { website?: string; explorer?: string; whitepaper?: string };
  /** A palette name for the letter icon when there is no logo. */
  color: string;
  /** When the profile was written. */
  asOf: string;
  /** The logo operators uploaded (a URL), if any. */
  logo?: string;
};

const files = import.meta.glob<CoinProfile>("../assets/coins/*.json", { eager: true, import: "default" });

const profiles = new Map<string, CoinProfile>(Object.values(files).map((p) => [p.symbol, p]));

/**
 * coinProfile returns a coin's profile ("1000PEPE" finds PEPE's), with
 * what operators set through the API laid over it.
 */
export function coinProfile(symbol: string): CoinProfile | undefined {
  const own = profiles.get(symbol) ?? profiles.get(symbol.replace(/^1000+/, ""));
  const api = apiProfile(symbol);
  if (!api || (!api.name && !api.intro && !api.links && !api.logo)) return own;
  return {
    symbol: own?.symbol ?? symbol,
    name: api.name ? { "zh-CN": api.name, en: api.name } : (own?.name ?? { "zh-CN": symbol, en: symbol }),
    intro: { "zh-CN": api.intro?.["zh-CN"] ?? own?.intro["zh-CN"] ?? "", en: api.intro?.en ?? own?.intro.en ?? "" },
    links: api.links ?? own?.links ?? {},
    color: own?.color ?? "",
    asOf: own?.asOf ?? "",
    logo: api.logo,
  };
}

/** coinName returns a coin's name in a language, or its symbol. */
export function coinName(symbol: string, locale: Locale): string {
  return coinProfile(symbol)?.name[locale] ?? symbol;
}
