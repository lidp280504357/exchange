import i18n, { type Resource } from "i18next";
import { initReactI18next } from "react-i18next";
import { ApiError } from "../api/errors";
import { formatAmount } from "../format/number";
import { DEFAULT_BRAND } from "../platform/profile";
import { browserTags, LOCALES, negotiateLocale, useSettings, type Locale } from "../settings/store";
import { en } from "./en";
import { zhCN } from "./zh-CN";
import { zhTW } from "./zh-TW";

export { en, zhCN, zhTW };
export type Messages = typeof zhCN;
/** Strings by language: an app's, a page's. */
export type LocaleMessages = Partial<Record<Locale, Record<string, unknown>>>;

// One i18next instance per app, started with the shared strings; each app
// merges its own page strings (initI18n's extra) under the same keys. The
// Traditional Chinese strings are generated from the Simplified ones
// (scripts/gen-zh-tw.mjs); a key missing in a language shows in zh-CN.

let started = false;

const shared: Record<Locale, Record<string, unknown>> = { "zh-CN": zhCN, "zh-TW": zhTW, en };

// The languages of the app: an app that brings no Traditional Chinese
// strings of its own (the console, design 2026-10-06 繁体中文 §1) shows
// Simplified Chinese instead.
let offered: readonly Locale[] = LOCALES;

/** initI18n starts i18next in the saved language; call once at start-up. */
export function initI18n(extra: LocaleMessages = {}): typeof i18n {
  if (started) return i18n;
  started = true;
  offered = LOCALES.filter((l) => l !== "zh-TW" || extra[l]);
  if (!offered.includes(useSettings.getState().locale)) useSettings.getState().set({ locale: "zh-CN" });
  const resources: Resource = {};
  for (const lng of offered) resources[lng] = { translation: deepMerge(shared[lng], extra[lng] ?? {}) };
  void i18n.use(initReactI18next).init({
    resources,
    lng: useSettings.getState().locale,
    fallbackLng: "zh-CN",
    // {{brand}} is the exchange's name in every string (design 2026-10-04
    // §4.1): the built-in one until the platform profile is read.
    interpolation: { escapeValue: false, defaultVariables: { brand: DEFAULT_BRAND } },
    returnNull: false,
  });
  return i18n;
}

/**
 * setBrandVariable sets the {{brand}} of every string to the platform
 * profile's name and re-renders the translated text when it changed.
 */
export function setBrandVariable(name: string): void {
  const vars = i18n.options.interpolation?.defaultVariables;
  if (!vars || vars.brand === name) return;
  vars.brand = name;
  if (i18n.isInitialized) void i18n.changeLanguage(i18n.language);
}

function deepMerge(base: Record<string, unknown>, extra: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = { ...base };
  for (const [k, v] of Object.entries(extra)) {
    const b = out[k];
    out[k] = isObject(b) && isObject(v) ? deepMerge(b, v) : v;
  }
  return out;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * registerMessages adds a page's own strings when its chunk loads (every
 * language, merged deeply into the shared ones), so the first screen does
 * not carry every page's text.
 */
export function registerMessages(messages: LocaleMessages): void {
  for (const lng of offered) {
    const m = messages[lng];
    if (m) i18n.addResourceBundle(lng, "translation", m, true, true);
  }
}

/**
 * setLocale switches to the language the user chose and remembers it as
 * chosen: the browser and the platform's fallback no longer decide (one
 * the app does not offer is Simplified Chinese).
 */
export function setLocale(wanted: Locale): void {
  const locale = offered.includes(wanted) ? wanted : "zh-CN";
  useSettings.getState().set({ locale, localeChosen: true });
  showLocale(locale);
}

/**
 * followFallbackLocale takes the platform's fallback language (its profile's
 * default_locale, the console's 回退语言) for a visitor who has not chosen
 * one: the first of the browser's preferences the site has, else that
 * language (F30). It is remembered for the next page start; a chosen
 * language stays.
 */
export function followFallbackLocale(fallback: Locale): void {
  const s = useSettings.getState();
  const kept = offered.includes(fallback) ? fallback : "en";
  if (s.fallbackLocale !== kept) s.set({ fallbackLocale: kept });
  if (s.localeChosen) return;
  const locale = negotiateLocale(browserTags(), kept);
  if (locale === s.locale || !offered.includes(locale)) return;
  s.set({ locale });
  showLocale(locale);
}

function showLocale(locale: Locale): void {
  void i18n.changeLanguage(locale);
  if (globalThis.document) document.documentElement.lang = locale;
}

