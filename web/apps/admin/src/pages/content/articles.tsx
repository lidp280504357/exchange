import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { bundledSource, listSlugs, type ContentSection } from "@exchange/core/content/loader";
import { renderByMode, type ContentMode } from "@exchange/core/content/markdown";
import { Markdown, type LinkProps } from "@exchange/core/content/render";
import { Badge, Button, cn, DataTable, Drawer, Input, Segmented, Select, Switch, Tabs, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { Copy, ExternalLink, Plus } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, FormError } from "../../kit/actions";
import { ReadOnly } from "../../kit/ReadOnly";
import { TimeText } from "../../kit/format";
import { RowActions } from "../../kit/lists";
import { Card, Page } from "../../kit/Page";

// Announcements and help articles (design 2026-10-02 §4.5): written here in
// Chinese and English (Markdown, previewed as the sites show it), saved as
// drafts, published now or at a time, taken off. The sites read the
// published ones within a minute; one replaces the sites' own file of its
// slug, and taking it off hides that file too.

export type Article = AdminSchemas["ContentArticle"];
export type Text = AdminSchemas["ArticleText"];
export type Section = Article["section"];
/** The sections this page lists (the fixed LEGAL and HOME pages have their own view, FixedPages). */
type ListSection = Extract<Section, "ANNOUNCEMENT" | "HELP">;
type Locale = Text["locale"];

const LOCALES: Locale[] = ["zh-CN", "en"];
const CATEGORIES: Record<ListSection, string[]> = {
  ANNOUNCEMENT: ["notice", "product", "security"],
  HELP: ["account", "funds", "trading", "futures", "faq"],
};
export const FILES: Record<Section, ContentSection> = { ANNOUNCEMENT: "announcements", HELP: "help", LEGAL: "legal", HOME: "home" };
/** fixedSection reports whether a section has a fixed set of slugs, each the sites bundle a draft of. */
export const fixedSection = (s: Section): s is "LEGAL" | "HOME" => s === "LEGAL" || s === "HOME";
/** The user site: the console's domain without its admin. prefix. */
export const SITE = (globalThis.location?.origin ?? "").replace("://admin.", "://");
const slugRE = /^[a-z0-9][a-z0-9-]{0,63}$/;

export const articlesKey = (section: Section) => ["admin", "articles", section];

/** The typography of the preview, as the sites render articles. */
const PROSE = cn(
  "min-w-0 text-sm leading-7 text-fg-2",
  "[&_h1]:mb-3 [&_h1]:mt-6 [&_h1]:text-lg [&_h1]:font-semibold [&_h1]:text-fg-1",
  "[&_h2]:mb-2 [&_h2]:mt-6 [&_h2]:border-b [&_h2]:border-line-1 [&_h2]:pb-1 [&_h2]:text-md [&_h2]:font-semibold [&_h2]:text-fg-1",
  "[&_h3]:mb-2 [&_h3]:mt-5 [&_h3]:font-semibold [&_h3]:text-fg-1 [&_p]:my-2",
  "[&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-6 [&_li]:my-1",
  "[&_a]:text-brand-strong [&_strong]:font-semibold [&_strong]:text-fg-1",
  "[&_code]:rounded-1 [&_code]:bg-bg-3 [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs",
  "[&_blockquote]:my-3 [&_blockquote]:border-l-2 [&_blockquote]:border-brand [&_blockquote]:bg-bg-2 [&_blockquote]:px-3",
  "[&_table]:w-full [&_table]:text-xs [&_th]:border-b [&_th]:border-line-1 [&_th]:px-2 [&_th]:py-1 [&_th]:text-left",
  "[&_td]:border-b [&_td]:border-line-1 [&_td]:px-2 [&_td]:py-1",
);

/** A link in the preview leads to the user site, in a new tab. */
function previewLink({ href, title, children }: LinkProps) {
  return (
    <a href={href.startsWith("/") ? SITE + href : href} title={title} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  );
}

/** title is an article's Chinese title. */
const title = (a: Article) => a.texts.find((t) => t.locale === "zh-CN")?.title ?? a.texts[0]?.title ?? a.slug;

