import i18n, { type Resource } from "i18next";
import { initReactI18next } from "react-i18next";
import { ApiError } from "../api/errors";
import { formatAmount } from "../format/number";
import { DEFAULT_BRAND } from "../platform/profile";
import { useSettings, type Locale } from "../settings/store";
import { en } from "./en";
import { zhCN } from "./zh-CN";

export { en, zhCN };
export type Messages = typeof zhCN;

// One i18next instance per app, started with the shared strings; each app
// merges its own page strings (initI18n's extra) under the same keys.

let started = false;

/** initI18n starts i18next in the saved language; call once at start-up. */
export function initI18n(extra: { "zh-CN"?: Record<string, unknown>; en?: Record<string, unknown> } = {}): typeof i18n {
  if (started) return i18n;
  started = true;
  const resources: Resource = {
    "zh-CN": { translation: deepMerge(zhCN, extra["zh-CN"] ?? {}) },
    en: { translation: deepMerge(en, extra.en ?? {}) },
  };
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
 * registerMessages adds a page's own strings when its chunk loads (both
 * languages, merged deeply into the shared ones), so the first screen does
 * not carry every page's text.
 */
export function registerMessages(messages: { "zh-CN"?: Record<string, unknown>; en?: Record<string, unknown> }): void {
  for (const lng of ["zh-CN", "en"] as const) {
    const m = messages[lng];
    if (m) i18n.addResourceBundle(lng, "translation", m, true, true);
  }
}

/** setLocale switches the language and remembers it. */
export function setLocale(locale: Locale): void {
  useSettings.getState().set({ locale });
  void i18n.changeLanguage(locale);
  if (globalThis.document) document.documentElement.lang = locale;
}

// Enum values that mean different things by kind: an account status
// FROZEN is not a frozen balance.
const kindOverrides: Record<string, Record<string, string>> = {
  accountStatus: { FROZEN: "FROZEN_ACCOUNT" },
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
