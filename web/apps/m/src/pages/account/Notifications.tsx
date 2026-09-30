import { errorText, routes } from "@exchange/core";
import {
  filterNotices, markRead, noticeCategory, noticeKeys, noticeLink, useNotifications, type Notice, type NoticeCategory,
} from "@exchange/core/user/notifications";
import { Badge, Button, EmptyState, ErrorState, Skeleton, SkeletonLines, Spinner, TimeText, cn, listItem, toast, type BadgeTone } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { Check, CheckCheck, ChevronRight, Megaphone, ShieldAlert, Wallet } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { PillBar } from "../../components/PillBar";
import { PullToRefresh } from "../../components/PullToRefresh";
import { usePageHeader } from "../../layout/header";
import { TextButton } from "../auth/parts/TextButton";
import { LoadMore } from "./parts/LoadMore";
import { countBadge, entrance } from "./parts/logic";

type Filter = NoticeCategory | "all";

const categoryIcon: Record<NoticeCategory, ReactNode> = {
  security: <ShieldAlert size={18} />,
  assets: <Wallet size={18} />,
  system: <Megaphone size={18} />,
};

const categoryTone: Record<NoticeCategory, { icon: string; badge: BadgeTone }> = {
  security: { icon: "bg-warn/10 text-warn", badge: "warn" },
  assets: { icon: "bg-info/10 text-info", badge: "info" },
  system: { icon: "bg-brand-soft text-brand", badge: "brand" },
};

/**
 * Notifications (design §7.2 我的 → 通知): the inbox in categories (pill
 * tabs), unread ones marked, one or all marked read, a tap opens the page
 * a notice is about. Pull down to refresh; more load as the list scrolls.
 * New notices arrive by the private push, which refreshes the list and
 * the bell.
 */
export default function Notifications() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const q = useNotifications();
  const [filter, setFilter] = useState<Filter>("all");
  const [marking, setMarking] = useState(false);
  const all = useMemo(() => q.data?.pages.flatMap((p) => p.items) ?? [], [q.data]);
  const items = useMemo(() => filterNotices(all, filter), [all, filter]);
  const unread = q.data?.pages[0]?.unread_count ?? 0;

  const read = async (which: string[] | "all") => {
    try {
      await markRead(qc, which);
      if (which === "all") toast.success(t("mAccount.notices.markedAll"));
    } catch (e) {
      toast.error(errorText(e));
    }
  };

  const markAll = async () => {
    setMarking(true);
    await read("all");
    setMarking(false);
  };

  usePageHeader(
    {
      title: t("mAccount.notices.title"),
      back: routes.me,
      right: (
        <button
          type="button"
          aria-label={t("mAccount.notices.markAll")}
          title={t("mAccount.notices.markAll")}
          disabled={unread === 0 || marking}
          onClick={() => void markAll()}
          className="grid size-11 place-items-center text-fg-2 transition-opacity active:opacity-60 disabled:text-fg-3 disabled:opacity-50"
        >
          {marking ? <Spinner size={18} /> : <CheckCheck size={20} />}
        </button>
      ),
    },
    [t, unread, marking],
  );

  const open = (n: Notice) => {
    if (!n.read) void read([n.id]);
    const to = noticeLink(n);
    if (to) navigate(to);
  };

  const refresh = async () => {
    void qc.invalidateQueries({ queryKey: noticeKeys.unread });
    const r = await q.refetch();
    if (r.isError) toast.error(errorText(r.error));
  };

  return (
    <PullToRefresh onRefresh={refresh}>
      <div className="flex flex-col gap-3 px-4 py-3">
        <PillBar
          aria-label={t("mAccount.notices.title")}
          value={filter}
          onValueChange={(v) => setFilter(v as Filter)}
          items={[
            {
              value: "all",
              label: (
                <>
                  {t("mAccount.notices.tabs.all")}
                  {unread > 0 && <span className="text-xs tabular-nums opacity-70">{countBadge(unread)}</span>}
                </>
              ),
            },
            { value: "security", label: t("mAccount.notices.tabs.security") },
            { value: "assets", label: t("mAccount.notices.tabs.assets") },
            { value: "system", label: t("mAccount.notices.tabs.system") },
          ]}
        />
        {q.isPending ? (
          <ul aria-busy className="flex flex-col gap-2">
            {[0, 1, 2, 3].map((i) => (
              <li key={i} className="flex gap-3 rounded-3 bg-bg-1 p-4">
                <Skeleton round className="size-10" />
                <SkeletonLines lines={3} className="flex-1" />
              </li>
            ))}
          </ul>
        ) : q.isError ? (
          <div className="rounded-3 bg-bg-1">
            <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
          </div>
        ) : (
          <>
            {items.length > 0 ? (
              <ul className="flex flex-col gap-2">
                {items.map((n, i) => (
                  <NoticeCard key={n.id} notice={n} index={i} onOpen={() => open(n)} onRead={() => void read([n.id])} />
                ))}
              </ul>
            ) : (
              // A category may be empty in the pages so far: LoadMore keeps looking.
              !q.hasNextPage && (
                <div className="rounded-3 bg-bg-1">
                  <EmptyState
                    title={t("mAccount.notices.empty")}
                    description={t("mAccount.notices.emptyHint")}
                    action={
                      <Button asChild size="lg" variant="secondary">
                        <Link to={routes.security}>{t("mAccount.notices.toSecurity")}</Link>
                      </Button>
                    }
                  />
                </div>
              )
            )}
            {(items.length > 0 || q.hasNextPage) && (
              <LoadMore
                hasMore={Boolean(q.hasNextPage)}
                loading={q.isFetchingNextPage}
                failed={q.isFetchNextPageError}
                onMore={() => void q.fetchNextPage()}
                count={items.length}
              />
            )}
          </>
        )}
      </div>
    </PullToRefresh>
  );
}