/** shown reports whether the sites show an article now. */
export const shown = (a: Article) => a.status === "PUBLISHED" && (!a.publish_at || new Date(a.publish_at) <= new Date());

/** An article's modes: for test mode, for the live exchange, or both (design 2026-10-04 §4.4). */
export type Modes = Article["modes"];
export const MODES: Modes[] = ["BOTH", "TEST", "FORMAL"];

export type Draft = { slug: string; modes: Modes; category: string; pinned: boolean; order: string; texts: Record<Locale, Text> };

type Source = NonNullable<Awaited<ReturnType<typeof bundledSource>>>;

/** seedOf is the draft of a bundled file, in Chinese and (when there is one) English, for modes (its front matter's by default). */
export function seedOf(slug: string, zh: Source, en: Source | null, modes: Modes = zh.modes): Draft {
  const text = (s: Source): Text => ({ locale: s.locale, title: s.title, summary: s.summary, body: s.body.trim() });
  return {
    slug, modes, category: zh.category, pinned: zh.pinned, order: String(zh.order),
    texts: { "zh-CN": text(zh), en: en ? text(en) : { locale: "en", title: "", summary: "", body: "" } },
  };
}

/** articleBody is a draft as the API takes it, the English text only when written; it throws a FormError on what is missing. */
export function articleBody(d: Draft, t: (key: string) => string) {
  if (!slugRE.test(d.slug)) throw new FormError(t("admin.content.badSlug"));
  const zh = d.texts["zh-CN"];
  if (!zh.title.trim() || !zh.body.trim()) throw new FormError(t("admin.content.needChinese"));
  const en = d.texts.en;
  const texts = [zh, ...(en.title.trim() || en.body.trim() ? [en] : [])];
  if (texts.some((x) => !x.title.trim() || !x.body.trim())) throw new FormError(t("admin.content.needBoth"));
  const order = Number(d.order);
  if (!Number.isInteger(order)) throw new FormError(t("admin.content.badOrder"));
  return { slug: d.slug, modes: d.modes, category: d.category, pinned: d.pinned, order, texts };
}

/** ArticlesPage lists a section's articles and the sites' own files, and opens the editor. */
export function ArticlesPage({ admin, section }: { admin: Admin; section: ListSection }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: articlesKey(section),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/articles", { params: { query: { section } } })).articles,
    refetchInterval: 30_000,
  });
  const [editing, setEditing] = useState<{ article: Article | null; seed?: Draft } | null>(null);
  const write = can(admin, "content.write");
  const columns = useMemo<ColumnDef<Article, unknown>[]>(
    () => [
      {
        id: "title", header: t("admin.content.title"),
        cell: ({ row: { original: a } }) => (
          <span className="flex flex-col">
            <span className="flex items-center gap-1.5 font-medium text-fg-1">
              {a.pinned && <Badge tone="brand">{t("admin.content.pinned")}</Badge>}
              <ModesBadge modes={a.modes} />
              {title(a)}
            </span>
            <span className="font-mono text-xs text-fg-3">
              {a.slug} · {t(`admin.content.categories.${a.category}`, { defaultValue: a.category })}
              {a.texts.some((x) => x.locale === "en") ? " · EN" : ""}
            </span>
          </span>
        ),
      },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <StatusBadge a={row.original} /> },
      {
        id: "publish", header: t("admin.content.publishAt"),
        cell: ({ row }) => (row.original.publish_at ? <TimeText value={row.original.publish_at} style="datetime" /> : "—"),
      },
      ...(section === "HELP"
        ? [{ id: "order", header: t("admin.content.order"), meta: { align: "right" }, cell: ({ row }) => row.original.order } as ColumnDef<Article, unknown>]
        : []),
      {
        id: "updated", header: t("admin.content.updated"),
        cell: ({ row }) => (
          <span className="flex flex-col text-xs">
            <TimeText value={row.original.updated_at} style="datetime" />
            <span className="text-fg-3">{row.original.updated_by}</span>
          </span>
        ),
      },
      {
        id: "site", header: "", meta: { align: "right" },
        cell: ({ row: { original: a } }) =>
          shown(a) && (
            <RowActions>
              <a
                className="inline-flex items-center gap-1 text-xs text-info-strong hover:underline"
                href={`${SITE}/${FILES[section]}/${a.slug}`}
                target="_blank"
                rel="noreferrer"
              >
                {t("admin.content.onSite")} <ExternalLink size={12} className="inline-block align-middle" />
              </a>
            </RowActions>
          ),
      },
    ],
    [t, section],
  );
  const known = useMemo(() => new Set((q.data ?? []).map((a) => a.slug)), [q.data]);
  return (
    <Page
      title={t(section === "ANNOUNCEMENT" ? "admin.nav.announcements" : "admin.nav.helpArticles")}
      help={t("admin.content.help")}
      actions={
        write && (
          <Button icon={<Plus size={14} />} onClick={() => setEditing({ article: null })} data-testid="article-new">
            {t(section === "ANNOUNCEMENT" ? "admin.content.newAnnouncement" : "admin.content.newHelp")}
          </Button>
        )
      }
    >
      <Card className="stagger">
        <DataTable
          columns={columns}
          data={q.data ?? []}
          getRowId={(a) => a.id}
          loading={q.isPending}
          error={q.error}
          onRetry={() => void q.refetch()}
          onRowClick={(a) => setEditing({ article: a })}
          empty={t("admin.content.none")}
          density="compact"
          aria-label={t(section === "ANNOUNCEMENT" ? "admin.nav.announcements" : "admin.nav.helpArticles")}
        />
      </Card>
      {q.isSuccess && <BundledFiles section={section} known={known} write={write} onCopy={(seed) => setEditing({ article: null, seed })} />}
      {editing && (
        <ArticleEditor
          admin={admin}
          key={editing.article?.id ?? `new:${editing.seed?.slug ?? ""}`}
          section={section}
          article={editing.article}
          seed={editing.seed}
          write={write}
          onClose={() => setEditing(null)}
          onSaved={(a) => setEditing({ article: a })}
        />
      )}
    </Page>
  );
}

