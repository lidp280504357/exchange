import { frontBool, frontNumber, frontString, parseFrontMatter } from "./frontmatter";
import { excerpt, parseMarkdown, type MarkdownDoc } from "./markdown";

// The announcements and help articles are Markdown files in the
// repository (web/packages/core/content/<section>/<slug>.<locale>.md,
// design §6.2), bundled at build time, one chunk per file: an article
// page loads only its own file, a list loads the files of one language.
// A missing English file falls back to the Chinese one.

export type ContentSection = "announcements" | "help";
export type ContentLocale = "zh-CN" | "en";

export const CONTENT_FALLBACK: ContentLocale = "zh-CN";

/** The help centre's categories, in sidebar order (front matter `category`). */
export const HELP_CATEGORIES = ["account", "funds", "trading", "futures", "faq"] as const;

type Loader = () => Promise<string>;

const files: Record<ContentSection, Record<string, Loader>> = {
  announcements: import.meta.glob<string>("../../content/announcements/*.md", { query: "?raw", import: "default" }),
  help: import.meta.glob<string>("../../content/help/*.md", { query: "?raw", import: "default" }),
};

const FILE = /\/([a-z0-9][a-z0-9-]*)\.(zh-CN|en)\.md$/;

/** indexFiles maps slug → language → loader from the file paths alone. */
export function indexFiles<T>(paths: Record<string, T>): Map<string, Map<ContentLocale, T>> {
  const out = new Map<string, Map<ContentLocale, T>>();
  for (const [path, load] of Object.entries(paths)) {
    const m = FILE.exec(path);
    if (!m) continue;
    const slug = m[1]!;
    let byLocale = out.get(slug);
    if (!byLocale) {
      byLocale = new Map();
      out.set(slug, byLocale);
    }
    byLocale.set(m[2] as ContentLocale, load);
  }
  return out;
}

const indexes: Record<ContentSection, Map<string, Map<ContentLocale, Loader>>> = {
  announcements: indexFiles(files.announcements),
  help: indexFiles(files.help),
};

/** pickLocale chooses the file to show: the language asked for, else the fallback. */
export function pickLocale<T>(byLocale: Map<ContentLocale, T> | undefined, locale: ContentLocale): { locale: ContentLocale; value: T } | null {
  if (!byLocale) return null;
  const own = byLocale.get(locale);
  if (own !== undefined) return { locale, value: own };
  const fb = byLocale.get(CONTENT_FALLBACK);
  return fb !== undefined ? { locale: CONTENT_FALLBACK, value: fb } : null;
}

export type ArticleMeta = {
  section: ContentSection;
  slug: string;
  /** The language of the file shown. */
  locale: ContentLocale;
  /** Shown in the fallback language because the one asked for is missing. */
  fallback: boolean;
  title: string;
  /** YYYY-MM-DD. */
  date: string;
  pinned: boolean;
  category: string;
  order: number;
  /** The front matter's summary, or the first paragraph. */
  summary: string;
};

export type Article = ArticleMeta & { doc: MarkdownDoc };

/** toArticle parses a file into an article. */
export function toArticle(section: ContentSection, slug: string, locale: ContentLocale, fallback: boolean, src: string): Article {
  const { data, body } = parseFrontMatter(src);
  const doc = parseMarkdown(body);
  return {
    section,
    slug,
    locale,
    fallback,
    title: frontString(data, "title", slug),
    date: frontString(data, "date"),
    pinned: frontBool(data, "pinned"),
    category: frontString(data, "category", section === "help" ? "faq" : "notice"),
    order: frontNumber(data, "order", 0),
    summary: frontString(data, "summary") || excerpt(doc.blocks),
    doc,
  };
}

/** listSlugs returns a section's article slugs. */
export function listSlugs(section: ContentSection): string[] {
  return [...indexes[section].keys()];
}

/** loadArticle loads one article in a language (or the fallback); null when there is none. */
export async function loadArticle(section: ContentSection, slug: string, locale: ContentLocale): Promise<Article | null> {
  const picked = pickLocale(indexes[section].get(slug), locale);
  if (!picked) return null;
  return toArticle(section, slug, picked.locale, picked.locale !== locale, await picked.value());
}

function categoryRank(category: string): number {
  const i = (HELP_CATEGORIES as readonly string[]).indexOf(category);
  return i === -1 ? HELP_CATEGORIES.length : i;
}

/**
 * sortArticles orders a list: announcements pinned first, then newest;
 * help articles by category (sidebar order), then `order`, then title.
 */
export function sortArticles<T extends ArticleMeta>(section: ContentSection, list: readonly T[]): T[] {
  const out = [...list];
  if (section === "announcements") {
    return out.sort((a, b) => Number(b.pinned) - Number(a.pinned) || b.date.localeCompare(a.date) || a.slug.localeCompare(b.slug));
  }
  return out.sort(
    (a, b) =>
      categoryRank(a.category) - categoryRank(b.category) ||
      a.category.localeCompare(b.category) ||
      a.order - b.order ||
      a.title.localeCompare(b.title),
  );
}

/** loadArticles loads every article of a section in a language, sorted. */
export async function loadArticles(section: ContentSection, locale: ContentLocale): Promise<Article[]> {
  const all = await Promise.all(listSlugs(section).map((slug) => loadArticle(section, slug, locale)));
  return sortArticles(
    section,
    all.filter((a): a is Article => a !== null),
  );
}

/** latestArticles returns the newest n articles by date (pinned or not). */
export function latestArticles<T extends ArticleMeta>(list: readonly T[], n: number): T[] {
  return [...list].sort((a, b) => b.date.localeCompare(a.date) || a.slug.localeCompare(b.slug)).slice(0, n);
}

/** neighbours returns the articles before and after one in a sorted list. */
export function neighbours<T extends ArticleMeta>(list: readonly T[], slug: string): { prev?: T; next?: T } {
  const i = list.findIndex((a) => a.slug === slug);
  if (i === -1) return {};
  return { prev: list[i - 1], next: list[i + 1] };
}
