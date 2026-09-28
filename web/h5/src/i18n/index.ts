import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { ApiError } from "../api/client";
import { en } from "./en";
import { zhCN } from "./zh-CN";

const KEY = "exchange.lang";

export function initialLanguage(): string {
  const saved = localStorage.getItem(KEY);
  if (saved) return saved;
  return navigator.language.toLowerCase().startsWith("en") ? "en" : "zh-CN";
}

void i18n.use(initReactI18next).init({
  resources: { "zh-CN": { translation: zhCN }, en: { translation: en } },
  lng: initialLanguage(),
  fallbackLng: "zh-CN",
  interpolation: { escapeValue: false },
});

export function setLanguage(lang: string) {
  localStorage.setItem(KEY, lang);
  void i18n.changeLanguage(lang);
  document.documentElement.lang = lang;
}

// codeText localizes an API enum value (entry types, statuses, methods);
// unknown values show as they are. Account statuses reuse FROZEN, which
// also names a balance kind, so they go through FROZEN_ACCOUNT.
export function codeText(code: string, kind: "status" | "other" = "other"): string {
  const key = `codes.${kind === "status" && code === "FROZEN" ? "FROZEN_ACCOUNT" : code}`;
  return i18n.exists(key) ? i18n.t(key) : code;
}

// errorText localizes an error by its stable code (§7.1).
export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `errors.${err.code}`;
    return i18n.exists(key) ? i18n.t(key) : i18n.t("errors.unknown", { code: err.code });
  }
  return i18n.t("errors.unknown", { code: err instanceof Error ? err.message : String(err) });
}

export default i18n;
