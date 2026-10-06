import { QueryClient, type QueryKey } from "@tanstack/react-query";
import { useEffect } from "react";
import { add } from "../format/decimal";
import type { MarginAccount, MarginPush } from "../margin/math";
import { useWs } from "../market/hooks";
import { selectSignedIn, useSession } from "../session/store";
import type { WsClient } from "../ws/client";
import type { BalanceData, FillData, OrderData, PrivatePush } from "../ws/types";
import { privateRoots, qk } from "./keys";

// Private pushes go straight into the query cache (design §4.3): a balance
// push updates that balance, an order push that order, a fill is prepended.
// Only a resync (the server lost events) or data a push cannot express
// reloads over REST, debounced by 500 ms, instead of a string of refetches
// after every trade.

/** createQueryClient returns the app's client with the shared defaults. */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: 1, staleTime: 10_000, refetchOnWindowFocus: false },
    },
  });
}

type Balance = { account_type: string; asset: string; available: string; frozen: string; total: string };
type BalancesPage = { balances: Balance[] };

/** applyBalance puts a balance push into the cached balances. */
export function applyBalance(page: BalancesPage | undefined, p: BalanceData): BalancesPage | undefined {
  if (!page) return page;
  const next: Balance = {
    account_type: p.account_type, asset: p.asset, available: p.available, frozen: p.frozen, total: add(p.available, p.frozen),
  };
  const i = page.balances.findIndex((b) => b.account_type === p.account_type && b.asset === p.asset);
  const balances = [...page.balances];
  if (i >= 0) balances[i] = next;
  else balances.push(next);
  return { ...page, balances };
}

type Order = Record<string, unknown> & { order_id: string; status: string };
type OrdersPage = { items: Order[]; next_cursor: string | null };

const ACTIVE = new Set(["NEW", "OPEN", "PARTIALLY_FILLED"]);

/**
 * applyOrder merges an order push into a cached page of orders for one
 * status filter: fields the push carries replace the cached ones; an order
 * that leaves the filter (filled, canceled) leaves the page. A new order
 * joins the ACTIVE pages of its symbol (and of all symbols).
 */
export function applyOrder(page: OrdersPage | undefined, p: OrderData, filter: string, symbol: string): OrdersPage | undefined {
  if (!page) return page;
  const fits = filter === "ACTIVE" ? ACTIVE.has(p.status) : filter === "" || filter === p.status;
  const i = page.items.findIndex((o) => o.order_id === p.order_id);
  const defined = Object.fromEntries(Object.entries(p).filter(([, v]) => v !== undefined && v !== ""));
  if (i >= 0) {
    if (!fits) return { ...page, items: page.items.filter((_, j) => j !== i) };
    const items = [...page.items];
    items[i] = { ...items[i]!, ...defined };
    return { ...page, items };
  }
  if (!fits || (symbol !== "" && symbol !== p.symbol) || !p.side) return page; // not enough to show
  const created = new Date().toISOString();
  const order: Order = { filled_quantity: "0", filled_quote: "0", created_at: created, updated_at: created, ...defined, order_id: p.order_id, status: p.status };
  return { ...page, items: [order, ...page.items] };
}

type FillsPage = { items: (Record<string, unknown> & { trade_id: string; order_id: string })[]; next_cursor: string | null };

/** applyFill prepends a fill push to a cached page of fills. */
export function applyFill(page: FillsPage | undefined, p: FillData, symbol: string): FillsPage | undefined {
  if (!page || (symbol !== "" && symbol !== p.symbol)) return page;
  if (page.items.some((f) => f.trade_id === p.trade_id && f.order_id === p.order_id)) return page;
  return { ...page, items: [p as unknown as FillsPage["items"][number], ...page.items] };
}

type MarginAccounts = { cross: MarginAccount; isolated: MarginAccount[] };

/** holdingsOf is what an account holds and owes, without its valuation: the same for two pushes that only revalued it. */
export const holdingsOf = (a: MarginAccount) => a.balances.map((b) => [b.asset, b.free, b.locked, b.borrowed, b.interest].join(" ")).join("|");