/**
 * NoticeCard: the notice as one large button (it opens what the notice is
 * about and marks it read), and under an unread one its own "mark read".
 */
function NoticeCard({ notice: n, index, onOpen, onRead }: { notice: Notice; index: number; onOpen: () => void; onRead: () => void }) {
  const { t } = useTranslation();
  const category = noticeCategory(n.type);
  const tone = categoryTone[category];
  const link = noticeLink(n);
  return (
    <motion.li
      variants={listItem}
      initial={entrance(index)}
      animate="animate"
      custom={index}
      className={cn("relative overflow-hidden rounded-3", n.read ? "bg-bg-1" : "bg-brand-soft")}
    >
      {!n.read && <span aria-hidden className="absolute inset-y-4 left-0 w-0.5 rounded-full bg-brand" />}
      <button type="button" onClick={onOpen} className="flex w-full gap-3 p-4 text-left transition-colors active:bg-bg-2">
        <span aria-hidden className={cn("grid size-10 shrink-0 place-items-center rounded-full", tone.icon)}>
          {categoryIcon[category]}
        </span>
        <span className="block min-w-0 flex-1">
          <span className="flex items-start gap-2">
            <span className={cn("min-w-0 flex-1 break-words text-sm", n.read ? "text-fg-2" : "font-semibold text-fg-1")}>{n.title}</span>
            {!n.read && <span className="sr-only">{t("mAccount.notices.unreadMark")}</span>}
            <Badge tone={tone.badge}>{t(`mAccount.notices.tabs.${category}`)}</Badge>
          </span>
          {n.body && <span className="mt-1 block break-words text-sm leading-relaxed text-fg-3">{n.body}</span>}
          <span className="mt-2 flex items-center gap-3 text-xs text-fg-3">
            <TimeText value={n.created_at} relative />
            {link && (
              <span className="inline-flex items-center gap-0.5 text-brand">
                {t("mAccount.notices.view")}
                <ChevronRight size={12} aria-hidden />
              </span>
            )}
          </span>
        </span>
      </button>
      {!n.read && (
        <div className="flex justify-end border-t border-line-1 px-2">
          <TextButton tone="muted" onClick={onRead} className="px-2">
            <Check size={14} aria-hidden /> {t("mAccount.notices.markRead")}
          </TextButton>
        </div>
      )}
    </motion.li>
  );
}
