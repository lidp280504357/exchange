import { dec } from "@exchange/core";
import { useQuery } from "@tanstack/react-query";
import type { Page } from "../../kit/lists";
import * as mock from "./mock";
import type { MarginAccount, MarginInterestBucket, MarginLiquidation } from "./mock";

// The margin pages' data (design 2026-10-06 §8, A55). The API is a draft
// (api/admin/admin.yaml, tag margin) that admin-service does not serve
// yet: until E5 these answer with mock.ts's sample, filtered as the
// server is to filter it, and the pages' changes go nowhere (common.tsx).

export const marginKey = ["admin", "margin"];

export function useMarginAssets() {
  return useQuery({ queryKey: [...marginKey, "assets"], queryFn: async () => mock.assets });
}

export function useMarginPairs() {
  return useQuery({ queryKey: [...marginKey, "pairs"], queryFn: async () => mock.pairs });
}

export function useMarginSettings() {
  return useQuery({ queryKey: [...marginKey, "settings"], queryFn: async () => mock.settings });
}

export type AccountQuery = { status?: string; account?: string; symbol?: string; user_id?: string };

/** riskOrder sorts accounts as the server does: the lowest margin level first, those without liabilities last, by net assets. */
function riskOrder(a: MarginAccount, b: MarginAccount): number {
  if (a.margin_level === null || b.margin_level === null) {
    if (a.margin_level !== b.margin_level) return a.margin_level === null ? 1 : -1;
    return dec.cmp(b.net_asset, a.net_asset);
  }
  return dec.cmp(a.margin_level, b.margin_level);
}

export function useMarginAccounts(q: AccountQuery) {
  return useQuery({
    queryKey: [...marginKey, "accounts", q],
    queryFn: async () => {
      const items = mock.accounts
        .filter(
          (a) =>
            (!q.status || a.status === q.status) && (!q.account || a.account === q.account) && (!q.symbol || a.symbol === q.symbol) &&
            (!q.user_id || a.user_id === q.user_id),
        )
        .sort(riskOrder);
      return { items, truncated: false };
    },
  });
}

/** useMarginAccount reads an account by its holder and key (MARGIN_CROSS, or MARGIN_ISOLATED:<symbol>). */
export function useMarginAccount(userId: string, key: string) {
  return useQuery({
    queryKey: [...marginKey, "account", userId, key],
    queryFn: async () => {
      const a = mock.accounts.find((x) => x.user_id === userId && mock.keyOf(x) === key);
      if (!a) throw new Error(`no margin account ${key} of ${userId}`);
      return mock.accountDetail(a);
    },
  });
}

export type LiquidationQuery = { days: number; account?: string; symbol?: string; trigger?: string; user_id?: string };

/** liquidationsPage is a page of liquidations, newest first (one page: the sample is short). */
export async function liquidationsPage(q: LiquidationQuery): Promise<Page<MarginLiquidation>> {
  const since = Date.parse("2026-10-06T23:59:59Z") - q.days * 86_400_000;
  const items = mock.liquidations.filter(
    (l) =>
      Date.parse(l.started_at) >= since && (!q.account || l.account === q.account) && (!q.symbol || l.symbol === q.symbol) &&
      (!q.trigger || l.trigger === q.trigger) && (!q.user_id || l.user_id === q.user_id),
  );
  return { items, next_cursor: null };
}

export type InterestQuery = { days: number; bucket: "day" | "week" | "month"; asset?: string };

/** bucketOf is the first day of a day's bucket: the day, its week's Monday or its month's first day (UTC). */
function bucketOf(day: string, bucket: InterestQuery["bucket"]): string {
  if (bucket === "month") return `${day.slice(0, 7)}-01`;
  if (bucket === "day") return day;
  const t = new Date(`${day}T00:00:00Z`);
  t.setUTCDate(t.getUTCDate() - ((t.getUTCDay() + 6) % 7));
  return t.toISOString().slice(0, 10);
}

/** sumUp folds the sample's days into buckets as the report is to: sums, the last day's owed, the mean principal. */
function sumUp(days: MarginInterestBucket[], bucket: InterestQuery["bucket"]): MarginInterestBucket[] {
  const out = new Map<string, MarginInterestBucket & { n: number }>();
  const usdt = (x: string | null, y: string | null) => (x !== null && y !== null ? dec.add(x, y) : null);
  for (const d of days) {
    const day = bucketOf(d.day, bucket);
    const b = out.get(`${day} ${d.asset}`);
    if (!b) {
      out.set(`${day} ${d.asset}`, { ...d, day, n: 1 });
      continue;
    }
    b.charged = dec.add(b.charged, d.charged);
    b.repaid = dec.add(b.repaid, d.repaid);
    b.charged_usdt = usdt(b.charged_usdt, d.charged_usdt);
    b.repaid_usdt = usdt(b.repaid_usdt, d.repaid_usdt);
    b.owed = d.owed;
    b.principal_avg = dec.div(dec.add(dec.mul(b.principal_avg, String(b.n)), d.principal_avg), String(b.n + 1), 8);
    b.accounts = Math.max(b.accounts, d.accounts);
    b.n++;
  }
  return [...out.values()].map(({ n: _, ...b }) => b);
}

export function useMarginInterest(q: InterestQuery) {
  return useQuery({
    queryKey: [...marginKey, "interest", q],
    queryFn: async () => ({ items: sumUp(mock.interest.filter((b) => !q.asset || b.asset === q.asset), q.bucket) }),
  });
}