/** ModesBadge marks an article for one of the exchange's modes (nothing for both). */
export function ModesBadge({ modes }: { modes: Modes }) {
  const { t } = useTranslation();
  if (modes === "BOTH") return null;
  return <Badge tone={modes === "TEST" ? "warn" : "info"}>{t(`admin.content.modes.${modes}`)}</Badge>;
}

export function StatusBadge({ a }: { a: Article }) {
  const { t } = useTranslation();
  if (a.status === "PUBLISHED" && !shown(a)) return <Badge tone="info">{t("admin.content.scheduled")}</Badge>;
  const tone = { DRAFT: "neutral", PUBLISHED: "success", ARCHIVED: "warn" } as const;
  return <Badge tone={tone[a.status]}>{t(`admin.content.status.${a.status}`)}</Badge>;
}

type Bundled = { slug: string; title: string; category: string; date: string; seed: Draft };

/**
 * BundledFiles lists the sites' own Markdown files of the section that no
 * article here has taken over: copied, one becomes a draft whose
 * publication replaces the file.
 */
function BundledFiles({ section, known, write, onCopy }: { section: ListSection; known: Set<string>; write: boolean; onCopy: (seed: Draft) => void }) {
  const { t } = useTranslation();
  const files = useQuery({
    queryKey: ["admin", "bundled-articles", section],
    staleTime: Infinity,
    queryFn: async () =>
      Promise.all(
        listSlugs(FILES[section]).map(async (slug): Promise<Bundled | null> => {
          const [zh, en] = await Promise.all([bundledSource(FILES[section], slug, "zh-CN"), bundledSource(FILES[section], slug, "en")]);
          if (!zh) return null;
          return { slug, title: zh.title, category: zh.category, date: zh.date, seed: seedOf(slug, zh, en) };
        }),
      ),
  });
  const rows = (files.data ?? []).filter((f): f is Bundled => f !== null && !known.has(f.slug));
  const columns = useMemo<ColumnDef<Bundled, unknown>[]>(
    () => [
      {
        id: "title", header: t("admin.content.title"),
        cell: ({ row: { original: f } }) => (
          <span className="flex flex-col">
            <span className="text-fg-1">{f.title}</span>
            <span className="font-mono text-xs text-fg-3">
              {f.slug} · {t(`admin.content.categories.${f.category}`, { defaultValue: f.category })}
            </span>
          </span>
        ),
      },
      section === "HELP"
        ? { id: "order", header: t("admin.content.order"), cell: ({ row }) => row.original.seed.order }
        : { id: "date", header: t("admin.content.fileDate"), cell: ({ row }) => row.original.date || "—" },
      {
        id: "copy", header: "", meta: { align: "right" },
        cell: ({ row }) =>
          write && (
            <Button size="sm" variant="ghost" icon={<Copy size={12} />} onClick={() => onCopy(row.original.seed)} data-testid={`bundled-copy-${row.original.slug}`}>
              {t("admin.content.copy")}
            </Button>
          ),
      },
    ],
    [t, write, onCopy, section],
  );
  if (files.isSuccess && rows.length === 0) return null;
  return (
    <Card title={t("admin.content.bundled")} extra={<span className="text-xs text-fg-3">{t("admin.content.bundledHelp")}</span>}>
      <DataTable columns={columns} data={rows} getRowId={(f) => f.slug} loading={files.isPending} error={files.error} density="compact" />
    </Card>
  );
}

