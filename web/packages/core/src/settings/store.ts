import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { ContractUnit } from "../trading/coinMargined";

// User preferences kept on the device (design §6.2 account settings):
// language, time zone, the colour of rises, order confirmations and small
// balances. Applying them to the page (html attributes) is applySettings.

export type Locale = "zh-CN" | "zh-TW" | "en";
/** The sites' languages, in menu order. */
export const LOCALES: readonly Locale[] = ["zh-CN", "zh-TW", "en"];
/** Each language's name in itself, the same whatever the page's language. */
export const LOCALE_NAMES: Record<Locale, string> = { "zh-CN": "简体中文", "zh-TW": "繁體中文", en: "English" };
/** Each language's name in English, beside its own in the pickers. */
export const LOCALE_ENGLISH_NAMES: Record<Locale, string> = { "zh-CN": "Simplified Chinese", "zh-TW": "Traditional Chinese", en: "English" };

/** A site language with its names: the one list every language picker shows (F30). */
export type Language = { locale: Locale; name: string; english: string };
export const LANGUAGES: readonly Language[] = LOCALES.map((locale) => ({ locale, name: LOCALE_NAMES[locale], english: LOCALE_ENGLISH_NAMES[locale] }));
/** green-up: green rises, red falls (default); red-up swaps them. */
export type UpDown = "green-up" | "red-up";

export type Settings = {
  locale: Locale;
  /**
   * Whether the user chose the language. Until they do, it follows the
   * browser's preferences, then the platform's fallback language (F30).
   */
  localeChosen: boolean;
  /** The platform's fallback language (its profile's default_locale) as last read: English until then. */
  fallbackLocale: Locale;
  /** An IANA zone such as Asia/Shanghai; "" follows the browser. */
  timeZone: string;
  upDown: UpDown;
  confirmOrders: boolean;
  hideSmallBalances: boolean;
  /** Amounts hidden behind asterisks (the eye toggle on the assets card). */
  hideAmounts: boolean;
  /** What a coin-margined order's amount is typed in (B130): contracts by default. */
  contractUnit: ContractUnit;
};

type SettingsState = Settings & {
  set: (patch: Partial<Settings>) => void;
};

/**
 * matchLocale maps a browser language tag to the site language for it, or
 * null when the site has none: Traditional Chinese for zh-Hant and Taiwan,
 * Hong Kong and Macao (design 2026-10-06 繁体中文 §2.1), Simplified for the
 * other Chinese, English for any English.
 */
export function matchLocale(tag: string): Locale | null {
  const t = tag.trim().toLowerCase();
  if (t === "en" || t.startsWith("en-")) return "en";
  if (t !== "zh" && !t.startsWith("zh-")) return null;
  if (t.includes("-hant")) return "zh-TW";
  if (t.includes("-hans")) return "zh-CN";
  return /^zh-(tw|hk|mo)\b/.test(t) ? "zh-TW" : "zh-CN";
}

/** localeOf maps a browser language tag to a site language, English for one the site does not have. */
export function localeOf(tag: string): Locale {
  return matchLocale(tag) ?? "en";
}

/**
 * negotiateLocale picks the language of a visitor who has not chosen one
 * (F30): the first of the browser's preferences (most wanted first) the
 * site has, else the platform's fallback language.
 */
export function negotiateLocale(tags: readonly string[], fallback: Locale): Locale {
  for (const tag of tags) {
    const locale = matchLocale(tag);
    if (locale) return locale;
  }
  return fallback;
}

/** browserTags lists the browser's language preferences, most wanted first. */
export function browserTags(): readonly string[] {
  const nav = globalThis.navigator;
  if (nav?.languages?.length) return nav.languages;
  return nav?.language ? [nav.language] : [];
}

/**
 * restoredLocale is the language a page starts in: the user's choice, kept
 * on the device; else negotiated afresh, the browser's preferences may
 * have changed since (F30).
 */
export function restoredLocale(saved: Partial<Pick<Settings, "locale" | "localeChosen" | "fallbackLocale">>, tags: readonly string[]): Locale {
  if (saved.localeChosen && saved.locale) return saved.locale;
  return negotiateLocale(tags, saved.fallbackLocale ?? "en");
}

export const useSettings = create<SettingsState>()(
  persist(
    (set) => ({
      locale: negotiateLocale(browserTags(), "en"),
      localeChosen: false,
      fallbackLocale: "en",
      timeZone: "",
      upDown: "green-up",
      confirmOrders: true,
      hideSmallBalances: false,
      hideAmounts: false,
      contractUnit: "CONT",
      set: (patch) => set(patch),
    }),
    {
      name: "exchange.settings",
      version: 2,
      // Before version 2 every kept language counted as chosen: a user's switch stays.
      migrate: (saved, version) => (version < 2 ? { ...(saved as Settings), localeChosen: true } : saved) as SettingsState,
      merge: (saved, current) => {
        const s = (saved ?? {}) as Partial<Settings>;
        return { ...current, ...s, locale: restoredLocale(s, browserTags()) };
      },
    },
  ),
);

/** timeZoneOf returns the zone to format times in. */
export function timeZoneOf(s: Pick<Settings, "timeZone">): string | undefined {
  return s.timeZone || undefined;
}

/**
 * applySettings mirrors the settings onto <html>: lang, data-updown (the
 * tokens swap the rise and fall colours) and the theme.
 */
export function applySettings(s: Settings, theme: "dark" | "light" = "dark"): void {
  const root = globalThis.document?.documentElement;
  if (!root) return;
  root.lang = s.locale;
  root.dataset.updown = s.upDown;
  root.dataset.theme = theme;
}
