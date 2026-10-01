import i18n, { type Resource } from "i18next";
import { initReactI18next } from "react-i18next";
import { ApiError } from "../api/errors";
import { formatAmount } from "../format/number";
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
    interpolation: { escapeValue: false },
    returnNull: false,
  });
  return i18n;
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

// The details some errors' messages name: amounts with their decimals,
// other values (null) as they come.
const detailFields: Record<string, Record<string, number | null>> = {
  DERIV_RISK_LIMIT_EXCEEDED: { max_notional: 0, notional: 2, leverage: null },
};

/** withDetails is the error's message with its details, when it has one and they all came. */
function withDetails(err: ApiError): string | null {
  const fields = detailFields[err.code];
  const key = `errorDetails.${err.code}`;
  if (!fields || !i18n.exists(key)) return null;
  const values: Record<string, string> = {};
  for (const [k, decimals] of Object.entries(fields)) {
    const v = err.details[k];
    if (v === undefined || v === null || v === "") return null;
    values[k] = decimals === null ? String(v) : formatAmount(String(v), decimals);
  }
  return i18n.t(key, values);
}

export { i18n };
