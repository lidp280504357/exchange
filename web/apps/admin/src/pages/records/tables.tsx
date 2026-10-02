import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, type DataColumnMeta, type ColumnDef } from "@exchange/ui";

import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { EnumBadge, EnumText } from "../../kit/enums";
import { IdText, Num, TimeText, UserCell } from "../../kit/format";
import { ListTable, pageSize, useCursorList, type CursorList } from "../../kit/lists";

// The record lists the pages and the user drawer share: orders, trades,
// deposits and the audit trail, each paged with its cursor.

export type Order = AdminSchemas["Order"];
export type Trade = AdminSchemas["Trade"];
export type Deposit = AdminSchemas["Deposit"];
export type AuditEntry = AdminSchemas["AuditEntry"];

const right: DataColumnMeta = { align: "right" };

export type OrderQuery = { user_id?: string; order_id?: string; symbol?: string; status?: string; side?: string; from?: string; to?: string };

export function useOrders(q: OrderQuery) {
  return useCursorList<Order>(["admin", "orders", q], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/orders", {
        params: {
          query: {
            ...clean(q),
            status: (q.status || undefined) as Order["status"] as never,
            side: (q.side || undefined) as never,
            cursor,
            limit: pageSize(),
          },
        },
      }),
    ),
  );
}

export function useOrderColumns(withUser = true): ColumnDef<Order, unknown>[] {
  const { t } = useTranslation();
  return useMemo(
    () => [
      { id: "created", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "id", header: t("admin.orders.orderId"), cell: ({ row }) => <IdText value={row.original.order_id} /> },
      ...(withUser ? [{ id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> } as ColumnDef<Order, unknown>] : []),
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "side", header: t("admin.common.side"), cell: ({ row }) => <EnumBadge group="side" code={row.original.side} /> },
      { id: "type", header: t("admin.orders.type"), cell: ({ row }) => <EnumText group="orderType" code={row.original.type} /> },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.price} /> },
      {
        id: "qty",
        header: t("admin.common.quantity"),
        meta: right,
        cell: ({ row }) => <Num value={row.original.quantity ?? row.original.quote_amount} unit={row.original.quantity ? undefined : "Q"} />,
      },
      { id: "filled", header: t("admin.orders.filled"), meta: right, cell: ({ row }) => <Num value={row.original.filled_quantity} /> },
      { id: "filledQuote", header: t("admin.orders.filledQuote"), meta: right, cell: ({ row }) => <Num value={row.original.filled_quote} /> },
      {
        id: "status",
        header: t("admin.common.status"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1" title={row.original.reason || undefined}>
            <EnumBadge group="orderStatus" code={row.original.status} />
            {row.original.reason && <span className="max-w-32 truncate text-xs text-fg-3">{row.original.reason}</span>}
          </span>
        ),
      },
    ],
    [t, withUser],
  );
}

/** OrdersTable lists orders; action adds a last column of what may be done to one (the user page's cancel). */
export function OrdersTable({
  list, withUser = true, onRowClick, action,
}: {
  list: CursorList<Order>;
  withUser?: boolean;
  onRowClick?: (o: Order) => void;
  action?: (o: Order) => ReactNode;
}) {
  const base = useOrderColumns(withUser);
  const columns = useMemo<ColumnDef<Order, unknown>[]>(
    () => (action ? [...base, { id: "act", header: "", meta: right, cell: ({ row }) => action(row.original) }] : base),
    [base, action],
  );
  return <ListTable list={list} columns={columns} getRowId={(o) => o.order_id} onRowClick={onRowClick} aria-label="orders" />;
}

export type TradeQuery = { user_id?: string; symbol?: string; from?: string; to?: string };

export function useTrades(q: TradeQuery) {
  return useCursorList<Trade>(["admin", "trades", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/trades", { params: { query: { ...clean(q), cursor, limit: pageSize() } } })),
  );
}

export function TradesTable({ list, onRowClick }: { list: CursorList<Trade>; onRowClick?: (t: Trade) => void }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Trade, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.time"), cell: ({ row }) => <TimeText value={row.original.executed_at} /> },
      { id: "id", header: t("admin.orders.tradeId"), cell: ({ row }) => <IdText value={row.original.trade_id} /> },
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.price} /> },
      { id: "qty", header: t("admin.common.quantity"), meta: right, cell: ({ row }) => <Num value={row.original.quantity} /> },
      { id: "quote", header: t("admin.orders.quote"), meta: right, cell: ({ row }) => <Num value={row.original.quote_quantity} /> },
      { id: "taker", header: t("admin.orders.taker"), cell: ({ row }) => <EnumBadge group="side" code={row.original.taker_side} /> },
      { id: "buyer", header: t("admin.orders.buyer"), cell: ({ row }) => <Party trade={row.original} side="BUY" /> },
      { id: "seller", header: t("admin.orders.seller"), cell: ({ row }) => <Party trade={row.original} side="SELL" /> },
    ],
    [t],
  );
  return <ListTable list={list} columns={columns} getRowId={(x) => x.trade_id} onRowClick={onRowClick} aria-label="trades" />;
}