/** marginKey names a margin account: the cross account, or an isolated account by its pair. */
export const marginKey = (a: Pick<MarginAccount, "account" | "symbol">) => `${a.account}:${a.symbol ?? ""}`;

/**
 * applyMarginAccount puts an account of an ACCOUNT push into the cached
 * accounts (GET /v1/margin/accounts): the cross account, or its pair's
 * isolated account (a new one joins the list). One older than the cached
 * account (a poll answered after the push was made), or an isolated one
 * without its pair, is left out. held tells whether what the account holds
 * or owes changed, not only its valuation: against last, the holdings of
 * its previous push (holdingsOf), else against the cached account; with
 * neither it cannot tell and says so.
 */
export function applyMarginAccount(
  page: MarginAccounts | undefined,
  a: MarginAccount,
  last?: string,
): { page: MarginAccounts | undefined; held: boolean } {
  const cross = a.account === "MARGIN_CROSS";
  if (!cross && !a.symbol) return { page, held: false };
  const i = cross || !page ? -1 : page.isolated.findIndex((x) => x.symbol === a.symbol);
  const cur = page ? (cross ? page.cross : page.isolated[i]) : undefined;
  if (cur && Date.parse(cur.updated_at) > Date.parse(a.updated_at)) return { page, held: false };
  const before = last ?? (cur ? holdingsOf(cur) : undefined);
  const held = before !== holdingsOf(a);
  if (!page) return { page, held };
  if (cross) return { page: { ...page, cross: a }, held };
  const isolated = [...page.isolated];
  if (i >= 0) isolated[i] = a;
  else isolated.push(a);
  return { page: { ...page, isolated }, held };
}

/** isContract tells a perpetual contract (BTC-USDT-PERP) from a spot pair. */
export function isContract(symbol: string | undefined): boolean {
  return Boolean(symbol?.endsWith("-PERP"));
}

type Infinite<T> = { pages: T[]; pageParams: unknown[] };

function isInfinite<T>(d: unknown): d is Infinite<T> {
  return typeof d === "object" && d !== null && Array.isArray((d as Infinite<T>).pages);
}

/**
 * applyOrderData applies an order push to a cached query of either shape:
 * one page, or the pages of an infinite list (updated where the order is,
 * a new order joins the first page).
 */
export function applyOrderData(data: unknown, p: OrderData, filter: string, symbol: string): unknown {
  if (!isInfinite<OrdersPage>(data)) return applyOrder(data as OrdersPage | undefined, p, filter, symbol);
  if (data.pages.length === 0) return data;
  const at = Math.max(0, data.pages.findIndex((pg) => pg.items.some((o) => o.order_id === p.order_id)));
  const pages = [...data.pages];
  pages[at] = applyOrder(pages[at], p, filter, symbol)!;
  return { ...data, pages };
}

/** applyFillData prepends a fill push to a cached page or to the first page of an infinite list. */
export function applyFillData(data: unknown, p: FillData, symbol: string): unknown {
  if (!isInfinite<FillsPage>(data)) return applyFill(data as FillsPage | undefined, p, symbol);
  if (data.pages.length === 0 || data.pages.some((pg) => pg.items.some((f) => f.trade_id === p.trade_id && f.order_id === p.order_id))) return data;
  const pages = [...data.pages];
  pages[0] = applyFill(pages[0], p, symbol)!;
  return { ...data, pages };
}

/**
 * applyOrderToCaches puts an order the caller just placed or cancelled
 * (the REST answer) into every cached list of orders, as its push would:
 * the push may arrive before the page's private subscription is up (a
 * fresh page load) and be missed.
 */
export function applyOrderToCaches(qc: QueryClient, order: OrderData): void {
  for (const [key, data] of qc.getQueriesData({ queryKey: qk.allOrders })) {
    const [, symbol = "", filter = ""] = key as [string, string, string];
    qc.setQueryData(key, applyOrderData(data, order, filter, symbol));
  }
}

