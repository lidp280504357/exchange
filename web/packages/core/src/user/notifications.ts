import { useInfiniteQuery, useQuery, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { notificationApi, unwrap } from "../api/client";
import type { components } from "../api/gen/notification";
import { qk } from "../query/keys";
import { routes } from "../routes";
import { selectSignedIn, useSession } from "../session/store";

// The in-app inbox (api/openapi/notification.yaml). Every key lives under
// the "notifications" root: the private push of a new notice invalidates
// that root (query/private.ts), so the list and the bell's unread count
// refresh by themselves, and sign-out clears them.

/**
 * A notice. The server sends more types than the contract's enum lists
 * (TOTP_CHANGED, deposits, withdrawals), so the type is any string here.
 */
export type Notice = Omit<components["schemas"]["Notification"], "type"> & { type: string };

export type NoticePage = { items: Notice[]; next_cursor: string | null; unread_count: number };

export const noticeKeys = {
  all: qk.notifications,
  list: [...qk.notifications, "list"] as const,
  unread: [...qk.notifications, "unread"] as const,
};

/** Page size of the inbox list. */
export const NOTICE_PAGE = 20;

async function fetchNotices(cursor: string | undefined, limit: number): Promise<NoticePage> {
  return (await unwrap(notificationApi.GET("/v1/notifications", { params: { query: { cursor, limit } } }))) as NoticePage;
}

/** useNotifications pages through the inbox, newest first; each page carries the unread count. */
export function useNotifications() {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: noticeKeys.list,
    queryFn: ({ pageParam }) => fetchNotices(pageParam || undefined, NOTICE_PAGE),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn,
    staleTime: 30_000,
  });
}

/**
 * useUnreadNotifications is the number of unread notices, for the bell in
 * the top bar (0 while signed out or loading).
 */
export function useUnreadNotifications(): number {
  const signedIn = useSession(selectSignedIn);
  const q = useQuery({
    queryKey: noticeKeys.unread,
    queryFn: () => fetchNotices(undefined, 1),
    enabled: signedIn,
    staleTime: 60_000,
    select: (d) => d.unread_count,
  });
  return signedIn ? (q.data ?? 0) : 0;
}

/** markNoticesRead marks some notices, or all of them, read; returns how many changed. */
export async function markNoticesRead(which: string[] | "all"): Promise<number> {
  const body = which === "all" ? { all: true } : { ids: which.slice(0, 100) };
  const res = await unwrap(notificationApi.POST("/v1/notifications/read", { body }));
  return res.updated;
}

/**
 * readInPages marks notices read in the cached pages (all of them for
 * "all") and lowers the unread count by the ones that were unread.
 */
export function readInPages(data: InfiniteData<NoticePage> | undefined, which: string[] | "all"): InfiniteData<NoticePage> | undefined {
  if (!data) return data;
  const ids = which === "all" ? null : new Set(which);
  let changed = 0;
  const pages = data.pages.map((p) => ({
    ...p,
    items: p.items.map((n) => {
      if (n.read || (ids && !ids.has(n.id))) return n;
      changed++;
      return { ...n, read: true };
    }),
  }));
  const unread = (count: number) => (which === "all" ? 0 : Math.max(0, count - changed));
  return { ...data, pages: pages.map((p) => ({ ...p, unread_count: unread(p.unread_count) })) };
}

/**
 * markRead marks notices read at once in the cache (list and count),
 * then on the server; the root is refetched either way.
 */
export async function markRead(qc: QueryClient, which: string[] | "all"): Promise<void> {
  const list = qc.getQueryData<InfiniteData<NoticePage>>(noticeKeys.list);
  const before = unreadOf(list, which);
  qc.setQueryData<InfiniteData<NoticePage>>(noticeKeys.list, (d) => readInPages(d, which));
  qc.setQueryData<NoticePage>(noticeKeys.unread, (d) =>
    d ? { ...d, unread_count: which === "all" ? 0 : Math.max(0, d.unread_count - before) } : d,
  );
  try {
    await markNoticesRead(which);
  } finally {
    void qc.invalidateQueries({ queryKey: noticeKeys.all });
  }
}

function unreadOf(data: InfiniteData<NoticePage> | undefined, which: string[] | "all"): number {
  if (!data || which === "all") return 0;
  const ids = new Set(which);
  return data.pages.reduce((n, p) => n + p.items.filter((i) => !i.read && ids.has(i.id)).length, 0);
}

/** The inbox's categories (the API has types; the page groups them). */
export type NoticeCategory = "security" | "assets" | "system";

export const NOTICE_CATEGORIES: readonly NoticeCategory[] = ["security", "assets", "system"];

const SECURITY = new Set(["NEW_DEVICE_LOGIN", "IDENTITY_CHANGED", "PASSWORD_CHANGED", "ACCOUNT_LOCKED", "TOTP_CHANGED"]);

/** noticeCategory groups a notice type: sign-ins and security settings, deposits and withdrawals, the rest. */
export function noticeCategory(type: string): NoticeCategory {
  if (SECURITY.has(type)) return "security";
  if (type.startsWith("DEPOSIT_") || type.startsWith("WITHDRAWAL_")) return "assets";
  return "system";
}

/** noticeLink is the page a notice leads to, if any: the devices, the security centre, the deposit or withdrawal page. */
export function noticeLink(n: Pick<Notice, "type">): string | null {
  switch (n.type) {
    case "NEW_DEVICE_LOGIN":
      return routes.sessions;
    case "IDENTITY_CHANGED":
    case "PASSWORD_CHANGED":
    case "ACCOUNT_LOCKED":
    case "TOTP_CHANGED":
      return routes.security;
    case "WELCOME":
      return routes.assets;
    default:
      if (n.type.startsWith("DEPOSIT_")) return routes.deposit;
      if (n.type.startsWith("WITHDRAWAL_")) return routes.withdraw;
      return null;
  }
}

/** filterNotices keeps a category's notices ("all" keeps every one), optionally only the unread. */
export function filterNotices(items: readonly Notice[], category: NoticeCategory | "all", unreadOnly = false): Notice[] {
  return items.filter((n) => (category === "all" || noticeCategory(n.type) === category) && (!unreadOnly || !n.read));
}
