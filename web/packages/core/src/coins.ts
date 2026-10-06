import { coinsZhTW } from "./coins.zh-TW";
import { apiProfile } from "./markets/profiles";
import type { Locale } from "./settings/store";

// Coin profiles for the coin pages (design §3 #11, §8.7): names, a short
// introduction and links, kept in the repository (assets/coins/*.json) and
// maintained by hand after a one-off generation. What operators set through
// the API (ASTRA design §5.3: display name, introductions, links, logo)
// goes over them as the market queries bring it (markets/profiles). The
// Traditional Chinese names and introductions are generated from the
// Simplified ones (coins.zh-TW.ts, scripts/gen-zh-tw.mjs).

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

/** A profile as kept in assets/coins: Simplified Chinese and English. */
type CoinFile = Omit<CoinProfile, "name" | "intro"> & { name: Record<"zh-CN" | "en", string>; intro: Record<"zh-CN" | "en", string> };

const files = import.meta.glob<CoinFile>("../assets/coins/*.json", { eager: true, import: "default" });

const profiles = new Map<string, CoinProfile>(
  Object.values(files).map((p) => {
    const tw = coinsZhTW[p.symbol];
    return [p.symbol, { ...p, name: { ...p.name, "zh-TW": tw?.name ?? p.name["zh-CN"] }, intro: { ...p.intro, "zh-TW": tw?.intro ?? p.intro["zh-CN"] } }];
  }),
);

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
    name: api.name ? { "zh-CN": api.name, "zh-TW": api.name, en: api.name } : (own?.name ?? { "zh-CN": symbol, "zh-TW": symbol, en: symbol }),
    intro: introOf(api.intro, own?.intro),
    links: api.links ?? own?.links ?? {},
    color: own?.color ?? "",
    asOf: own?.asOf ?? "",
    logo: api.logo,
  };
}

/**
 * introOf lays the introductions operators wrote over the repository's.
 * A Traditional one they left empty shows their Simplified one (design
 * 2026-10-06 繁体中文 §2.4), the repository's only when they wrote no
 * Chinese at all: theirs is the newer.
 */
function introOf(api: Partial<Record<Locale, string>> | undefined, own: Record<Locale, string> | undefined): Record<Locale, string> {
  const zh = api?.["zh-CN"];
  return {
    "zh-CN": zh ?? own?.["zh-CN"] ?? "",
    "zh-TW": api?.["zh-TW"] ?? zh ?? own?.["zh-TW"] ?? "",
    en: api?.en ?? own?.en ?? "",
  };
}

/** coinName returns a coin's name in a language, or its symbol. */
export function coinName(symbol: string, locale: Locale): string {
  return coinProfile(symbol)?.name[locale] ?? symbol;
}
