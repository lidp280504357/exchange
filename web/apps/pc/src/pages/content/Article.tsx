import { errorText, formatTime, routes, useSettings } from "@exchange/core";
import { neighbours, useArticle, useArticles, type Article as ArticleData, type ArticleMeta, type ContentSection } from "@exchange/core/content/index";
import { Badge, Button, EmptyState, ErrorState, Skeleton, SkeletonLines, Tag } from "@exchange/ui";
import { ArrowLeft, ArrowRight, Languages, Megaphone, Pin } from "lucide-react";
import { motion } from "motion/react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";
import { usePageTitle } from "../markets/hooks";
import { Breadcrumb, HelpNav, Prose, SimNotice, Toc, useHashScroll } from "./parts";

// An announcement (/announcements/:slug) or a help article (/help/:slug):
// the Markdown body with a table of contents that follows the reading,
// previous and next articles, and for help the category sidebar. An
// English reader sees the Chinese text, marked, where no English exists.

export default function Article({ section }: { section: ContentSection }) {
  const { t } = useTranslation();
  const { slug = "" } = useParams();
  const article = useArticle(section, slug);
  const list = useArticles(section);
  const a = article.data;
  usePageTitle(a?.title);
  useHashScroll(Boolean(a));

  const help = section === "help";
  const listPath = help ? routes.help : routes.announcements;
  const listLabel = help ? t("pcContent.help.title") : t("pcContent.announcements.title");
  const sorted = list.data ?? [];

  let body: ReactNode;
  if (article.isPending) body = <ArticleSkeleton />;
  else if (article.isError) body = <ErrorState message={errorText(article.error)} onRetry={() => void article.refetch()} />;
  else if (!a)
    body = (
      <EmptyState
        title={t("pcContent.article.notFound")}
        description={t("pcContent.article.notFoundHint")}
        action={
          <Button asChild size="sm" variant="secondary" icon={<ArrowLeft size={14} />}>
            <Link to={listPath}>{help ? t("pcContent.help.back") : t("pcContent.announcements.back")}</Link>
          </Button>
        }
      />
    );
  else body = <ArticleBody article={a} list={sorted} listPath={listPath} />;

  return (
    <div className="mx-auto max-w-[1440px] px-6 py-8">
      <Breadcrumb to={listPath} label={listLabel} current={a?.title} />
      <div
        className={
          help
            ? "mt-6 grid grid-cols-[210px_minmax(0,1fr)] gap-8 xl:grid-cols-[230px_minmax(0,1fr)_220px] xl:gap-10"
            : "mt-6 grid grid-cols-[minmax(0,1fr)] gap-10 xl:grid-cols-[minmax(0,1fr)_240px]"
        }
      >
        {help && (
          <aside className="sticky top-20 max-h-[calc(100dvh-6rem)] self-start overflow-y-auto pb-6">
            {list.isPending ? <SkeletonLines lines={8} /> : <HelpNav articles={sorted} current={slug} />}
          </aside>
        )}
        <div className="min-w-0">{body}</div>
        {a && (
          <aside className="sticky top-20 hidden self-start xl:block">
            <Toc items={a.doc.toc} />
          </aside>
        )}
      </div>
    </div>
  );
}

