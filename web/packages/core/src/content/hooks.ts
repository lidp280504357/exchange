import { useQuery } from "@tanstack/react-query";
import { useSettings } from "../settings/store";
import { loadArticle, loadArticles, type ContentSection } from "./loader";

// Query hooks of the content: the bundled files and what the admin console
// publishes (design 2026-10-02 §4.5). The API's answers are cached for 15
// seconds; the lists ask again every 45 while shown, so a new announcement
// appears within a minute (design §9).

export const contentKeys = {
  list: (section: ContentSection, locale: string) => ["content", section, locale] as const,
  article: (section: ContentSection, locale: string, slug: string) => ["content", section, locale, slug] as const,
};

/** useArticles returns a section's articles in the user's language, sorted. */
export function useArticles(section: ContentSection) {
  const locale = useSettings((s) => s.locale);
  return useQuery({
    queryKey: contentKeys.list(section, locale),
    queryFn: () => loadArticles(section, locale),
    staleTime: 30_000,
    refetchInterval: 45_000,
  });
}

/** useArticle returns one article (null when the slug does not exist). */
export function useArticle(section: ContentSection, slug: string) {
  const locale = useSettings((s) => s.locale);
  return useQuery({
    queryKey: contentKeys.article(section, locale, slug),
    queryFn: () => loadArticle(section, slug, locale),
    staleTime: 30_000,
  });
}