/** Party is a trade's buyer or seller: HOUSE's side shows as HOUSE. */
function Party({ trade, side }: { trade: Trade; side: "BUY" | "SELL" }) {
  const { t } = useTranslation();
  if (trade.house_side === side) return <Badge tone="brand">{t("admin.orders.house")}</Badge>;
  return <UserCell id={side === "BUY" ? trade.buyer_user_id : trade.seller_user_id} />;
}

export type DepositQuery = { user_id?: string; asset?: string; network?: string; status?: string; tx_hash?: string };

export function useDeposits(q: DepositQuery) {
  return useCursorList<Deposit>(["admin", "deposits", q], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/deposits", {
        params: { query: { ...clean(q), status: (q.status || undefined) as never, cursor, limit: pageSize() } },
      }),
    ),
  );
}

export function DepositsTable({ list, withUser = true, onRowClick }: { list: CursorList<Deposit>; withUser?: boolean; onRowClick?: (d: Deposit) => void }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Deposit, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.updatedAt"), cell: ({ row }) => <TimeText value={row.original.updated_at} /> },
      ...(withUser ? [{ id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> } as ColumnDef<Deposit, unknown>] : []),
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} unit={row.original.asset} /> },
      { accessorKey: "network", header: t("admin.common.network") },
      { id: "tx", header: t("admin.deposits.txHash"), cell: ({ row }) => <IdText value={row.original.tx_hash} chars={10} /> },
      {
        id: "conf",
        header: t("admin.deposits.confirmations"),
        meta: right,
        cell: ({ row }) => (
          <span className="tabular-nums">
            {row.original.confirmations}/{row.original.required_confirmations}
          </span>
        ),
      },
      {
        id: "status",
        header: t("admin.common.status"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1">
            <EnumBadge group="depositStatus" code={row.original.status} />
            {row.original.unclaimed && <Badge tone="warn">{t("admin.deposits.unclaimed")}</Badge>}
          </span>
        ),
      },
    ],
    [t, withUser],
  );
  return <ListTable list={list} columns={columns} getRowId={(d) => d.deposit_id} onRowClick={onRowClick} aria-label="deposits" />;
}

export type AuditQuery = { actor?: string; target?: string; event_type?: string; from?: string; to?: string };

export function useAudit(q: AuditQuery) {
  return useCursorList<AuditEntry>(["admin", "audit", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/audit-logs", { params: { query: { ...clean(q), cursor, limit: pageSize() } } })),
  );
}

export function AuditTable({ list, onRowClick }: { list: CursorList<AuditEntry>; onRowClick?: (e: AuditEntry) => void }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<AuditEntry, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.time"), cell: ({ row }) => <TimeText value={row.original.occurred_at} /> },
      { accessorKey: "actor", header: t("admin.audit.actor") },
      { accessorKey: "target", header: t("admin.audit.target"), cell: ({ row }) => <Target value={row.original.target} /> },
      { accessorKey: "event_type", header: t("admin.audit.eventType") },
      {
        id: "payload",
        header: t("admin.audit.payload"),
        cell: ({ row }) => <span className="block max-w-96 truncate font-mono text-xs text-fg-2">{summary(row.original.payload)}</span>,
      },
    ],
    [t],
  );
  return <ListTable list={list} columns={columns} getRowId={(e) => e.event_id} onRowClick={onRowClick} aria-label="audit" />;
}

/** Target links a user target ("user:<id>") to the user's drawer. */
function Target({ value }: { value: string }) {
  if (value.startsWith("user:")) return <UserCell id={value.slice(5)} />;
  return <span className="font-mono text-xs">{value}</span>;
}

/** summary renders an audit payload's action and reason on one line. */
function summary(payload: unknown): string {
  if (payload && typeof payload === "object") {
    const p = payload as Record<string, unknown>;
    const parts = [p.action, p.reason].filter((x) => typeof x === "string" && x);
    if (parts.length) return parts.join(" · ");
  }
  return JSON.stringify(payload);
}

/** clean drops empty filters (the API treats "" as a value). */
export function clean<T extends Record<string, string | undefined>>(q: T): Partial<T> {
  const out: Partial<T> = {};
  for (const [k, v] of Object.entries(q)) if (v) (out as Record<string, string>)[k] = v;
  return out;
}
