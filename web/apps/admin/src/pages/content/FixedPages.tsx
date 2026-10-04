import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { bundledSource, LEGAL_SLUGS } from "@exchange/core/content/loader";
import { Badge, Button, DataTable, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, FileCheck2, Pencil, Undo2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../../kit/actions";
import { TimeText } from "../../kit/format";
import { RowActions } from "../../kit/lists";
import { Card, Page } from "../../kit/Page";
import { ArticleEditor, articleBody, articlesKey, FILES, seedOf, shown, SITE, StatusBadge, type Article, type Draft } from "./articles";
import { ReadOnly } from "../../kit/ReadOnly";

// The fixed pages (design 2026-10-04 §4.4): the legal and information
// pages and the home page's hero have fixed addresses, and the sites bundle
// a draft of each. An article published here replaces the draft; taking it
// off hides a legal page (and the draft with it) and gives the home page
// its built-in text; "publish the default" publishes the bundled draft as
// it is, which is what the launch checklist asks of terms, privacy and risk.

type Fixed = "LEGAL" | "HOME";
const PAGES: { section: Fixed; slug: string }[] = [...LEGAL_SLUGS.map((slug) => ({ section: "LEGAL" as const, slug })), { section: "HOME", slug: "home-hero" }];
/** The pages the launch checklist wants published (its legal item). */
const REQUIRED = new Set(["terms", "privacy", "risk"]);

type Row = { section: Fixed; slug: string; article: Article | null; seed: Draft };

/** What the sites show now: the console's article, the bundled draft (also while one is scheduled), or none. */
type OnSite = "override" | "scheduled" | "bundled" | "hidden" | "builtin" | "none";

function onSite(r: Row, bundled: boolean): OnSite {
  const a = r.article;
  if (a?.status === "ARCHIVED") return r.section === "HOME" ? "builtin" : "hidden";
  if (a && shown(a)) return "override";
  if (a?.status === "PUBLISHED") return "scheduled";
  return bundled ? "bundled" : "none";
}

const TONE: Record<OnSite, "success" | "info" | "neutral" | "warn" | "danger"> = {
  override: "success", scheduled: "info", bundled: "neutral", hidden: "warn", builtin: "warn", none: "danger",
};

const empty = (slug: string): Draft => ({
  slug, category: "", pinned: false, order: "0",
  texts: { "zh-CN": { locale: "zh-CN", title: "", summary: "", body: "" }, en: { locale: "en", title: "", summary: "", body: "" } },
});

/** FixedPages lists the legal pages and the home hero with what the sites show of each, and edits, publishes or takes them off. */
export default function FixedPages({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const write = can(admin, "content.write");
  const legal = useQuery({
    queryKey: articlesKey("LEGAL"),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/articles", { params: { query: { section: "LEGAL" } } })).articles,
    refetchInterval: 30_000,
  });
  const home = useQuery({
    queryKey: articlesKey("HOME"),
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/articles", { params: { query: { section: "HOME" } } })).articles,
    refetchInterval: 30_000,
  });
  const drafts = useQuery({
    queryKey: ["admin", "bundled-articles", "fixed"],
    staleTime: Infinity,
    queryFn: async () => {
      const out = new Map<string, Draft>();
      await Promise.all(
        PAGES.map(async ({ section, slug }) => {
          const [zh, en] = await Promise.all([bundledSource(FILES[section], slug, "zh-CN"), bundledSource(FILES[section], slug, "en")]);
          if (zh) out.set(slug, seedOf(slug, zh, en));
        }),
      );
      return out;
    },
  });
  const rows = useMemo<Row[]>(() => {
    const articles = [...(legal.data ?? []), ...(home.data ?? [])];
    return PAGES.map(({ section, slug }) => ({
      section, slug,
      article: articles.find((a) => a.section === section && a.slug === slug) ?? null,
      seed: drafts.data?.get(slug) ?? empty(slug),
    }));
  }, [legal.data, home.data, drafts.data]);
  const [editing, setEditing] = useState<Row | null>(null);

  const columns = useMemo<ColumnDef<Row, unknown>[]>(() => {
    const bundled = (r: Row) => drafts.data?.has(r.slug) ?? false;
    return [
      {
        id: "page", header: t("admin.pages.page"),
        cell: ({ row: { original: r } }) => (
          <span className="flex flex-col" data-testid={`fixed-${r.slug}`} data-onsite={onSite(r, bundled(r))}>
            <span className="font-medium text-fg-1">{t(`admin.pages.names.${r.slug}`)}</span>
            <span className="font-mono text-xs text-fg-3">{r.section === "HOME" ? "/ · home-hero" : `/legal/${r.slug}`}</span>
          </span>
        ),
      },
      {
        id: "onSite", header: t("admin.pages.onSite"),
        cell: ({ row: { original: r } }) => {
          const s = onSite(r, bundled(r));
          return <Badge tone={TONE[s]}>{t(`admin.pages.shows.${s}`)}</Badge>;
        },
      },
      {
        id: "article", header: t("admin.pages.article"),
        cell: ({ row: { original: r } }) =>
          r.article ? (
            <span className="flex items-center gap-2">
              <StatusBadge a={r.article} />
              <span className="font-mono text-xs text-fg-3">v{r.article.version}</span>
            </span>
          ) : (
            <span className="text-xs text-fg-3">{t("admin.pages.noArticle")}</span>
          ),
      },
      {
        id: "launch", header: t("admin.pages.launch"),
        cell: ({ row: { original: r } }) =>
          REQUIRED.has(r.slug) ? (
            r.article?.status === "PUBLISHED" ? (
              <Badge tone="success">{t("admin.pages.requiredOk")}</Badge>
            ) : (
              <Badge tone="danger">{t("admin.pages.requiredMissing")}</Badge>
            )
          ) : (
            <span className="text-fg-3">—</span>
          ),
      },
      {
        id: "updated", header: t("admin.pages.updated"),
        cell: ({ row: { original: r } }) =>
          r.article ? (
            <span className="flex flex-col text-xs">
              <TimeText value={r.article.updated_at} style="datetime" />
              <span className="text-fg-3">{r.article.updated_by}</span>
            </span>
          ) : (
            <span className="text-fg-3">—</span>
          ),
      },
      {
        id: "actions", header: "", meta: { align: "right" },
        cell: ({ row: { original: r } }) => (
          <RowActions>
            <Button size="sm" variant="ghost" icon={<Pencil size={12} />} onClick={() => setEditing(r)} data-testid={`fixed-edit-${r.slug}`}>
              {t(write ? "admin.pages.edit" : "admin.pages.read")}
            </Button>
            {write && bundled(r) && r.article?.status !== "PUBLISHED" && <PublishDefault row={r} />}
            {write && r.article?.status === "PUBLISHED" && <TakeOff row={r} article={r.article} />}
            <a
              className="inline-flex items-center gap-1 text-xs text-info-strong hover:underline"
              href={r.section === "HOME" ? `${SITE}/` : `${SITE}/legal/${r.slug}`}
              target="_blank"
              rel="noreferrer"
            >
              {t("admin.pages.view")} <ExternalLink size={12} className="inline-block align-middle" />
            </a>
          </RowActions>
        ),
      },
    ];
  }, [t, write, drafts.data]);

  const error = legal.error ?? home.error;
  return (
    <Page title={t("admin.nav.fixedPages")} help={t("admin.pages.help")}>
      <ReadOnly admin={admin} perm="content.write" />
      <Card className="stagger">
        <DataTable
          columns={columns}
          data={rows}
          getRowId={(r) => r.slug}
          loading={legal.isPending || home.isPending}
          error={error}
          onRetry={() => void Promise.all([legal.refetch(), home.refetch()])}
          density="compact"
          aria-label={t("admin.nav.fixedPages")}
        />
      </Card>
      {editing && (
        <ArticleEditor
          admin={admin}
          key={editing.article?.id ?? `new:${editing.slug}`}
          section={editing.section}
          article={editing.article}
          seed={editing.article ? undefined : editing.seed}
          write={write}
          onClose={() => setEditing(null)}
          onSaved={(a) => setEditing({ ...editing, article: a })}
        />
      )}
    </Page>
  );
}

/**
 * PublishDefault publishes the bundled draft (both languages) as the page's
 * article: a new one, or the existing draft or taken-off article rewritten
 * with it; then publishes it.
 */
function PublishDefault({ row }: { row: Row }) {
  const { t } = useTranslation();
  const page = t(`admin.pages.names.${row.slug}`);
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="ghost" icon={<FileCheck2 size={12} />} onClick={open} data-testid={`fixed-default-${row.slug}`}>
          {t("admin.pages.publishDefault")}
        </Button>
      )}
      danger={false}
      title={t("admin.pages.publishDefaultTitle", { page })}
      description={
        <span className="flex flex-col gap-1">
          <span>{t("admin.pages.publishDefaultHint")}</span>
          {row.article && <span className="text-warn-strong">{t("admin.pages.publishDefaultReplace", { status: t(`admin.content.status.${row.article.status}`) })}</span>}
        </span>
      }
      target={<span className="font-mono">{row.slug}</span>}
      confirmWord={row.slug}
      run={async (reason) => {
        const body = articleBody(row.seed, t);
        const a = row.article
          ? adminData(await adminApi.PUT("/admin/v1/articles/{id}", { params: { path: { id: row.article.id } }, body: { ...body, version: row.article.version, reason } }))
          : adminData(await adminApi.POST("/admin/v1/articles", { body: { ...body, section: row.section, reason } }));
        return adminData(await adminApi.POST("/admin/v1/articles/{id}/publish", { params: { path: { id: a.id } }, body: { version: a.version, reason } }));
      }}
      success={t("admin.pages.publishedDefault", { page })}
      invalidate={[articlesKey(row.section)]}
    />
  );
}

/** TakeOff archives the page's published article: a legal page leaves the sites, the hero goes back to the sites' own text. */
function TakeOff({ row, article }: { row: Row; article: Article }) {
  const { t } = useTranslation();
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="ghost" icon={<Undo2 size={12} />} onClick={open} data-testid={`fixed-archive-${row.slug}`}>
          {t("admin.pages.archive")}
        </Button>
      )}
      title={t("admin.pages.archiveTitle", { page: t(`admin.pages.names.${row.slug}`) })}
      description={t(row.section === "HOME" ? "admin.pages.archiveHeroHint" : "admin.pages.archiveHint")}
      target={<span className="font-mono">{row.slug}</span>}
      confirmWord={row.slug}
      run={async (reason) =>
        adminData(await adminApi.POST("/admin/v1/articles/{id}/archive", { params: { path: { id: article.id } }, body: { version: article.version, reason } }))
      }
      success={t("admin.content.archived")}
      invalidate={[articlesKey(row.section)]}
    />
  );
}
