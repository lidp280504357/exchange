import { useQuery } from "@tanstack/react-query";
import { useSettings } from "../settings/store";
import { loadArticle, loadArticles, type ContentSection } from "./loader";

// Query hooks of the static content: the files never change while the
// page is open, so they are loaded once per language and kept.

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
    staleTime: Infinity,
  });
}

/** useArticle returns one article (null when the slug does not exist). */
export function useArticle(section: ContentSection, slug: string) {
  const locale = useSettings((s) => s.locale);
  return useQuery({
    queryKey: contentKeys.article(section, locale, slug),
    queryFn: () => loadArticle(section, slug, locale),
    staleTime: Infinity,
  });
}
