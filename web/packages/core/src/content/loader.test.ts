import { describe, expect, it } from "vitest";
import { plainText, type Block, type Inline } from "./markdown";
import {
  HELP_CATEGORIES,
  indexFiles,
  latestArticles,
  listSlugs,
  loadArticle,
  loadArticles,
  neighbours,
  pickLocale,
  sortArticles,
  type Article,
  type ArticleMeta,
  type ContentSection,
} from "./loader";

function meta(over: Partial<ArticleMeta>): ArticleMeta {
  return { section: "announcements", slug: "x", locale: "zh-CN", fallback: false, title: "x", date: "", pinned: false, category: "", order: 0, summary: "", ...over };
}

describe("file index and language fallback", () => {
  it("maps slugs and languages from file names", () => {
    const idx = indexFiles({ "../../content/help/a.zh-CN.md": 1, "../../content/help/a.en.md": 2, "../../content/help/b.zh-CN.md": 3, "../../content/help/README.md": 4 });
    expect([...idx.keys()]).toEqual(["a", "b"]);
    expect(pickLocale(idx.get("a"), "en")).toEqual({ locale: "en", value: 2 });
    expect(pickLocale(idx.get("b"), "en")).toEqual({ locale: "zh-CN", value: 3 });
    expect(pickLocale(idx.get("c"), "en")).toBeNull();
  });
});

describe("ordering", () => {
  it("puts pinned announcements first, then the newest", () => {
    const list = [
      meta({ slug: "a", date: "2026-09-29" }),
      meta({ slug: "b", date: "2026-09-30" }),
      meta({ slug: "c", date: "2026-09-28", pinned: true }),
      meta({ slug: "d", date: "2026-09-30" }),
    ];
    expect(sortArticles("announcements", list).map((a) => a.slug)).toEqual(["c", "b", "d", "a"]);
    expect(latestArticles(list, 3).map((a) => a.slug)).toEqual(["b", "d", "a"]);
  });

  it("orders help by category, then order, then title", () => {
    const list = [
      meta({ section: "help", slug: "faq", category: "faq", order: 1, title: "FAQ" }),
      meta({ section: "help", slug: "w", category: "funds", order: 2, title: "W" }),
      meta({ section: "help", slug: "d", category: "funds", order: 1, title: "D" }),
      meta({ section: "help", slug: "x", category: "other", order: 1, title: "X" }),
      meta({ section: "help", slug: "r", category: "account", order: 1, title: "R" }),
    ];
    const sorted = sortArticles("help", list);
    expect(sorted.map((a) => a.slug)).toEqual(["r", "d", "w", "faq", "x"]);
    expect(neighbours(sorted, "d")).toEqual({ prev: sorted[0], next: sorted[2] });
    expect(neighbours(sorted, "nope")).toEqual({});
  });
});

// The repository's own articles: complete, in both languages, linked correctly.

function links(blocks: readonly Block[]): string[] {
  const out: string[] = [];
  const inl = (nodes: readonly Inline[]) => {
    for (const n of nodes) {
      if (n.type === "link") out.push(n.href);
      if ("children" in n) inl(n.children);
    }
  };
  for (const b of blocks) {
    if (b.type === "heading" || b.type === "paragraph") inl(b.children);
    else if (b.type === "blockquote") out.push(...links(b.children));
    else if (b.type === "list") for (const item of b.items) out.push(...links(item));
    else if (b.type === "table") for (const cell of [...b.header, ...b.rows.flat()]) inl(cell);
  }
  return out;
}

function text(blocks: readonly Block[]): string {
  return blocks
    .map((b) => {
      switch (b.type) {
        case "heading":
        case "paragraph":
          return plainText(b.children);
        case "blockquote":
          return text(b.children);
        case "list":
          return b.items.map(text).join(" ");
        default:
          return "";
      }
    })
    .join(" ");
}

const SITE_PATHS = /^\/(markets|trade\/[A-Z-]+|futures\/[A-Z-]+|coin\/[A-Z0-9]+|assets(\/(deposit|withdraw|transfer|history))?|account\/(security|settings|sessions)|notifications|announcements|help|login|register|reset|docs\/)$/;

describe.each<ContentSection>(["announcements", "help"])("the %s", (section) => {
  it("exist in Chinese and English with complete front matter", async () => {
    const slugs = listSlugs(section);
    expect(slugs.length).toBeGreaterThanOrEqual(section === "help" ? 8 : 3);
    for (const locale of ["zh-CN", "en"] as const) {
      const list = await loadArticles(section, locale);
      expect(list).toHaveLength(slugs.length);
      for (const a of list) {
        expect(a.fallback, `${a.slug}.${locale}`).toBe(false);
        expect(a.title, a.slug).not.toBe(a.slug);
        expect(a.summary.length, a.slug).toBeGreaterThan(10);
        expect(a.doc.blocks.length, a.slug).toBeGreaterThan(2);
        if (section === "announcements") {
          expect(a.date >= "2026-09-28" && a.date <= "2026-09-30", `${a.slug} ${a.date}`).toBe(true);
        } else {
          expect(HELP_CATEGORIES as readonly string[], a.slug).toContain(a.category);
          expect(a.order, a.slug).toBeGreaterThan(0);
        }
      }
    }
  });

  it("say that funds are simulated and link only to real pages", async () => {
    const help = new Set(listSlugs("help"));
    const news = new Set(listSlugs("announcements"));
    for (const locale of ["zh-CN", "en"] as const) {
      for (const a of await loadArticles(section, locale)) {
        const body = text(a.doc.blocks);
        expect(body, `${a.slug}.${locale}`).toMatch(locale === "en" ? /simulated/ : /模拟/);
        for (const href of links(a.doc.blocks)) {
          const where = `${a.slug}.${locale} → ${href}`;
          if (href.startsWith("/help/")) expect(help.has(href.slice(6)), where).toBe(true);
          else if (href.startsWith("/announcements/")) expect(news.has(href.slice(15)), where).toBe(true);
          else if (href.startsWith("/")) expect(href, where).toMatch(SITE_PATHS);
          else expect(href, where).toMatch(/^https:\/\//);
        }
      }
    }
  });

  it("have matching tables of contents in both languages", async () => {
    for (const slug of listSlugs(section)) {
      const zh = (await loadArticle(section, slug, "zh-CN")) as Article;
      const en = (await loadArticle(section, slug, "en")) as Article;
      expect(en.doc.toc.map((t) => t.depth), slug).toEqual(zh.doc.toc.map((t) => t.depth));
      expect(en.category, slug).toBe(zh.category);
      expect(en.date, slug).toBe(zh.date);
      expect(en.pinned, slug).toBe(zh.pinned);
      expect(en.order, slug).toBe(zh.order);
    }
  });
});

describe("loadArticle", () => {
  it("returns null for an unknown slug", async () => {
    expect(await loadArticle("help", "no-such-article", "zh-CN")).toBeNull();
  });
});