function ArticleBody({ article: a, list, listPath }: { article: ArticleData; list: readonly ArticleMeta[]; listPath: string }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const help = a.section === "help";
  const { prev, next } = neighbours(list, a.slug);
  const others = help ? [] : list.filter((x) => x.slug !== a.slug).slice(0, 3);
  const pathOf = (m: ArticleMeta) => (help ? routes.helpArticle(m.slug) : routes.announcement(m.slug));
  return (
    <motion.article key={`${a.slug}.${a.locale}`} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.2 }} className="max-w-3xl">
      <header>
        <div className="flex flex-wrap items-center gap-2 text-sm text-fg-3">
          {a.pinned && (
            <Badge tone="brand" icon={<Pin size={12} />}>
              {t("pcContent.announcements.pinned")}
            </Badge>
          )}
          <Tag>{t(`pcContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>
          {a.date && (
            <time dateTime={a.date} className="tabular-nums">
              {t("pcContent.article.published", { date: formatTime(a.date, "date", locale, "UTC") })}
            </time>
          )}
        </div>
        <h1 className="mt-3 text-2xl font-semibold leading-tight text-fg-1">{a.title}</h1>
        {a.summary && <p className="mt-3 text-md leading-relaxed text-fg-2">{a.summary}</p>}
      </header>

      {a.fallback && (
        <div role="note" className="mt-5 flex items-center gap-2 rounded-2 border border-info/30 bg-info/10 px-4 py-2.5 text-sm text-info">
          <Languages size={16} className="shrink-0" />
          {t("pcContent.article.fallback")}
        </div>
      )}
      <SimNotice className="mt-5" />

      {a.doc.toc.length > 1 && (
        <details className="mt-5 rounded-2 border border-line-1 bg-bg-1 px-4 py-3 xl:hidden">
          <summary className="cursor-pointer text-sm font-medium text-fg-2">{t("pcContent.article.toc")}</summary>
          <Toc items={a.doc.toc} className="mt-3" />
        </details>
      )}

      <Prose doc={a.doc} className="mt-6" />

      {(prev || next) && (
        <nav aria-label={`${t("pcContent.article.prev")} / ${t("pcContent.article.next")}`} className="mt-12 grid grid-cols-2 gap-4 border-t border-line-1 pt-6">
          {prev ? (
            <Link to={pathOf(prev)} className="group min-w-0 rounded-3 border border-line-1 bg-bg-1 p-4 transition-colors hover:border-line-2">
              <span className="flex items-center gap-1 text-xs text-fg-3">
                <ArrowLeft size={12} className="transition-transform group-hover:-translate-x-0.5" />
                {t("pcContent.article.prev")}
              </span>
              <span className="mt-1 block truncate text-sm font-medium text-fg-1 group-hover:text-brand">{prev.title}</span>
            </Link>
          ) : (
            <span />
          )}
          {next ? (
            <Link to={pathOf(next)} className="group min-w-0 rounded-3 border border-line-1 bg-bg-1 p-4 text-right transition-colors hover:border-line-2">
              <span className="flex items-center justify-end gap-1 text-xs text-fg-3">
                {t("pcContent.article.next")}
                <ArrowRight size={12} className="transition-transform group-hover:translate-x-0.5" />
              </span>
              <span className="mt-1 block truncate text-sm font-medium text-fg-1 group-hover:text-brand">{next.title}</span>
            </Link>
          ) : (
            <span />
          )}
        </nav>
      )}

      {others.length > 0 && (
        <section className="mt-10">
          <h2 className="flex items-center gap-2 text-base font-medium text-fg-1">
            <Megaphone size={16} className="text-brand" />
            {t("pcContent.announcements.others")}
          </h2>
          <ul className="mt-3 divide-y divide-line-1 rounded-3 border border-line-1 bg-bg-1">
            {others.map((o) => (
              <li key={o.slug}>
                <Link to={pathOf(o)} className="flex items-center gap-4 px-4 py-3 text-sm transition-colors hover:bg-bg-2">
                  <span className="min-w-0 flex-1 truncate text-fg-1">{o.title}</span>
                  <time dateTime={o.date} className="shrink-0 text-xs text-fg-3 tabular-nums">
                    {formatTime(o.date, "date", locale, "UTC")}
                  </time>
                </Link>
              </li>
            ))}
          </ul>
          <Link to={listPath} className="mt-3 inline-flex items-center gap-1 text-sm text-brand hover:underline">
            {t("pcContent.announcements.back")}
            <ArrowRight size={14} />
          </Link>
        </section>
      )}
    </motion.article>
  );
}

function ArticleSkeleton() {
  return (
    <div className="max-w-3xl">
      <Skeleton className="h-4 w-40" />
      <Skeleton className="mt-4 h-8 w-3/4" />
      <SkeletonLines lines={2} className="mt-4" />
      <Skeleton className="mt-6 h-10 w-full" />
      <SkeletonLines lines={8} className="mt-8" />
    </div>
  );
}
