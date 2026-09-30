import { errorText, routes } from "@exchange/core";
import {
  filterNotices, markRead, noticeCategory, noticeLink, useNotifications, type Notice, type NoticeCategory,
} from "@exchange/core/user/notifications";
import {
  Badge, Button, EmptyState, ErrorState, Skeleton, SkeletonLines, Switch, Tabs, TimeText, cn, listItem, toast, type BadgeTone,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, CheckCheck, Megaphone, ShieldAlert, Wallet } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { AccountLayout } from "./parts/AccountLayout";

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
 * Notifications (design §6.2 账户): the inbox in categories (security,
 * assets, system), unread ones marked, one or all marked read, and a link
 * to the page a notice is about. New notices arrive by the private push,
 * which refreshes the list and the bell.
 */
export default function Notifications() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const q = useNotifications();
  const [filter, setFilter] = useState<Filter>("all");
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [marking, setMarking] = useState(false);
  const all = useMemo(() => q.data?.pages.flatMap((p) => p.items) ?? [], [q.data]);
  const items = useMemo(() => filterNotices(all, filter, unreadOnly), [all, filter, unreadOnly]);
  const unread = q.data?.pages[0]?.unread_count ?? 0;

  const read = async (which: string[] | "all") => {
    try {
      await markRead(qc, which);
      if (which === "all") toast.success(t("pcAccount.notices.markedAll"));
    } catch (e) {
      toast.error(errorText(e));
    }
  };

  const open = (n: Notice) => {
    if (!n.read) void read([n.id]);
    const to = noticeLink(n);
    if (to) navigate(to);
  };

  return (
    <AccountLayout
      title={t("pcAccount.notices.title")}
      subtitle={t("pcAccount.notices.subtitle")}
      actions={
        <Button
          variant="secondary"
          icon={<CheckCheck size={16} />}
          disabled={unread === 0}
          loading={marking}
          onClick={async () => {
            setMarking(true);
            await read("all");
            setMarking(false);
          }}
        >
          {t("pcAccount.notices.markAll")}
        </Button>
      }
    >
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <Tabs
          variant="pill"
          value={filter}
          onValueChange={(v) => setFilter(v as Filter)}
          listClassName="border-b border-line-1 px-4 py-3"
          aria-label={t("pcAccount.notices.title")}
          items={[
            { value: "all", label: t("pcAccount.notices.tabs.all"), count: unread > 0 ? unread : undefined },
            { value: "security", label: t("pcAccount.notices.tabs.security") },
            { value: "assets", label: t("pcAccount.notices.tabs.assets") },
            { value: "system", label: t("pcAccount.notices.tabs.system") },
          ]}
          extra={
            <Switch size="sm" checked={unreadOnly} onCheckedChange={setUnreadOnly} label={t("pcAccount.notices.unreadOnly")} />
          }
        />
        {q.isPending ? (
          <ul aria-busy className="flex flex-col">
            {[0, 1, 2, 3].map((i) => (
              <li key={i} className="flex gap-4 border-b border-line-1 px-5 py-4 last:border-0">
                <Skeleton round className="size-10" />
                <SkeletonLines lines={2} className="flex-1" />
              </li>
            ))}
          </ul>
        ) : q.isError ? (
          <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState
            title={unreadOnly ? t("pcAccount.notices.emptyUnread") : t("pcAccount.notices.empty")}
            description={t("pcAccount.notices.emptyHint")}
            action={
              <Button asChild size="sm" variant="secondary">
                <Link to={routes.security}>{t("pcAccount.notices.toSecurity")}</Link>
              </Button>
            }
          />
        ) : (
          <ul className="flex flex-col">
            {items.map((n, i) => (
              <NoticeRow key={n.id} notice={n} index={i} onOpen={() => open(n)} onRead={() => void read([n.id])} />
            ))}
          </ul>
        )}
        {q.data && (q.hasNextPage || all.length > 0) && (
          <div className="flex justify-center border-t border-line-1 p-3">
            {q.hasNextPage ? (
              <Button variant="ghost" size="sm" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
                {t("pcAccount.notices.loadMore")}
              </Button>
            ) : (
              <span className="text-xs text-fg-3">{t("pcAccount.notices.noMore")}</span>
            )}
          </div>
        )}
      </div>
    </AccountLayout>
  );
}

function NoticeRow({ notice: n, index, onOpen, onRead }: { notice: Notice; index: number; onOpen: () => void; onRead: () => void }) {
  const { t } = useTranslation();
  const category = noticeCategory(n.type);
  const tone = categoryTone[category];
  const link = noticeLink(n);
  return (
    <motion.li
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      className={cn(
        "group relative flex gap-4 border-b border-line-1 px-5 py-4 transition-colors last:border-0 hover:bg-bg-2",
        !n.read && "bg-brand-soft/40",
      )}
    >
      {!n.read && <span aria-hidden className="absolute inset-y-4 left-0 w-0.5 rounded-full bg-brand" />}
      <span className={cn("grid size-10 shrink-0 place-items-center rounded-full", tone.icon)}>{categoryIcon[category]}</span>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={onOpen}
            className={cn("text-left text-sm hover:underline", n.read ? "text-fg-2" : "font-semibold text-fg-1")}
          >
            {n.title}
          </button>
          {!n.read && <span className="sr-only">{t("pcAccount.notices.unreadMark")}</span>}
          <Badge tone={tone.badge}>{t(`pcAccount.notices.tabs.${category}`)}</Badge>
        </div>
        {n.body && <p className="mt-1 text-sm leading-relaxed text-fg-3">{n.body}</p>}
        <div className="mt-2 flex items-center gap-4 text-xs text-fg-3">
          <TimeText value={n.created_at} relative />
          {link && (
            <button type="button" onClick={onOpen} className="inline-flex items-center gap-1 text-brand hover:underline">
              {t("pcAccount.notices.view")} <ArrowRight size={12} />
            </button>
          )}
        </div>
      </div>
      {!n.read && (
        <Button
          size="sm"
          variant="ghost"
          onClick={onRead}
          className="shrink-0 self-start opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 focus-visible:opacity-100"
        >
          {t("pcAccount.notices.markRead")}
        </Button>
      )}
    </motion.li>
  );
}
