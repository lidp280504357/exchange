import { notificationApi, unwrap } from "../api/client";
import { ApiError } from "../api/errors";
import type { components } from "../api/gen/notification";
import { frontBool, frontNumber, frontString, parseFrontMatter } from "./frontmatter";
import { excerpt, parseMarkdown, type MarkdownDoc } from "./markdown";

// The announcements and help articles are Markdown files in the
// repository (web/packages/core/content/<section>/<slug>.<locale>.md,
// design §6.2), bundled at build time, one chunk per file: an article
// page loads only its own file, a list loads the files of one language.
// A missing English file falls back to the Chinese one.

export type ContentSection = "announcements" | "help" | "legal" | "home";

/** The legal and information pages (design 2026-10-04 §4.4), bundled as drafts the console may replace. */
export const LEGAL_SLUGS = ["terms", "privacy", "risk", "fees", "about", "contact"] as const;
export type LegalSlug = (typeof LEGAL_SLUGS)[number];
export type ContentLocale = "zh-CN" | "en";

export const CONTENT_FALLBACK: ContentLocale = "zh-CN";

/** The help centre's categories, in sidebar order (front matter `category`). */
export const HELP_CATEGORIES = ["account", "funds", "trading", "futures", "faq"] as const;

type Loader = () => Promise<string>;

const files: Record<ContentSection, Record<string, Loader>> = {
  announcements: import.meta.glob<string>("../../content/announcements/*.md", { query: "?raw", import: "default" }),
  help: import.meta.glob<string>("../../content/help/*.md", { query: "?raw", import: "default" }),
  legal: import.meta.glob<string>("../../content/legal/*.md", { query: "?raw", import: "default" }),
  home: import.meta.glob<string>("../../content/home/*.md", { query: "?raw", import: "default" }),
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
  legal: indexFiles(files.legal),
  home: indexFiles(files.home),
};

/** defaultCategory is a section's category when a file or article names none. */
function defaultCategory(section: ContentSection): string {
  return section === "help" ? "faq" : section === "announcements" ? "notice" : "";
}

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
    category: frontString(data, "category", defaultCategory(section)),
    order: frontNumber(data, "order", 0),
    summary: frontString(data, "summary") || excerpt(doc.blocks),
    doc,
  };
}

/** listSlugs returns a section's article slugs. */
export function listSlugs(section: ContentSection): string[] {
  return [...indexes[section].keys()];
}

/** loadBundledArticle loads one article of the repository's files in a language (or the fallback); null when there is none. */
export async function loadBundledArticle(section: ContentSection, slug: string, locale: ContentLocale): Promise<Article | null> {
  const picked = pickLocale(indexes[section].get(slug), locale);
  if (!picked) return null;
  return toArticle(section, slug, picked.locale, picked.locale !== locale, await picked.value());
}

/** bundledFile returns a bundled file as written in a language, null when there is none in it. */
export async function bundledFile(section: ContentSection, slug: string, locale: ContentLocale): Promise<string | null> {
  const load = indexes[section].get(slug)?.get(locale);
  return load ? load() : null;
}

/** bundledSource returns a file as written, its article and Markdown body (the console copies it to edit); null when there is none in the language. */
export async function bundledSource(section: ContentSection, slug: string, locale: ContentLocale): Promise<(Article & { body: string }) | null> {
  const load = indexes[section].get(slug)?.get(locale);
  if (!load) return null;
  const src = await load();
  return { ...toArticle(section, slug, locale, false, src), body: parseFrontMatter(src).body };
}

// The articles the admin console publishes (design 2026-10-02 §4.5, GET
// /v1/announcements and /v1/help) join the bundled files and win over a
// file of the same slug; while the API cannot be reached the files alone
// are shown.

type PublishedSummary = components["schemas"]["ArticleSummary"];

/** fromPublished turns an article the API returns into one of ours; a list's comes without its body. */
export function fromPublished(section: ContentSection, s: PublishedSummary, body?: string): Article {
  const doc = parseMarkdown(body ?? "");
  return {
    section,
    slug: s.slug,
    locale: s.locale,
    fallback: s.fallback,
    title: s.title,
    date: s.published_at.slice(0, 10),
    pinned: s.pinned,
    category: s.category || defaultCategory(section),
    order: s.order,
    summary: s.summary || excerpt(doc.blocks),
    doc,
  };
}

