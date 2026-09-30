import { HELP_CATEGORIES, type ArticleMeta } from "@exchange/core/content/index";

// Pure pieces of the mobile announcement and help pages: the help search,
// the categories that have articles, grouping and related articles. The
// articles themselves (loading, sorting, neighbours) come from
// @exchange/core/content.

/**
 * matchArticle: every word of the query appears in the title or the
 * summary (case-insensitive); an empty query matches everything.
 */
export function matchArticle(a: Pick<ArticleMeta, "title" | "summary">, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const text = `${a.title}\n${a.summary}`.toLowerCase();
  return words.every((w) => text.includes(w));
}

/** helpCategories lists the categories that have articles: the help centre's order, then any other by name. */
export function helpCategories(list: readonly Pick<ArticleMeta, "category">[]): string[] {
  const present = new Set(list.map((a) => a.category));
  const known: string[] = HELP_CATEGORIES.filter((c) => present.has(c));
  const other = [...present].filter((c) => !known.includes(c)).sort();
  return [...known, ...other];
}

/** groupByCategory groups sorted articles by category, keeping their order. */
export function groupByCategory<T extends Pick<ArticleMeta, "category">>(list: readonly T[]): { category: string; items: T[] }[] {
  const groups: { category: string; items: T[] }[] = [];
  for (const a of list) {
    const g = groups.find((x) => x.category === a.category);
    if (g) g.items.push(a);
    else groups.push({ category: a.category, items: [a] });
  }
  return groups;
}

/** relatedArticles lists up to n other articles of an article's category, in list order. */
export function relatedArticles<T extends Pick<ArticleMeta, "slug" | "category">>(list: readonly T[], slug: string, n = 4): T[] {
  const current = list.find((a) => a.slug === slug);
  if (!current) return [];
  return list.filter((a) => a.slug !== slug && a.category === current.category).slice(0, n);
}
