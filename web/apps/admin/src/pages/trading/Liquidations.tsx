import { adminApi, adminData, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { EnumBadge, useEnum } from "../../kit/enums";
import { ALL, FilterBar, options, useFilters } from "../../kit/filters";
import { Num, TimeText, UserCell } from "../../kit/format";
import { ListTable, PAGE_SIZE, useCursorList } from "../../kit/lists";
import { Page } from "../../kit/Page";

type Step = AdminSchemas["LiquidationStep"];

const right: DataColumnMeta = { align: "right" };
const KINDS = ["WARNING", "STARTED", "FILLED", "ADL"];

/** filled is an amount of a step that trades (a fill or an auto-deleveraging); the others have none to show. */
const filled = (s: Step, v: string) => (s.kind === "FILLED" || s.kind === "ADL" ? v : null);
const DAYS = ["1", "7", "30", "90"];

/**
 * Liquidations (design 2026-10-02 §3, C3): every step of the liquidation
 * engine (warning, take-over, fills, auto-deleveraging), newest first,
 * by kind, contract, user and period (the read model, seconds behind).
 */
export default function Liquidations(_: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["kind", "symbol", "user_id", "days"]);
  const f = filters.values;
  const days = DAYS.includes(f.days ?? "") ? Number(f.days) : 30;
  const q = {
    days, kind: (f.kind || undefined) as never, symbol: f.symbol?.toUpperCase() || undefined, user_id: f.user_id || undefined,
  };
  const list = useCursorList<Step>(["admin", "derivatives", "liquidations", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/derivatives/liquidations", { params: { query: { ...q, cursor, limit: PAGE_SIZE } } })),
  );
  const columns = useMemo<ColumnDef<Step, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.time"), cell: ({ row }) => <TimeText value={row.original.occurred_at} /> },
      { id: "kind", header: t("admin.derivatives.kind"), cell: ({ row }) => <EnumBadge group="liquidationKind" code={row.original.kind} /> },
      { id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> },
      {
        id: "symbol", header: t("admin.common.symbol"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1">
            {row.original.symbol || "—"}
            {row.original.cross && <Badge tone="neutral">{t("admin.money.cross")}</Badge>}
          </span>
        ),
      },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={filled(row.original, row.original.price)} /> },
      { id: "qty", header: t("admin.common.quantity"), meta: right, cell: ({ row }) => <Num value={filled(row.original, row.original.quantity)} /> },
      { id: "mark", header: t("admin.derivatives.mark"), meta: right, cell: ({ row }) => <Num value={row.original.mark_price} /> },
      { id: "pnl", header: t("admin.reports.realizedPnl"), meta: right, cell: ({ row }) => <Num value={filled(row.original, row.original.realized_pnl)} signed /> },
      { id: "ins", header: t("admin.derivatives.insurancePaid"), meta: right, cell: ({ row }) => <Num value={filled(row.original, row.original.insurance_paid)} /> },
    ],
    [t],
  );
  return (
    <Page title={t("admin.nav.liquidations")} help={t("admin.liquidations.help")}>
      <FilterBar
        page="liquidations"
        filters={filters}
        defs={[
          { key: "kind", label: t("admin.derivatives.kind"), kind: "select", options: options(t("admin.common.all"), KINDS, (k) => label("liquidationKind", k)), width: 130 },
          { key: "symbol", label: t("admin.common.symbol"), kind: "text", placeholder: "BTC-USDT-PERP", width: 160 },
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          {
            key: "days", label: t("admin.liquidations.period"), kind: "select", width: 120,
            options: DAYS.map((d) => ({ value: d === "30" ? ALL : d, label: t("admin.reports.lastDays", { n: Number(d) }) })),
          },
        ]}
      />
      <ListTable list={list} columns={columns} getRowId={(s) => s.event_id} aria-label="liquidations" />
    </Page>
  );
}
