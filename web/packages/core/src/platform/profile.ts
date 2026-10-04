import type { components } from "../api/gen/platform";
import type { ContentMode } from "../content/markdown";
import { formatAmount } from "../format/number";

// The platform's profile (design 2026-10-04 §4.1, GET /v1/platform/profile):
// the exchange's name, images, colours, footer, contact, test mode,
// registration and welcome credits, as operators set them in the admin
// console. The sites read it at start and every minute; until it
// answers, and while it cannot, they show the built-in profile below, so
// they never go blank.

export type PlatformProfile = components["schemas"]["PlatformProfile"];
export type Texts = components["schemas"]["Texts"];
export type WelcomeCredit = components["schemas"]["WelcomeCredit"];
export type PlatformImageKind = components["schemas"]["PlatformImageKind"];

/** DEFAULT_BRAND is the built-in name, also the i18n {{brand}} until the profile is read. */
export const DEFAULT_BRAND = "Astras";

/**
 * DEFAULT_PROFILE is what the sites show without the API: the built-in
 * name, images and colours. It promises nothing it cannot keep while the
 * profile is unknown: no welcome credits, no banner, sign-ups open.
 */
export const DEFAULT_PROFILE: PlatformProfile = {
  name: DEFAULT_BRAND,
  short_name: DEFAULT_BRAND,
  domain: "",
  theme_color: "#0b0e11",
  brand_color: "#f0b90b",
  images: { logo_light: null, logo_dark: null, favicon: null, apple_touch_icon: null },
  footer: {
    copyright: { "zh-CN": `© 2026 ${DEFAULT_BRAND}`, en: `© 2026 ${DEFAULT_BRAND}` },
    compliance: { "zh-CN": "", en: "" },
  },
  contact: { email: "", support_url: null },
  social: [],
  default_locale: "zh-CN",
  test_mode: { enabled: false, banner: false, text: { "zh-CN": "", en: "" } },
  registration: { status: "OPEN", closed_text: { "zh-CN": "", en: "" } },
  welcome_credits: [],
  version: 0,
  updated_at: "1970-01-01T00:00:00.000Z",
};

/**
 * normalizeProfile fills in test_mode for a profile read before the
 * learning mode was renamed (2026-10-05): a browser may hold one in its
 * cache for a minute after the deploy.
 */
export function normalizeProfile(p: PlatformProfile): PlatformProfile {
  if (p.test_mode) return p;
  const old = (p as { learning_mode?: { enabled: boolean; text: Texts } }).learning_mode;
  return { ...p, test_mode: { enabled: old?.enabled ?? false, banner: true, text: old?.text ?? { "zh-CN": "", en: "" } } };
}

/** contentMode is the content the sites show (design 2026-10-04 §4.4): "test" in test mode, "formal" when live. */
export function contentMode(p: Pick<PlatformProfile, "test_mode">): ContentMode {
  return p.test_mode.enabled ? "test" : "formal";
}

/** textOf picks a text in the locale, else the Chinese one. */
export function textOf(texts: Partial<Texts> | undefined, locale: string): string {
  if (!texts) return "";
  return (locale === "en" ? texts.en : texts["zh-CN"]) || texts["zh-CN"] || "";
}

/**
 * creditsText renders the welcome credits for copy such as "注册即得
 * {{credits}}": "10,000 USDT、0.1 BTC、2 ETH" (en: "10,000 USDT, 0.1 BTC
 * and 2 ETH"); "" when there are none, and the copy is left out.
 */
export function creditsText(list: readonly WelcomeCredit[] | undefined, locale: string): string {
  const parts = (list ?? []).map((c) => `${formatAmount(c.amount)} ${c.asset}`);
  if (parts.length === 0) return "";
  if (locale !== "en") return parts.join("、");
  return parts.length === 1 ? parts[0]! : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

/** registrationOpen reports whether sign-ups are open. */
export function registrationOpen(p: Pick<PlatformProfile, "registration">): boolean {
  return p.registration.status !== "CLOSED";
}

/**
 * brandForeground is the text colour on the brand colour: dark on a light
 * brand (the built-in yellow), white on a dark one.
 */
export function brandForeground(hex: string): string {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) return "#0b0e11";
  const [r, g, b] = [m[1]!, m[2]!, m[3]!].map((h) => {
    const c = parseInt(h, 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  const luminance = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return luminance > 0.35 ? "#0b0e11" : "#ffffff";
}