/** Debounced invalidation: many pushes, one refetch per key. */
class Invalidator {
  private pending = new Map<string, QueryKey>();
  private timer?: ReturnType<typeof setTimeout>;
  constructor(
    private qc: QueryClient,
    private delay = 500,
  ) {}
  add(key: QueryKey) {
    this.pending.set(JSON.stringify(key), key);
    clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      const keys = [...this.pending.values()];
      this.pending.clear();
      for (const k of keys) void this.qc.invalidateQueries({ queryKey: k });
    }, this.delay);
  }
}

/**
 * bindPrivate routes the private channels into the query cache; returns
 * the unsubscribe. Call while signed in (usePrivateSync does).
 */
export function bindPrivate(ws: WsClient, qc: QueryClient): () => void {
  const later = new Invalidator(qc);
  // Each margin account's holdings at its last push (holdingsOf), to tell a
  // push that changed them from one that revalued them while no page holds
  // the accounts (the ledger page reloads only on the first kind).
  const pushed = new Map<string, string>();
  const offs = [
    ws.subscribe("balances", (m) => {
      const p = (m as PrivatePush<BalanceData>).data;
      const cur = qc.getQueryData<BalancesPage>(qk.balances);
      if (cur) qc.setQueryData(qk.balances, applyBalance(cur, p));
      else later.add(qk.balances);
      later.add(qk.ledger);
      if (p.account_type === "FUTURES") later.add(qk.derivatives);
    }),
    ws.subscribe("orders", (m) => {
      const p = (m as PrivatePush<OrderData>).data;
      // Contract orders live under the derivatives queries, which reload.
      if (isContract(p.symbol)) return later.add(qk.derivatives);
      for (const [key, data] of qc.getQueriesData({ queryKey: qk.allOrders })) {
        const [, symbol = "", filter = ""] = key as [string, string, string];
        qc.setQueryData(key, applyOrderData(data, p, filter, symbol));
      }
    }),
    ws.subscribe("fills", (m) => {
      const p = (m as PrivatePush<FillData>).data;
      if (isContract(p.symbol)) return later.add(qk.derivatives);
      for (const [key, data] of qc.getQueriesData({ queryKey: qk.allFills })) {
        const [, symbol = ""] = key as [string, string];
        qc.setQueryData(key, applyFillData(data, p, symbol));
      }
    }),
    ws.subscribe("notifications", () => later.add(qk.notifications)),
    ws.subscribe("deposits", () => later.add(qk.deposits)),
    ws.subscribe("withdrawals", () => later.add(qk.withdrawals)),
    ws.subscribe("positions", () => later.add(qk.derivatives)),
    ws.subscribe("risk", () => later.add(qk.derivatives)),
    // Margin accounts (margin design 2026-10-06 §5.2): an ACCOUNT push has
    // the account as it stands and replaces the cached one; when what it
    // holds or owes changed (not only its valuation), its loans, what it
    // may borrow and the ledger reload. A warning, a liquidation or a push
    // without an account reloads everything of margin.
    ws.subscribe("margin", (m) => {
      const p = (m as PrivatePush<MarginPush>).data;
      if (p.type !== "ACCOUNT" || !p.account) return later.add(qk.margin);
      const cached = qc.getQueryData<MarginAccounts>(qk.marginAccounts);
      const key = marginKey(p.account);
      const { page, held } = applyMarginAccount(cached, p.account, pushed.get(key));
      pushed.set(key, holdingsOf(p.account));
      if (page !== cached) qc.setQueryData(qk.marginAccounts, page);
      if (held) for (const key of [qk.marginLoans, qk.marginBorrowable, qk.ledger]) later.add(key);
    }),
    ws.onResync(() => {
      for (const root of privateRoots) later.add([root]);
    }),
  ];
  return () => offs.forEach((off) => off());
}

/**
 * usePrivateSync binds the private channels while signed in and drops the
 * user's cached data on sign-out.
 */
export function usePrivateSync(qc: QueryClient): void {
  const ws = useWs();
  const signedIn = useSession(selectSignedIn);
  useEffect(() => {
    if (!signedIn) {
      for (const root of privateRoots) qc.removeQueries({ queryKey: [root] });
      return;
    }
    return bindPrivate(ws, qc);
  }, [ws, qc, signedIn]);
}
