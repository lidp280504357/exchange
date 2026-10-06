import { errorText, formatTime, routes, useSettings } from "@exchange/core";
import { neighbours, useArticle, useArticles, type Article as ArticleData, type ArticleMeta, type ContentSection } from "@exchange/core/content/index";
import { Badge, Button, EmptyState, ErrorState, Skeleton, SkeletonLines, Tag } from "@exchange/ui";
import { ArrowLeft, ArrowRight, BookOpen, Languages, Megaphone, Pin } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { relatedArticles } from "./logic";
import { ListLink, Prose, SimNotice, Toc, useHashScroll } from "./parts";

// An announcement (/announcements/:slug) or a help article (/help/:slug)
// on the phone: the Markdown body with a folding table of contents, the
// previous and next articles, and more of the same kind. An English
// reader sees the Chinese text, marked, where no English exists.

export default function Article({ section }: { section: ContentSection }) {
  const { t } = useTranslation();
  const { slug = "" } = useParams();
  const article = useArticle(section, slug);
  const list = useArticles(section);
  const a = article.data;
  const help = section === "help";
  const listPath = help ? routes.help : routes.announcements;
  usePageHeader({ title: help ? t("mContent.help.title") : t("mContent.announcements.title"), back: listPath }, [help, t]);
  useHashScroll(Boolean(a));

  let body: ReactNode;
  if (article.isPending) body = <ArticleSkeleton />;
  else if (article.isError) body = <ErrorState message={errorText(article.error)} onRetry={() => void article.refetch()} />;
  else if (!a)
    body = (
      <EmptyState
        title={t("mContent.article.notFound")}
        description={t("mContent.article.notFoundHint")}
        action={
          <Button asChild size="lg" variant="secondary" icon={<ArrowLeft size={16} />}>
            <Link to={listPath}>{help ? t("mContent.help.back") : t("mContent.announcements.back")}</Link>
          </Button>
        }
      />
    );
  else body = <ArticleBody article={a} list={list.data ?? []} listPath={listPath} />;

  return <div className="px-4 pb-10 pt-4">{body}</div>;
}

function ArticleBody({ article: a, list, listPath }: { article: ArticleData; list: readonly ArticleMeta[]; listPath: string }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const help = a.section === "help";
  const { prev, next } = neighbours(list, a.slug);
  const more = help ? relatedArticles(list, a.slug) : list.filter((x) => x.slug !== a.slug).slice(0, 3);
  const pathOf = (m: ArticleMeta) => (help ? routes.helpArticle(m.slug) : routes.announcement(m.slug));
  const date = (d: string) => formatTime(d, "date", locale, "UTC");

  return (
    <article key={`${a.slug}.${a.locale}`} className="animate-fade-in">
      <header>
        <div className="flex flex-wrap items-center gap-2 text-xs text-fg-3">
          {a.pinned && (
            <Badge tone="brand" icon={<Pin size={12} />}>
              {t("mContent.announcements.pinned")}
            </Badge>
          )}
          <Tag>{t(`mContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>
          {a.date && (
            <time dateTime={a.date} className="tabular-nums">
              {t("mContent.article.published", { date: date(a.date) })}
            </time>
          )}
        </div>
        <h1 className="mt-3 text-xl font-semibold leading-snug text-fg-1">{a.title}</h1>
        {a.summary && <p className="mt-2 text-base leading-relaxed text-fg-2">{a.summary}</p>}
      </header>

      {a.fallback && (
        <div role="note" className="mt-4 flex items-start gap-2 rounded-2 border border-info/30 bg-info/10 px-3 py-2.5 text-xs leading-relaxed text-info">
          <Languages size={14} className="mt-0.5 shrink-0" />
          {t(locale === "zh-TW" ? "mContent.article.fallbackTraditional" : "mContent.article.fallback")}
        </div>
      )}
      <SimNotice className="mt-4" />
      <Toc items={a.doc.toc} className="mt-4" />

      <Prose doc={a.doc} className="mt-5" />

      {(prev || next) && (
        <nav aria-label={`${t("mContent.article.prev")} / ${t("mContent.article.next")}`} className="mt-10 flex flex-col gap-2 border-t border-line-1 pt-5">
          {prev && (
            <Link to={pathOf(prev)} className="flex min-h-14 items-center gap-3 rounded-3 border border-line-1 bg-bg-1 px-4 py-2 transition-colors active:bg-bg-2">
              <ArrowLeft size={16} className="shrink-0 text-fg-3" />
              <span className="min-w-0">
                <span className="block text-xs text-fg-3">{t("mContent.article.prev")}</span>
                <span className="block truncate text-sm font-medium text-fg-1">{prev.title}</span>
              </span>
            </Link>
          )}
          {next && (
            <Link
              to={pathOf(next)}
              className="flex min-h-14 items-center justify-end gap-3 rounded-3 border border-line-1 bg-bg-1 px-4 py-2 text-right transition-colors active:bg-bg-2"
            >
              <span className="min-w-0">
                <span className="block text-xs text-fg-3">{t("mContent.article.next")}</span>
                <span className="block truncate text-sm font-medium text-fg-1">{next.title}</span>
              </span>
              <ArrowRight size={16} className="shrink-0 text-fg-3" />
            </Link>
          )}
        </nav>
      )}

      {more.length > 0 && (
        <section className="mt-8">
          <h2 className="mb-2 flex items-center gap-2 text-base font-semibold text-fg-1">
            <span className="text-brand">{help ? <BookOpen size={16} /> : <Megaphone size={16} />}</span>
            {help ? t("mContent.help.related") : t("mContent.announcements.others")}
          </h2>
          <ul className="divide-y divide-line-1 overflow-hidden rounded-3 border border-line-1 bg-bg-1">
            {more.map((o) => (
              <li key={o.slug}>
                <ListLink
                  to={pathOf(o)}
                  title={o.title}
                  meta={
                    help ? undefined : (
                      <time dateTime={o.date} className="tabular-nums">
                        {date(o.date)}
                      </time>
                    )
                  }
                />
              </li>
            ))}
          </ul>
          <Link to={listPath} className="mt-1 inline-flex h-tap items-center gap-1 text-sm text-brand">
            {help ? t("mContent.help.back") : t("mContent.announcements.back")}
            <ArrowRight size={14} />
          </Link>
        </section>
      )}
    </article>
  );
}

function ArticleSkeleton() {
  return (
    <div>
      <Skeleton className="h-4 w-40" />
      <Skeleton className="mt-4 h-7 w-4/5" />
      <SkeletonLines lines={2} className="mt-3" />
      <Skeleton className="mt-5 h-12 w-full rounded-3" />
      <SkeletonLines lines={8} className="mt-6" />
    </div>
  );
}
