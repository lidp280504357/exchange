import { create } from "zustand";
import { persist } from "zustand/middleware";

// User preferences kept on the device (design §6.2 account settings):
// language, time zone, the colour of rises, order confirmations and small
// balances. Applying them to the page (html attributes) is applySettings.

export type Locale = "zh-CN" | "zh-TW" | "en";
/** The sites' languages, in menu order. */
export const LOCALES: readonly Locale[] = ["zh-CN", "zh-TW", "en"];
/** Each language's name in itself, the same whatever the page's language. */
export const LOCALE_NAMES: Record<Locale, string> = { "zh-CN": "简体中文", "zh-TW": "繁體中文", en: "English" };
/** green-up: green rises, red falls (default); red-up swaps them. */
export type UpDown = "green-up" | "red-up";

export type Settings = {
  locale: Locale;
  /** An IANA zone such as Asia/Shanghai; "" follows the browser. */
  timeZone: string;
  upDown: UpDown;
  confirmOrders: boolean;
  hideSmallBalances: boolean;
  /** Amounts hidden behind asterisks (the eye toggle on the assets card). */
  hideAmounts: boolean;
};

type SettingsState = Settings & {
  set: (patch: Partial<Settings>) => void;
};

/**
 * localeOf maps a browser language tag to a site language: Traditional
 * Chinese for zh-Hant and Taiwan, Hong Kong and Macao (design 2026-10-06
 * 繁体中文 §2.1), Simplified for the other Chinese, English for the rest.
 */
export function localeOf(tag: string): Locale {
  const t = tag.toLowerCase();
  if (!t.startsWith("zh")) return "en";
  if (t.includes("-hant")) return "zh-TW";
  if (t.includes("-hans")) return "zh-CN";
  return /^zh-(tw|hk|mo)\b/.test(t) ? "zh-TW" : "zh-CN";
}

/** browserLocale is the first visit's language: the browser's first preference. */
function browserLocale(): Locale {
  const nav = globalThis.navigator;
  return localeOf(nav?.languages?.[0] ?? nav?.language ?? "zh-CN");
}

export const useSettings = create<SettingsState>()(
  persist(
    (set) => ({
      locale: browserLocale(),
      timeZone: "",
      upDown: "green-up",
      confirmOrders: true,
      hideSmallBalances: false,
      hideAmounts: false,
      set: (patch) => set(patch),
    }),
    { name: "exchange.settings", version: 1 },
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