type Published = { articles: Article[]; withdrawn: ReadonlySet<string> };

/**
 * fetchPublished returns the console's articles and the slugs it took off
 * (their files are hidden too). The lists read the first page, the newest
 * 100 announcements and the first 100 help articles; an older one is still
 * reached by its link (C5.5 ⑫).
 */
async function fetchPublished(section: ContentSection, locale: ContentLocale): Promise<Published> {
  const query = { params: { query: { locale, limit: 100 } } };
  const page = await {
    announcements: () => unwrap(notificationApi.GET("/v1/announcements", query)),
    help: () => unwrap(notificationApi.GET("/v1/help", query)),
    legal: () => unwrap(notificationApi.GET("/v1/legal", query)),
    home: () => unwrap(notificationApi.GET("/v1/home", query)),
  }[section]();
  return { articles: page.items.map((s) => fromPublished(section, s)), withdrawn: new Set(page.withdrawn ?? []) };
}

/** The console took the article off: no file stands in for it. */
const WITHDRAWN = "withdrawn";

/** fetchPublishedArticle returns the console's article, null when it published none with this slug. */
async function fetchPublishedArticle(section: ContentSection, slug: string, locale: ContentLocale): Promise<Article | typeof WITHDRAWN | null> {
  try {
    const a = await fetchOne(section, slug, locale);
    return fromPublished(section, a, a.body);
  } catch (err) {
    if (err instanceof ApiError && err.code === "NOTIFY_ARTICLE_WITHDRAWN") return WITHDRAWN;
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

/** fetchOne asks the API for a section's article; the fixed sections only for their own slugs. */
export function fetchOne(section: ContentSection, slug: string, locale: ContentLocale) {
  const query = { params: { path: { slug }, query: { locale } } };
  switch (section) {
    case "announcements":
      return unwrap(notificationApi.GET("/v1/announcements/{slug}", query));
    case "help":
      return unwrap(notificationApi.GET("/v1/help/{slug}", query));
    case "legal":
      if (!(LEGAL_SLUGS as readonly string[]).includes(slug)) return Promise.reject(new ApiError(404, "COMMON_NOT_FOUND", "no such page"));
      return unwrap(notificationApi.GET("/v1/legal/{slug}", { params: { path: { slug: slug as LegalSlug }, query: { locale } } }));
    case "home":
      if (slug !== "home-hero") return Promise.reject(new ApiError(404, "COMMON_NOT_FOUND", "no such block"));
      return unwrap(notificationApi.GET("/v1/home/{slug}", { params: { path: { slug: "home-hero" }, query: { locale } } }));
  }
}

/**
 * loadArticle loads one article in a language: the console's when it
 * published one, else the bundled file; null when neither, or when the
 * console took it off.
 */
export async function loadArticle(section: ContentSection, slug: string, locale: ContentLocale): Promise<Article | null> {
  const published = await fetchPublishedArticle(section, slug, locale).catch(() => null);
  if (published === WITHDRAWN) return null;
  return published ?? loadBundledArticle(section, slug, locale);
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

/**
 * loadArticles loads every article of a section in a language, the
 * bundled files and the console's, sorted; a file the console took off is
 * left out.
 */
export async function loadArticles(section: ContentSection, locale: ContentLocale): Promise<Article[]> {
  const [bundled, published] = await Promise.all([
    Promise.all(listSlugs(section).map((slug) => loadBundledArticle(section, slug, locale))),
    fetchPublished(section, locale).catch((): Published => ({ articles: [], withdrawn: new Set() })),
  ]);
  const bySlug = new Map<string, Article>();
  for (const a of bundled) if (a && !published.withdrawn.has(a.slug)) bySlug.set(a.slug, a);
  for (const a of published.articles) bySlug.set(a.slug, a);
  return sortArticles(section, [...bySlug.values()]);
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
