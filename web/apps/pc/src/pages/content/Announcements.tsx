import { errorText, formatTime, routes, useSettings } from "@exchange/core";
import { useArticles } from "@exchange/core/content/index";
import { Badge, EmptyState, ErrorState, Skeleton, SkeletonLines, Tag, cn, listItem } from "@exchange/ui";
import { ArrowRight, BookOpen, LifeBuoy, Megaphone, Pin } from "lucide-react";
import { motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { usePageTitle } from "../markets/hooks";
import { PageHeader, SimNotice } from "./parts";

// /announcements (design §6.2): pinned notices first, then the newest,
// from the Markdown files in the repository.

export default function Announcements() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  usePageTitle(t("pcContent.announcements.title"));
  const q = useArticles("announcements");
  const help = useArticles("help");
  const list = q.data ?? [];

  return (
    <div className="mx-auto max-w-[1440px] px-6 py-8">
      <PageHeader
        icon={<Megaphone size={22} />}
        title={t("pcContent.announcements.title")}
        subtitle={t("pcContent.announcements.subtitle")}
        extra={q.data && <span className="text-sm text-fg-3">{t("pcContent.announcements.count", { count: list.length })}</span>}
      />
      <SimNotice className="mt-6" />

      <div className="mt-6 grid grid-cols-[minmax(0,1fr)_300px] gap-6 xl:grid-cols-[minmax(0,1fr)_340px]">
        <ol className="flex min-w-0 flex-col gap-4">
          {q.isPending ? (
            [0, 1, 2].map((i) => (
              <li key={i} className="rounded-3 border border-line-1 bg-bg-1 p-6">
                <Skeleton className="h-3 w-32" />
                <Skeleton className="mt-4 h-5 w-2/3" />
                <SkeletonLines lines={2} className="mt-3" />
              </li>
            ))
          ) : q.isError ? (
            <li className="rounded-3 border border-line-1 bg-bg-1">
              <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
            </li>
          ) : list.length === 0 ? (
            <li className="rounded-3 border border-line-1 bg-bg-1">
              <EmptyState title={t("pcContent.announcements.empty")} />
            </li>
          ) : (
            list.map((a, i) => (
              <motion.li key={a.slug} variants={listItem} initial="initial" animate="animate" custom={i}>
                <Link
                  to={routes.announcement(a.slug)}
                  className={cn(
                    "group relative block overflow-hidden rounded-3 border bg-bg-1 p-6 transition-[transform,border-color,box-shadow] duration-[var(--t-base)] hover:-translate-y-0.5 hover:shadow-pop",
                    a.pinned ? "border-brand/40 hover:border-brand" : "border-line-1 hover:border-line-2",
                  )}
                >
                  {a.pinned && <span aria-hidden className="absolute inset-y-0 left-0 w-1 bg-brand" />}
                  <div className="flex items-center gap-2 text-xs text-fg-3">
                    {a.pinned && (
                      <Badge tone="brand" icon={<Pin size={12} />}>
                        {t("pcContent.announcements.pinned")}
                      </Badge>
                    )}
                    <Tag>{t(`pcContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>
                    <time dateTime={a.date} className="ml-auto tabular-nums">
                      {formatTime(a.date, "date", locale, "UTC")}
                    </time>
                  </div>
                  <h2 className="mt-3 text-lg font-semibold text-fg-1 transition-colors group-hover:text-brand">{a.title}</h2>
                  <p className="mt-2 line-clamp-2 text-sm leading-relaxed text-fg-3">{a.summary}</p>
                  <span className="mt-4 flex items-center gap-1 text-sm text-brand">
                    {t("pcContent.announcements.readMore")}
                    <ArrowRight size={14} className="transition-transform group-hover:translate-x-0.5" />
                  </span>
                </Link>
              </motion.li>
            ))
          )}
        </ol>

        <aside className="flex min-w-0 flex-col gap-4">
          <section className="rounded-3 border border-line-1 bg-bg-1 p-5">
            <h2 className="flex items-center gap-2 text-base font-medium text-fg-1">
              <LifeBuoy size={18} className="text-brand" />
              {t("pcContent.help.title")}
            </h2>
            <p className="mt-1 text-sm text-fg-3">{t("pcContent.help.subtitle")}</p>
            <ul className="mt-4 flex flex-col gap-1">
              {help.isPending
                ? [0, 1, 2, 3].map((i) => (
                    <li key={i} className="py-1.5">
                      <Skeleton className="h-4 w-40" />
                    </li>
                  ))
                : (help.data ?? []).slice(0, 6).map((h) => (
                    <li key={h.slug}>
                      <Link
                        to={routes.helpArticle(h.slug)}
                        className="-mx-2 flex items-center gap-2 rounded-2 px-2 py-1.5 text-sm text-fg-2 transition-colors hover:bg-bg-2 hover:text-fg-1"
                      >
                        <BookOpen size={14} className="shrink-0 text-fg-3" />
                        <span className="truncate">{h.title}</span>
                      </Link>
                    </li>
                  ))}
            </ul>
            <Link to={routes.help} className="mt-3 flex items-center gap-1 text-sm text-brand hover:underline">
              {t("pcContent.help.all")}
              <ArrowRight size={14} />
            </Link>
          </section>
        </aside>
      </div>
    </div>
  );
}