// Enum values that mean different things by kind: an account status
// FROZEN is not a frozen balance.
const kindOverrides: Record<string, Record<string, string>> = {
  accountStatus: { FROZEN: "FROZEN_ACCOUNT" },
  // A user's insurance entry is what a liquidation leaves, the clearance
  // fee (C68, F24); the console's insurance fund keeps 保险基金.
  entry: { INSURANCE_CONTRIBUTION: "LIQUIDATION_CLEARANCE_FEE" },
};

/**
 * enumLabel localizes an API enum value (statuses, sides, entry types).
 * A value without a translation shows readably ("PARTIALLY_FILLED" →
 * "Partially filled") and warns in development, never as a raw code.
 */
export function enumLabel(code: string | null | undefined, kind?: string): string {
  if (!code) return "—";
  const mapped = (kind && kindOverrides[kind]?.[code]) || code;
  const key = `codes.${mapped}`;
  if (i18n.exists(key)) return i18n.t(key);
  if (import.meta.env?.DEV) console.warn(`enumLabel: no translation for ${kind ? `${kind}/` : ""}${code}`);
  const words = code.toLowerCase().split("_").join(" ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** codeText localizes a stable error code given as text (an order's reject reason). */
export function codeText(code: string | null | undefined): string {
  if (!code) return "—";
  const key = `errors.${code}`;
  return i18n.exists(key) ? i18n.t(key) : i18n.t("errors.unknown", { code });
}

/** errorText localizes an error by its stable code (requirements §7.1). */
export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    const detailed = withDetails(err);
    if (detailed) return detailed;
    const key = `errors.${err.code}`;
    return i18n.exists(key) ? i18n.t(key) : i18n.t("errors.unknown", { code: err.code });
  }
  return i18n.t("errors.unknown", { code: err instanceof Error ? err.message : String(err) });
}

// The details some errors' messages name, by message (errorDetails.<key>):
// amounts with their decimals, other values (null) as they come. A code
// may have several messages for different details; the first whose
// details all came is used, so the more specific comes first (a transfer
// out's MARGIN_LEVEL_TOO_LOW carries max_transferable besides the levels).
const detailMessages: Record<string, { key: string; fields: Record<string, number | null>; when?: (details: Record<string, unknown>) => boolean }[]> = {
  DERIV_RISK_LIMIT_EXCEEDED: [{ key: "DERIV_RISK_LIMIT_EXCEEDED", fields: { max_notional: 0, notional: 2, leverage: null } }],
  MARGIN_LIMIT: [{ key: "MARGIN_LIMIT", fields: { max_borrowable: null } }],
  MARGIN_POOL_EMPTY: [{ key: "MARGIN_POOL_EMPTY", fields: { pool_available: null } }],
  MARGIN_LEVEL_TOO_LOW: [
    { key: "MARGIN_LEVEL_TOO_LOW_OUT", fields: { max_transferable: null } },
    { key: "MARGIN_LEVEL_TOO_LOW", fields: { margin_level: 2, warn_level: null } },
  ],
  // A transfer out of a margin account: what may leave.
  LEDGER_INSUFFICIENT_BALANCE: [{ key: "LEDGER_INSUFFICIENT_BALANCE_OUT", fields: { max_transferable: null } }],
  // AUTO_BORROW while margin.auto_borrow is off: the terminals set the side
  // effect back to NONE (margin/trade.ts afterMarginOrder).
  MARGIN_DISABLED: [{ key: "MARGIN_DISABLED_AUTO_BORROW", fields: {}, when: (d) => d.flag === "margin.auto_borrow" }],
  // A cross account's liquidation (C68, F24: details settle_asset and
  // liquidation_id), not one position's.
  DERIV_POSITION_LIQUIDATING: [{ key: "DERIV_CROSS_LIQUIDATING", fields: { settle_asset: null }, when: (d) => d.liquidation_id != null }],
};

/** withDetails is the error's message with its details, when it has one and they all came. */
function withDetails(err: ApiError): string | null {
  for (const { key, fields, when } of detailMessages[err.code] ?? []) {
    const full = `errorDetails.${key}`;
    if (!i18n.exists(full) || (when && !when(err.details))) continue;
    const values: Record<string, string> = {};
    let complete = true;
    for (const [k, decimals] of Object.entries(fields)) {
      const v = err.details[k];
      if (v === undefined || v === null || v === "") {
        complete = false;
        break;
      }
      values[k] = decimals === null ? String(v) : formatAmount(String(v), decimals);
    }
    if (complete) return i18n.t(full, values);
  }
  return null;
}

export { i18n };
