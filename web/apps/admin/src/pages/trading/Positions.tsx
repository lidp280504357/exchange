import { dec, errorText, formatPercent } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, ErrorState, Tabs, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../../kit/actions";
import { FilterBar, useFilters } from "../../kit/filters";
import { Num, UserCell } from "../../kit/format";
import { Page } from "../../kit/Page";

type Position = AdminSchemas["RiskPosition"];

const right: DataColumnMeta = { align: "right" };

/** Margin ratios from here are close to liquidation (the warning threshold of the risk view). */
const WATCH = "0.5";

/**
 * Positions (design 2026-10-02 §3, C3): every user's open contract
 * positions, riskiest first (margin ratio, then size), refreshed every 5
 * seconds; ?view=watch keeps those under watch (warned, taken over,
 * margin ratio ≥ 0.5). HOUSE's positions are the counterparty of users'
 * and are marked; a user's position can be closed at the market.
 */
export default function Positions({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const filters = useFilters(["view", "symbol", "user_id"]);
  const f = filters.values;
  const watch = f.view === "watch";
  const symbol = f.symbol?.toUpperCase() || undefined;
  const q = useQuery({
    queryKey: ["admin", "positions", { watch, symbol, user_id: f.user_id }],
    queryFn: async () =>
      adminData(
        await adminApi.GET("/admin/v1/positions", {
          params: { query: { symbol, user_id: f.user_id || undefined, watch: watch ? "true" : undefined } },
        }),
      ),
    refetchInterval: 5_000,
  });
  const house = q.data?.house_user_id ?? null;
  const [closing, setClosing] = useState<Position | null>(null);
  const act = can(admin, "derivatives.write");
  const columns = useMemo<ColumnDef<Position, unknown>[]>(
    () => [
      {
        id: "user", header: t("admin.common.user"),
        cell: ({ row }) =>
          row.original.user_id === house ? <Badge tone="brand">{t("admin.orders.house")}</Badge> : <UserCell id={row.original.user_id} />,
      },
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      {
        id: "side", header: t("admin.money.direction"),
        cell: ({ row }) => {
          const long = dec.gt(row.original.quantity, "0");
          return (
            <span className="inline-flex items-center gap-1">
              <Badge tone={long ? "up" : "down"}>{long ? t("admin.money.long") : t("admin.money.short")}</Badge>
              {row.original.position_side !== "BOTH" && <span className="text-xs text-fg-3">{row.original.position_side}</span>}
            </span>
          );
        },
      },
      { id: "qty", header: t("admin.common.quantity"), meta: right, cell: ({ row }) => <Num value={dec.abs(row.original.quantity)} /> },
      { id: "entry", header: t("admin.money.entry"), meta: right, cell: ({ row }) => <Num value={row.original.entry_price} /> },
      { id: "mark", header: t("admin.money.mark"), meta: right, cell: ({ row }) => <Num value={row.original.mark_price} /> },
      { id: "upnl", header: t("admin.money.upnl"), meta: right, cell: ({ row }) => <Num value={row.original.unrealized_pnl} signed /> },
      {
        id: "margin", header: t("admin.money.margin"), meta: right,
        cell: ({ row }) => (
          <span className="flex flex-col items-end">
            <Num value={row.original.margin} />
            <span className="whitespace-nowrap text-xs text-fg-3">
              {row.original.margin_mode === "CROSS" ? t("admin.money.cross") : t("admin.money.isolated")} · {row.original.leverage}x
            </span>
          </span>
        ),
      },
      { id: "ratio", header: t("admin.derivatives.marginRatio"), meta: right, cell: ({ row }) => <Ratio p={row.original} /> },
      {
        id: "liq", header: t("admin.money.liquidation"), meta: right,
        cell: ({ row }) => (row.original.liquidation_price ? <Num value={row.original.liquidation_price} className="text-warn" /> : <span className="text-fg-3">—</span>),
      },
      ...(act
        ? [
            {
              id: "act", header: "", meta: right,
              cell: ({ row }: { row: { original: Position } }) =>
                row.original.user_id === house || row.original.liquidating ? null : (
                  <Button size="sm" variant="danger" onClick={() => setClosing(row.original)}>
                    {t("admin.money.forceClose")}
                  </Button>
                ),
            },
          ]
        : []),
    ],
    [t, act, house],
  );
  return (
    <Page title={t("admin.nav.positions")} help={t("admin.positions.help")}>
      <Tabs
        items={[
          { value: "all", label: t("admin.positions.all") },
          { value: "watch", label: t("admin.positions.watch") },
        ]}
        value={watch ? "watch" : "all"}
        onValueChange={(v) => filters.set({ view: v === "watch" ? "watch" : "" })}
        aria-label={t("admin.nav.positions")}
      />
      <FilterBar
        page="positions"
        filters={filters}
        defs={[
          { key: "symbol", label: t("admin.common.symbol"), kind: "text", placeholder: "BTC-USDT-PERP", width: 160 },
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
        ]}
      />
      {q.data?.truncated && <p className="text-sm text-warn">{t("admin.positions.truncated", { n: q.data.positions.length })}</p>}
      {q.isError ? (
        <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={q.data?.positions ?? []}
          getRowId={(p) => p.position_id}
          loading={q.isPending}
          density="compact"
          aria-label="positions"
          empty={<p className="py-4 text-center text-sm text-fg-3">{watch ? t("admin.positions.noneAtRisk") : t("admin.money.noPositions")}</p>}
        />
      )}
      {closing && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setClosing(null)}
          title={t("admin.money.closeTitle")}
          description={t("admin.money.closeHelp")}
          target={
            <span className="inline-flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs">{closing.user_id}</span>
              <span className="font-medium">{closing.symbol}</span>
              <Num value={closing.quantity} signed />
              {closing.position_side !== "BOTH" && <span className="text-xs text-fg-3">{closing.position_side}</span>}
            </span>
          }
          confirmWord={closing.symbol.split("-")[0] ?? closing.symbol}
          run={async (reason) =>
            adminData(
              await adminApi.POST("/admin/v1/users/{id}/positions/close", {
                params: { path: { id: closing.user_id } },
                body: { symbol: closing.symbol, position_side: closing.position_side, reason },
              }),
            )
          }
          success={t("admin.money.closed")}
          invalidate={[["admin", "positions"], ["admin", "user", closing.user_id]]}
        />
      )}
    </Page>
  );
}

/** Ratio is a position's margin ratio, toned by how close it is to liquidation, with its liquidation state. */
function Ratio({ p }: { p: Position }) {
  const { t } = useTranslation();
  if (p.liquidating) return <Badge tone="danger">{t("admin.enum.liquidationKind.STARTED")}</Badge>;
  const ratio = p.margin_ratio;
  const tone = ratio && dec.gte(ratio, WATCH) ? "danger" : p.warned_at ? "warn" : "neutral";
  return (
    <span className="inline-flex flex-col items-end gap-0.5">
      {ratio ? <Badge tone={tone}>{formatPercent(ratio, 2, false)}</Badge> : <span className="text-fg-3">—</span>}
      {p.warned_at && <span className="text-xs text-warn">{t("admin.enum.liquidationKind.WARNING")}</span>}
    </span>
  );
}
