import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { LEGAL_SLUGS } from "@exchange/core/content/loader";
import { Badge, Button, DataTable, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, FileCheck2, Pencil, Plus, Undo2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../../kit/actions";
import { TimeText } from "../../kit/format";
import { RowActions } from "../../kit/lists";
import { Card, Page } from "../../kit/Page";
import { useSiteMode } from "../../kit/profile";
import { ArticleEditor, articleBody, articlesKey, bundledSeed, FILES, shown, SITE, StatusBadge, type Article, type Draft } from "./articles";
import { ReadOnly } from "../../kit/ReadOnly";

// The fixed pages (design 2026-10-04 §4.4): the legal and information
// pages and the home page's hero have fixed addresses, and the sites bundle
// a draft of each. The exchange shows other pages in test mode than live
// (an article is for TEST, FORMAL or BOTH), so each page has a column per
// mode: what the sites show in it, and its article. An article published
// replaces the draft in its modes; taking it off hides a legal page (and
// the draft with it) there and gives the home page its built-in text;
// "publish the default" publishes the bundled draft as it is for the live
// exchange, which is what the launch checklist asks of terms, privacy and
// risk.

type Fixed = "LEGAL" | "HOME";
type Mode = "TEST" | "FORMAL";
const MODES: Mode[] = ["TEST", "FORMAL"];
const PAGES: { section: Fixed; slug: string }[] = [...LEGAL_SLUGS.map((slug) => ({ section: "LEGAL" as const, slug })), { section: "HOME", slug: "home-hero" }];
/** The pages the launch checklist wants published for the live exchange (its legal item). */
const REQUIRED = new Set(["terms", "privacy", "risk"]);

type Row = { section: Fixed; slug: string; articles: Article[]; seed: Draft };

/** forMode is the article the sites use for the page in mode: the one for that mode, else the one for both. */
const forMode = (r: Row, mode: Mode) => r.articles.find((a) => a.modes === mode) ?? r.articles.find((a) => a.modes === "BOTH") ?? null;

/** What the sites show in a mode: the console's article, the bundled draft (also while one is scheduled), or none. */
type OnSite = "override" | "scheduled" | "bundled" | "hidden" | "builtin" | "none";

function onSite(r: Row, mode: Mode, bundled: boolean): OnSite {
  const a = forMode(r, mode);
  if (a?.status === "ARCHIVED") return r.section === "HOME" ? "builtin" : "hidden";
  if (a && shown(a)) return "override";
  if (a?.status === "PUBLISHED") return "scheduled";
  return bundled ? "bundled" : "none";
}

const TONE: Record<OnSite, "success" | "info" | "neutral" | "warn" | "danger"> = {
  override: "success", scheduled: "info", bundled: "neutral", hidden: "warn", builtin: "warn", none: "danger",
};

const empty = (slug: string): Draft => ({
  slug, modes: "BOTH", category: "", pinned: false, order: "0",
  texts: {
    "zh-CN": { locale: "zh-CN", title: "", summary: "", body: "" }, "zh-TW": { locale: "zh-TW", title: "", summary: "", body: "" },
    en: { locale: "en", title: "", summary: "", body: "" },
  },
});

/** FixedPages lists the legal pages and the home hero with what the sites show of each in test mode and live, and edits, publishes or takes them off. */
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
          const seed = await bundledSeed(FILES[section], slug);
          if (seed) out.set(slug, seed);
        }),
      );
      return out;
    },
  });
  const rows = useMemo<Row[]>(() => {
    const articles = [...(legal.data ?? []), ...(home.data ?? [])];
    return PAGES.map(({ section, slug }) => ({
      section, slug,
      articles: articles.filter((a) => a.section === section && a.slug === slug),
      seed: drafts.data?.get(slug) ?? empty(slug),
    }));
  }, [legal.data, home.data, drafts.data]);
  const [editing, setEditing] = useState<{ row: Row; mode: Mode; article: Article | null } | null>(null);
  // The column the sites use now: the exchange's mode (the platform profile).
  const now = useSiteMode();

  const columns = useMemo<ColumnDef<Row, unknown>[]>(() => {
    const bundled = (r: Row) => drafts.data?.has(r.slug) ?? false;
    const modeColumn = (mode: Mode): ColumnDef<Row, unknown> => ({
      id: mode,
      header: () => (
        <span className="inline-flex items-center gap-1.5" data-testid={`fixed-mode-${mode.toLowerCase()}`} data-current={now === mode}>
          {t(`admin.content.modes.${mode}`)}
          {now === mode && <Badge tone="brand">{t("admin.pages.current")}</Badge>}
        </span>
      ),
      cell: ({ row: { original: r } }) => {
        const s = onSite(r, mode, bundled(r));
        const a = forMode(r, mode);
        const id = mode === "TEST" ? `test-${r.slug}` : r.slug;
        return (
          <span className="flex flex-col items-start gap-1.5" data-testid={`fixed-${mode.toLowerCase()}-${r.slug}`} data-onsite={s}>
            <Badge tone={TONE[s]}>{t(`admin.pages.shows.${s}`)}</Badge>
            {a ? (
              <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-fg-3">
                <StatusBadge a={a} />
                <span className="font-mono">v{a.version}</span>
                {a.modes === "BOTH" && <span>{t("admin.pages.forBoth")}</span>}
                <TimeText value={a.updated_at} style="datetime" />
              </span>
            ) : (
              <span className="text-xs text-fg-3">{t("admin.pages.noArticle")}</span>
            )}
            <RowActions>
              <Button
                size="sm"
                variant="ghost"
                icon={a ? <Pencil size={12} /> : <Plus size={12} />}
                onClick={() => setEditing({ row: r, mode, article: a })}
                disabled={!a && !write}
                data-testid={`fixed-edit-${id}`}
              >
                {t(a ? (write ? "admin.pages.edit" : "admin.pages.read") : "admin.pages.write")}
              </Button>
              {write && mode === "FORMAL" && bundled(r) && a?.status !== "PUBLISHED" && <PublishDefault row={r} article={a} />}
              {write && a?.status === "PUBLISHED" && <TakeOff row={r} article={a} id={id} />}
            </RowActions>
          </span>
        );
      },
    });
    return [
      {
        id: "page", header: t("admin.pages.page"),
        cell: ({ row: { original: r } }) => (
          <span className="flex flex-col" data-testid={`fixed-${r.slug}`} data-onsite={onSite(r, "FORMAL", bundled(r))}>
            <span className="font-medium text-fg-1">{t(`admin.pages.names.${r.slug}`)}</span>
            <span className="font-mono text-xs text-fg-3">{r.section === "HOME" ? "/ · home-hero" : `/legal/${r.slug}`}</span>
          </span>
        ),
      },
      ...MODES.map(modeColumn),
      {
        id: "launch", header: t("admin.pages.launch"),
        cell: ({ row: { original: r } }) => {
          if (!REQUIRED.has(r.slug)) return <span className="text-fg-3">—</span>;
          const live = forMode(r, "FORMAL");
          return live && shown(live) ? (
            <Badge tone="success">{t("admin.pages.requiredOk")}</Badge>
          ) : (
            <Badge tone="danger">{t("admin.pages.requiredMissing")}</Badge>
          );
        },
      },
      {
        id: "site", header: "", meta: { align: "right" },
        cell: ({ row: { original: r } }) => (
          <a
            className="inline-flex items-center gap-1 text-xs text-info-strong hover:underline"
            href={r.section === "HOME" ? `${SITE}/` : `${SITE}/legal/${r.slug}`}
            target="_blank"
            rel="noreferrer"
          >
            {t("admin.pages.view")} <ExternalLink size={12} className="inline-block align-middle" />
          </a>
        ),
      },
    ];
  }, [t, write, drafts.data, now]);

  const error = legal.error ?? home.error;
  return (
    <Page title={t("admin.nav.fixedPages")} help={t("admin.pages.help")}>
      <ReadOnly admin={admin} perm="content.write" />
      {now && (
        <p className="text-sm text-fg-2" data-testid="fixed-now">
          {t(now === "TEST" ? "admin.pages.nowTest" : "admin.pages.nowFormal")}
        </p>
      )}
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
          key={editing.article?.id ?? `new:${editing.mode}:${editing.row.slug}`}
          section={editing.row.section}
          article={editing.article}
          seed={editing.article ? undefined : { ...editing.row.seed, modes: editing.mode }}
          write={write}
          onClose={() => setEditing(null)}
          onSaved={(a) => setEditing({ ...editing, article: a })}
        />
      )}
    </Page>
  );
}

