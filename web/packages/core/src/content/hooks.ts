import { useQuery } from "@tanstack/react-query";
import { useContentMode, usePlatformProfile } from "../platform/hooks";
import { useSettings } from "../settings/store";
import { loadArticle, loadArticles, type ContentSection } from "./loader";
import type { ContentMode } from "./markdown";

// Query hooks of the content: the bundled files and what the admin console
// publishes (design 2026-10-02 §4.5). The API's answers are cached for 15
// seconds; the lists ask again every 45 while shown, so a new announcement
// appears within a minute (design §9). They follow the exchange's mode
// (design 2026-10-04 §4.4): a switch of mode is a new query, and none is
// made before the profile has said the mode.

export const contentKeys = {
  list: (section: ContentSection, locale: string, mode: ContentMode) => ["content", section, locale, mode] as const,
  article: (section: ContentSection, locale: string, mode: ContentMode, slug: string) => ["content", section, locale, mode, slug] as const,
};

/**
 * useModeKnown tells whether the platform profile has answered, or failed
 * to (the built-in live mode stands then): until it has, the content waits,
 * so a site in test mode does not load the live content first and swap it
 * a moment later (review BK).
 */
export function useModeKnown(): boolean {
  return !usePlatformProfile().isPending;
}

/** useArticles returns a section's articles in the user's language and the exchange's mode, sorted. */
export function useArticles(section: ContentSection) {
  const locale = useSettings((s) => s.locale);
  const mode = useContentMode();
  const known = useModeKnown();
  return useQuery({
    queryKey: contentKeys.list(section, locale, mode),
    queryFn: () => loadArticles(section, locale, mode),
    staleTime: 30_000,
    refetchInterval: 45_000,
    enabled: known,
  });
}

/** useArticle returns one article (null when the slug does not exist, or not in the exchange's mode). */
export function useArticle(section: ContentSection, slug: string) {
  const locale = useSettings((s) => s.locale);
  const mode = useContentMode();
  const known = useModeKnown();
  return useQuery({
    queryKey: contentKeys.article(section, locale, mode, slug),
    queryFn: () => loadArticle(section, slug, locale, mode),
    staleTime: 30_000,
    enabled: known,
  });
}
