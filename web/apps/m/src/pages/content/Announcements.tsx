import { errorText, formatTime, routes, useSettings } from "@exchange/core";
import { useArticles, type ArticleMeta } from "@exchange/core/content/index";
import { Badge, Button, EmptyState, ErrorState, Skeleton, SkeletonLines, Tag, cn, listItem } from "@exchange/ui";
import { Pin } from "lucide-react";
import { motion } from "motion/react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { usePageHeader } from "../../layout/header";
import { SimNotice } from "./parts";

// /announcements on the phone (design §7.2): pinned notices first, then
// the newest, as cards; a tap opens the article. The Markdown files ship
// with the site (packages/core/content), so there is nothing to refresh.

/** Cards of the first screen that fade in one after another. */
const INTRO_ROWS = 12;

export default function Announcements() {
  const { t } = useTranslation();
  usePageHeader({ title: t("mContent.announcements.title"), back: routes.home }, [t]);
  const q = useArticles("announcements");
  const list = q.data ?? [];

  let body;
  if (q.isPending)
    body = (
      <ul className="flex flex-col gap-3">
        {[0, 1, 2].map((i) => (
          <li key={i} className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Skeleton className="h-3 w-32" />
            <Skeleton className="mt-3 h-5 w-3/4" />
            <SkeletonLines lines={2} className="mt-2" />
          </li>
        ))}
      </ul>
    );
  else if (q.isError)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
      </div>
    );
  else if (list.length === 0)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <EmptyState
          title={t("mContent.announcements.empty")}
          action={
            <Button asChild size="lg" variant="secondary">
              <Link to={routes.help}>{t("nav.help")}</Link>
            </Button>
          }
        />
      </div>
    );
  else
    body = (
      <ol className="flex flex-col gap-3">
        {list.map((a, i) => (
          <motion.li key={a.slug} variants={listItem} initial={i < INTRO_ROWS ? "initial" : false} animate="animate" custom={i}>
            <AnnouncementCard article={a} />
          </motion.li>
        ))}
      </ol>
    );

  return (
    <div className="flex flex-col gap-4 px-4 pb-8 pt-3">
      <h1 className="sr-only">{t("mContent.announcements.title")}</h1>
      <p className="text-sm text-fg-3">{t("mContent.announcements.subtitle")}</p>
      {body}
      <SimNotice />
    </div>
  );
}

function AnnouncementCard({ article: a }: { article: ArticleMeta }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  return (
    <Link
      to={routes.announcement(a.slug)}
      className={cn(
        "relative block overflow-hidden rounded-3 border bg-bg-1 p-4 transition-[transform,background-color] duration-[var(--t-fast)] active:scale-[0.99] active:bg-bg-2",
        a.pinned ? "border-brand/40" : "border-line-1",
      )}
    >
      {a.pinned && <span aria-hidden className="absolute inset-y-0 left-0 w-1 bg-brand" />}
      <div className="flex min-w-0 items-center gap-2 text-xs text-fg-3">
        {a.pinned && (
          <Badge tone="brand" icon={<Pin size={12} />}>
            {t("mContent.announcements.pinned")}
          </Badge>
        )}
        <Tag>{t(`mContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>
        <time dateTime={a.date} className="ml-auto shrink-0 tabular-nums">
          {formatTime(a.date, "date", locale, "UTC")}
        </time>
      </div>
      <h2 className="mt-2.5 text-md font-semibold leading-snug text-fg-1">{a.title}</h2>
      <p className="mt-1.5 line-clamp-2 text-sm leading-relaxed text-fg-3">{a.summary}</p>
    </Link>
  );
}