function draftOf(section: Section, a: Article | null): Draft {
  const empty = (locale: Locale): Text => ({ locale, title: "", summary: "", body: "" });
  return {
    slug: a?.slug ?? "",
    modes: a?.modes ?? "BOTH",
    category: a?.category ?? (fixedSection(section) ? "" : CATEGORIES[section][0]!),
    pinned: a?.pinned ?? false,
    order: String(a?.order ?? 0),
    texts: {
      "zh-CN": a?.texts.find((x) => x.locale === "zh-CN") ?? empty("zh-CN"),
      en: a?.texts.find((x) => x.locale === "en") ?? empty("en"),
    },
  };
}

/**
 * ArticleEditor writes an article in both languages with a live preview,
 * and publishes it or takes it off. A fixed section's article keeps its
 * slug and has no category, pin or order; the home hero's summary is its
 * subtitle.
 */
export function ArticleEditor({
  admin, section, article, seed, write, onClose, onSaved,
}: {
  admin: Admin;
  section: Section;
  article: Article | null;
  seed?: Draft;
  write: boolean;
  onClose: () => void;
  onSaved: (a: Article) => void;
}) {
  const { t } = useTranslation();
  const [d, setD] = useState<Draft>(() => seed ?? draftOf(section, article));
  const [locale, setLocale] = useState<Locale>("zh-CN");
  const [view, setView] = useState("edit");
  // The preview shows the body as the sites do in a mode (:::test and
  // :::formal blocks filtered); an article for both modes can be seen in each.
  const [shownIn, setShownIn] = useState<ContentMode>(() => (article?.modes ?? seed?.modes) === "FORMAL" ? "formal" : "test");
  const previewMode: ContentMode = d.modes === "TEST" ? "test" : d.modes === "FORMAL" ? "formal" : shownIn;
  const [publishing, setPublishing] = useState(false);
  const [at, setAt] = useState("");
  const fixed = fixedSection(section);
  const text = d.texts[locale];
  const setText = (patch: Partial<Text>) => setD({ ...d, texts: { ...d.texts, [locale]: { ...text, ...patch } } });
  const save = async (reason: string) => {
    const res = article
      ? adminData(await adminApi.PUT("/admin/v1/articles/{id}", { params: { path: { id: article.id } }, body: { ...articleBody(d, t), version: article.version, reason } }))
      : adminData(await adminApi.POST("/admin/v1/articles", { body: { ...articleBody(d, t), section, reason } }));
    onSaved(res);
    return res;
  };
  const live = article !== null && shown(article);
  const heading = fixed
    ? t("admin.pages.editTitle", { page: t(`admin.pages.names.${d.slug}`, { defaultValue: d.slug }) })
    : t(section === "ANNOUNCEMENT" ? "admin.content.newAnnouncement" : "admin.content.newHelp");
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      width={920}
      title={article && !fixed ? title(article) : heading}
      description={
        article ? (
          <span className="flex items-center gap-2">
            <StatusBadge a={article} />
            <span className="font-mono text-xs">v{article.version}</span>
          </span>
        ) : (
          seed && t("admin.content.fromFile", { slug: seed.slug })
        )
      }
      actions={
        <span className="flex items-center gap-2">
          {write && (
            <DangerAction
              trigger={(open) => (
                <Button size="sm" variant={article?.status === "PUBLISHED" ? "secondary" : "primary"} onClick={open} data-testid="article-save">
                  {t(article ? "admin.common.save" : "admin.content.saveDraftButton")}
                </Button>
              )}
              danger={false}
              title={t(article ? "admin.content.saveTitle" : "admin.content.createTitle")}
              description={t(live ? "admin.content.saveLive" : "admin.content.saveDraft")}
              target={<span className="font-mono">{d.slug || "—"}</span>}
              confirmWord={d.slug || "slug"}
              run={save}
              success={t("admin.content.saved")}
              invalidate={[articlesKey(section)]}
            />
          )}
          {write && article && article.status !== "PUBLISHED" && (
            <Button size="sm" onClick={() => setPublishing(true)} data-testid="article-publish">
              {t("admin.content.publish")}
            </Button>
          )}
          {write && article?.status === "PUBLISHED" && (
            <DangerAction
              trigger={(open) => (
                <Button size="sm" variant="danger" onClick={open} data-testid="article-archive">
                  {t("admin.content.archive")}
                </Button>
              )}
              title={t("admin.content.archiveTitle")}
              description={t(section === "LEGAL" ? "admin.pages.archiveHint" : section === "HOME" ? "admin.pages.archiveHeroHint" : "admin.content.archiveHint")}
              target={<span className="font-mono">{article.slug}</span>}
              confirmWord={article.slug}
              run={async (reason) => {
                const res = adminData(
                  await adminApi.POST("/admin/v1/articles/{id}/archive", { params: { path: { id: article.id } }, body: { version: article.version, reason } }),
                );
                onSaved(res);
                return res;
              }}
              success={t("admin.content.archived")}
              invalidate={[articlesKey(section)]}
            />
          )}
        </span>
      }
    >
      <div className="flex flex-col gap-4">
        <ReadOnly admin={admin} perm="content.write" />
        <div className="flex flex-col gap-1.5">
          <span className="text-sm text-fg-2">{t("admin.content.modes.label")}</span>
          <Segmented
            size="sm"
            value={d.modes}
            disabled={!write}
            onValueChange={(v) => setD({ ...d, modes: v as Modes })}
            items={MODES.map((m) => ({ value: m, label: t(`admin.content.modes.${m}`) }))}
            aria-label={t("admin.content.modes.label")}
          />
          <p className="text-xs text-fg-3">{t("admin.content.modes.hint")}</p>
        </div>
        {fixed ? (
          <p className="text-xs text-fg-3">{t(section === "HOME" ? "admin.pages.heroHint" : "admin.pages.legalHint", { slug: d.slug })}</p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-[1fr_12rem_auto]">
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t("admin.content.slug")}
              <Input
                id="article-slug"
                value={d.slug}
                onValueChange={(v) => setD({ ...d, slug: v.toLowerCase() })}
                disabled={!write}
                autoComplete="off"
                error={d.slug && !slugRE.test(d.slug) ? t("admin.content.badSlug") : undefined}
                hint={!article && t("admin.content.slugHint")}
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t("admin.content.category")}
              <Select
                aria-label={t("admin.content.category")}
                value={d.category}
                disabled={!write}
                onValueChange={(v) => setD({ ...d, category: v })}
                options={[...new Set([...CATEGORIES[section], d.category])].map((c) => ({ value: c, label: t(`admin.content.categories.${c}`, { defaultValue: c }) }))}
              />
            </label>
            {section === "ANNOUNCEMENT" ? (
              <div className="flex h-10 items-center self-end">
                <Switch checked={d.pinned} disabled={!write} onCheckedChange={(v) => setD({ ...d, pinned: v })} label={t("admin.content.pinned")} />
              </div>
            ) : (
              <label className="flex w-28 flex-col gap-1.5 text-sm text-fg-2">
                {t("admin.content.order")}
                <Input value={d.order} inputMode="numeric" disabled={!write} onValueChange={(v) => setD({ ...d, order: v })} />
              </label>
            )}
          </div>
        )}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Segmented
            size="sm"
            value={locale}
            onValueChange={(v) => setLocale(v as Locale)}
            items={LOCALES.map((l) => ({ value: l, label: t(l === "zh-CN" ? "admin.content.zh" : "admin.content.en") }))}
            aria-label={t("admin.content.language")}
          />
          <Tabs
            variant="pill"
            size="sm"
            items={[
              { value: "edit", label: t("admin.content.edit") },
              { value: "preview", label: t("admin.content.preview") },
            ]}
            value={view}
            onValueChange={setView}
          />
        </div>
        {locale === "en" && <p className="text-xs text-fg-3">{t("admin.content.enHint")}</p>}
        {view === "edit" ? (
          <div className="flex flex-col gap-3">
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t("admin.content.title")}
              <Input id={`article-title-${locale}`} value={text.title} disabled={!write} maxLength={200} onValueChange={(v) => setText({ title: v })} />
            </label>
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t(section === "HOME" ? "admin.pages.subtitle" : "admin.content.summary")}
              <Input
                value={text.summary}
                disabled={!write}
                maxLength={500}
                placeholder={t(section === "HOME" ? "admin.pages.subtitleHint" : "admin.content.summaryHint")}
                onValueChange={(v) => setText({ summary: v })}
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm text-fg-2">
              {t("admin.content.body")}
              <textarea
                id={`article-body-${locale}`}
                value={text.body}
                disabled={!write}
                onChange={(e) => setText({ body: e.target.value })}
                rows={18}
                spellCheck={false}
                placeholder={t("admin.content.bodyHint")}
                className="w-full rounded-2 border border-line-1 bg-bg-1 px-3 py-2 font-mono text-sm leading-6 text-fg-1 outline-none focus-visible:border-brand"
              />
            </label>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2 text-xs text-fg-3">
              {d.modes === "BOTH" ? (
                <Segmented
                  size="sm"
                  value={shownIn}
                  onValueChange={(v) => setShownIn(v as ContentMode)}
                  items={[
                    { value: "test", label: t("admin.content.modes.TEST") },
                    { value: "formal", label: t("admin.content.modes.FORMAL") },
                  ]}
                  aria-label={t("admin.content.modes.previewIn")}
                />
              ) : null}
              <span>{t("admin.content.modes.previewAs", { mode: t(`admin.content.modes.${previewMode === "test" ? "TEST" : "FORMAL"}`) })}</span>
            </div>
            <article className="rounded-2 border border-line-1 p-5" data-testid="article-preview" data-mode={previewMode}>
              <h1 className="text-xl font-semibold text-fg-1">{text.title || "—"}</h1>
              {text.summary && <p className="mt-1 text-sm text-fg-3">{text.summary}</p>}
              <div className={cn(PROSE, "mt-3")}>
                <Markdown source={renderByMode(text.body, previewMode)} link={previewLink} />
              </div>
            </article>
          </div>
        )}
      </div>
      {publishing && article && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setPublishing(false)}
          danger={false}
          title={t("admin.content.publishTitle")}
          description={t("admin.content.publishHint")}
          target={<span className="font-mono">{article.slug}</span>}
          confirmWord={article.slug}
          run={async (reason) => {
            const when = at ? new Date(at) : null;
            if (when && Number.isNaN(when.getTime())) throw new FormError(t("admin.content.badTime"));
            const res = adminData(
              await adminApi.POST("/admin/v1/articles/{id}/publish", {
                params: { path: { id: article.id } },
                body: { version: article.version, reason, ...(when ? { publish_at: when.toISOString() } : {}) },
              }),
            );
            onSaved(res);
            return res;
          }}
          success={t(at ? "admin.content.scheduledOk" : "admin.content.published")}
          invalidate={[articlesKey(section)]}
        >
          <label className="flex flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.content.when")}
            <input
              type="datetime-local"
              value={at}
              onChange={(e) => setAt(e.target.value)}
              className="h-10 rounded-2 border border-line-1 bg-bg-1 px-3 text-sm text-fg-1"
              aria-label={t("admin.content.when")}
            />
            <span className="text-xs text-fg-3">{t("admin.content.whenHint")}</span>
          </label>
        </DangerAction>
      )}
    </Drawer>
  );
}
