import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { platformApi, unwrap } from "../api/client";
import { followFallbackLocale, setBrandVariable } from "../i18n/index";
import { qk } from "../query/keys";
import { useSettings } from "../settings/store";
import type { ContentMode } from "../content/markdown";
import { brandForeground, contentMode, creditsText, DEFAULT_BRAND, DEFAULT_PROFILE, normalizeProfile, textOf, type PlatformProfile } from "./profile";

/** usePlatformProfile reads the profile, again every minute (it changes without a build). */
export function usePlatformProfile() {
  return useQuery({
    queryKey: qk.platform,
    queryFn: async () => normalizeProfile(await unwrap(platformApi.GET("/v1/platform/profile"))),
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

/** useBranding returns the profile, the built-in one until (and while not) read. */
export function useBranding(): PlatformProfile {
  return usePlatformProfile().data ?? DEFAULT_PROFILE;
}

/** useTestMode returns the profile's test mode: on, its banner shown, the banner's text. */
export function useTestMode(): PlatformProfile["test_mode"] {
  return useBranding().test_mode;
}

/** useContentMode returns the content the sites show now: "test" in test mode, "formal" when live. */
export function useContentMode(): ContentMode {
  return contentMode(useBranding());
}

/**
 * useWelcomeCredits returns what a new account gets as text in the user's
 * language ("10,000 USDT、0.1 BTC"), "" when nothing: the copy that
 * promises it is left out then.
 */
export function useWelcomeCredits(): string {
  const p = useBranding();
  const locale = useSettings((s) => s.locale);
  return creditsText(p.welcome_credits, locale);
}

/** useBrandText picks one of the profile's texts in the user's language. */
export function useBrandText(pick: (p: PlatformProfile) => Parameters<typeof textOf>[0]): string {
  const p = useBranding();
  const locale = useSettings((s) => s.locale);
  return textOf(pick(p), locale);
}

let brand = DEFAULT_BRAND;

/** brandName is the exchange's name as last read, for titles set outside React. */
export function brandName(): string {
  return brand;
}

/** The site's own head links, put back when the profile has no image of the kind. */
type HeadDefaults = { favicon: string; faviconType: string; appleTouchIcon: string };

const DEFAULT_HEAD: HeadDefaults = { favicon: "/icon.svg", faviconType: "image/svg+xml", appleTouchIcon: "/apple-touch-icon.png" };

function setLink(rel: string, href: string, type?: string): void {
  let link = document.head.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`);
  if (!link) {
    link = document.createElement("link");
    link.rel = rel;
    document.head.appendChild(link);
  }
  if (link.getAttribute("href") !== href) link.href = href;
  if (type) link.type = type;
  else link.removeAttribute("type");
}

/**
 * applyBranding writes the profile into the page: the favicon and
 * apple-touch-icon links, the theme colour and the brand colours (CSS
 * --brand, --brand-fg; --brand-soft follows --brand).
 */
export function applyBranding(p: PlatformProfile, defaults: HeadDefaults = DEFAULT_HEAD): void {
  if (!globalThis.document) return;
  setLink("icon", p.images.favicon ?? defaults.favicon, p.images.favicon ? undefined : defaults.faviconType);
  setLink("apple-touch-icon", p.images.apple_touch_icon ?? defaults.appleTouchIcon);
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", p.theme_color);
  const root = document.documentElement.style;
  if (p.brand_color === DEFAULT_PROFILE.brand_color) {
    root.removeProperty("--brand");
    root.removeProperty("--brand-fg");
  } else {
    root.setProperty("--brand", p.brand_color);
    root.setProperty("--brand-fg", brandForeground(p.brand_color));
  }
}

/**
 * useBrandingEffects applies the profile to the page and to the strings
 * (i18n {{brand}}, and the fallback language of a visitor who has not
 * chosen one) whenever it changes; the sites call it once at their root
 * and read the profile with useBranding.
 */
export function useBrandingEffects(): PlatformProfile {
  const p = useBranding();
  // A visitor who has not chosen a language: the browser's, else the platform's fallback (F30), once the profile is read.
  const fallback = usePlatformProfile().data?.default_locale;
  useEffect(() => {
    if (fallback) followFallbackLocale(fallback);
  }, [fallback]);
  useEffect(() => {
    // A page that set no title of its own shows the name (index.html has the built-in one).
    if (globalThis.document && document.title === brand) document.title = p.name;
    brand = p.name;
    setBrandVariable(p.name);
  }, [p.name]);
  useEffect(() => applyBranding(p), [p]);
  return p;
}