/**
 * PublishDefault publishes the bundled draft (both languages) as the live
 * exchange's page: a new article for FORMAL, or the page's live article
 * (a draft, or taken off) rewritten with it for FORMAL; then publishes it.
 */
function PublishDefault({ row, article }: { row: Row; article: Article | null }) {
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
          {article && (
            <span className="text-warn-strong">
              {t(article.modes === "BOTH" ? "admin.pages.publishDefaultBoth" : "admin.pages.publishDefaultReplace", { status: t(`admin.content.status.${article.status}`) })}
            </span>
          )}
        </span>
      }
      target={<span className="font-mono">{row.slug}</span>}
      confirmWord={row.slug}
      run={async (reason) => {
        const body = articleBody({ ...row.seed, modes: "FORMAL" }, t);
        const a = article
          ? adminData(await adminApi.PUT("/admin/v1/articles/{id}", { params: { path: { id: article.id } }, body: { ...body, version: article.version, reason } }))
          : adminData(await adminApi.POST("/admin/v1/articles", { body: { ...body, section: row.section, reason } }));
        return adminData(await adminApi.POST("/admin/v1/articles/{id}/publish", { params: { path: { id: a.id } }, body: { version: a.version, reason } }));
      }}
      success={t("admin.pages.publishedDefault", { page })}
      invalidate={[articlesKey(row.section)]}
    />
  );
}

/** TakeOff archives a published page: a legal page leaves the sites in the article's modes, the hero goes back to the sites' own text. */
function TakeOff({ row, article, id }: { row: Row; article: Article; id: string }) {
  const { t } = useTranslation();
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="ghost" icon={<Undo2 size={12} />} onClick={open} data-testid={`fixed-archive-${id}`}>
          {t("admin.pages.archive")}
        </Button>
      )}
      title={t("admin.pages.archiveTitle", { page: t(`admin.pages.names.${row.slug}`) })}
      description={
        <span className="flex flex-col gap-1">
          <span>{t(row.section === "HOME" ? "admin.pages.archiveHeroHint" : "admin.pages.archiveHint")}</span>
          {article.modes === "BOTH" && <span className="text-warn-strong">{t("admin.pages.archiveBoth")}</span>}
        </span>
      }
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
