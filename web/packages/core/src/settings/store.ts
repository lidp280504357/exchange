import { create } from "zustand";
import { persist } from "zustand/middleware";

// User preferences kept on the device (design §6.2 account settings):
// language, time zone, the colour of rises, order confirmations and small
// balances. Applying them to the page (html attributes) is applySettings.

export type Locale = "zh-CN" | "en";
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

function browserLocale(): Locale {
  const lang = globalThis.navigator?.language?.toLowerCase() ?? "zh-cn";
  return lang.startsWith("zh") ? "zh-CN" : "en";
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
