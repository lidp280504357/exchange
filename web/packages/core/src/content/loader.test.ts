import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { plainText, type Block, type Inline } from "./markdown";
import {
  bundledSource,
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

// The console's articles come from the API; here it has none (404), so the
// bundled files are what the tests read, unless a test answers otherwise.
let api: (url: string) => Response = () => new Response(JSON.stringify({ code: "COMMON_NOT_FOUND", message: "none" }), { status: 404 });
beforeEach(() => {
  vi.stubGlobal("fetch", (input: Request | string) => Promise.resolve(api(typeof input === "string" ? input : input.url)));
});
afterEach(() => {
  vi.unstubAllGlobals();
  api = () => new Response(JSON.stringify({ code: "COMMON_NOT_FOUND", message: "none" }), { status: 404 });
});

function meta(over: Partial<ArticleMeta>): ArticleMeta {
  return { section: "announcements", slug: "x", locale: "zh-CN", fallback: false, title: "x", date: "", pinned: false, category: "", order: 0, summary: "", modes: "BOTH", ...over };
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

// What live pages must not say (design 2026-10-04 §4.4; the launch drill
// checks the same on the sites).
const TEST_ONLY = { "zh-CN": /测试环境|模拟|Sepolia|10,000|学习/, en: /test environment|simulated|Sepolia|10,000|learning/i } as const;

describe.each<ContentSection>(["announcements", "help"])("the %s", (section) => {
  it("exist in Chinese and English with complete front matter", async () => {
    const slugs = listSlugs(section);
    expect(slugs.length).toBeGreaterThanOrEqual(section === "help" ? 8 : 3);
    for (const locale of ["zh-CN", "en"] as const) {
      const list = await loadArticles(section, locale, "test");
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

  it("never say live what only holds in test mode, and link only to real pages", async () => {
    const help = new Set(listSlugs("help"));
    const news = new Set(listSlugs("announcements"));
    for (const locale of ["zh-CN", "en"] as const) {
      const live = await loadArticles(section, locale, "formal");
      expect(live.length, `${section} live`).toBeGreaterThanOrEqual(section === "help" ? 8 : 3);
      for (const a of live) {
        expect(`${a.title} ${a.summary} ${text(a.doc.blocks)}`, `${a.slug}.${locale} live`).not.toMatch(TEST_ONLY[locale]);
      }
      // In test mode the pinned announcement says the funds are simulated
      // (the content pages' note says it for every article).
      const testing = await loadArticles(section, locale, "test");
      if (section === "announcements") expect(text(testing[0]!.doc.blocks), testing[0]!.slug).toMatch(locale === "en" ? /simulated/ : /模拟/);
      for (const a of [...testing, ...live]) {
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
      const zh = (await loadArticle(section, slug, "zh-CN", "test")) as Article;
      const en = (await loadArticle(section, slug, "en", "test")) as Article;
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
    expect(await loadArticle("help", "no-such-article", "zh-CN", "test")).toBeNull();
  });

  it("leaves out a file meant for the other mode", async () => {
    expect((await loadArticle("announcements", "test-environment", "zh-CN", "test"))?.modes).toBe("TEST");
    expect(await loadArticle("announcements", "test-environment", "zh-CN", "formal")).toBeNull();
  });
});

describe("bundledSource", () => {
  it("returns a file's Markdown as written, for the console to copy", async () => {
    const slug = listSlugs("help")[0]!;
    const src = (await bundledSource("help", slug, "en"))!;
    expect(src.slug).toBe(slug);
    expect(src.locale).toBe("en");
    expect(src.body).not.toMatch(/^---/);
    expect(src.body).toMatch(/^#|\n#/);
    expect(src.title.length).toBeGreaterThan(3);
    expect(await bundledSource("help", "no-such-article", "en")).toBeNull();
  });
});

describe("the console's articles", () => {
  const summary = (slug: string, over: Record<string, unknown> = {}) => ({
    slug, category: "notice", pinned: false, order: 0, title: `T ${slug}`, summary: "", published_at: "2026-10-02T08:00:00Z", locale: "zh-CN",
    fallback: false, version: 1, ...over,
  });
  const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

  it("join the bundled files and win for a slug", async () => {
    const bundled = listSlugs("announcements");
    api = (url) =>
      url.includes("/v1/announcements")
        ? json({ items: [summary("maintenance", { pinned: true }), summary(bundled[0]!, { title: "edited" })], withdrawn: [] })
        : new Response("{}", { status: 404 });
    const list = await loadArticles("announcements", "zh-CN", "test");
    expect(list.length).toBe(bundled.length + 1);
    expect(list[0]!.slug).toBe("maintenance");
    expect(list[0]!.date).toBe("2026-10-02");
    expect(list.find((a) => a.slug === bundled[0])!.title).toBe("edited");
  });

  it("take a file off the list once the console withdrew its slug", async () => {
    const [gone, kept] = listSlugs("help");
    api = (url) => (url.includes("/v1/help") ? json({ items: [], withdrawn: [gone] }) : new Response("{}", { status: 404 }));
    const slugs = (await loadArticles("help", "zh-CN", "test")).map((a) => a.slug);
    expect(slugs).not.toContain(gone);
    expect(slugs).toContain(kept);
    expect(slugs.length).toBe(listSlugs("help").length - 1);
  });

  it("are read with their Markdown body; a 404 falls back to the file", async () => {
    api = (url) =>
      url.includes("/v1/announcements/maintenance")
        ? json({ ...summary("maintenance"), body: ":::test\n测试期间的说明。\n:::\n\n## Tonight\n\nOne hour." })
        : new Response("{}", { status: 404 });
    const a = (await loadArticle("announcements", "maintenance", "zh-CN", "formal")) as Article;
    expect(a.doc.toc.map((t) => t.text)).toEqual(["Tonight"]);
    expect(a.summary).toBe("One hour.");
    expect((await loadArticle("announcements", "maintenance", "zh-CN", "test"))?.summary).toBe("测试期间的说明。");
    const slug = listSlugs("help")[0]!;
    expect((await loadArticle("help", slug, "zh-CN", "formal"))?.slug).toBe(slug);
  });

  it("is not stood in for by its file once withdrawn", async () => {
    const slug = listSlugs("help")[0]!;
    api = () => new Response(JSON.stringify({ code: "NOTIFY_ARTICLE_WITHDRAWN", message: "taken off", trace_id: "t" }), { status: 404 });
    expect(await loadArticle("help", slug, "zh-CN", "test")).toBeNull();
  });

  it("leave the files alone while the API is down", async () => {
    api = () => {
      throw new TypeError("offline");
    };
    expect((await loadArticles("help", "en", "formal")).length).toBe(listSlugs("help").length);
  });
});
