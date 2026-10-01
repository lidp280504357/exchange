import { errorText } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, DataTable, ErrorState, Stat, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Num, TimeText } from "../kit/format";
import { Card, Page } from "../kit/Page";

type House = AdminSchemas["House"];
type Asset = House["assets"][number];
type Pair = House["pairs"][number];
type Position = { symbol?: string; position_side?: string; quantity?: string; entry_price?: string; mark_price?: string | null; unrealized_pnl?: string | null; margin?: string; leverage?: number };

const right: DataColumnMeta = { align: "right" };

/**
 * HOUSE (ADR-0013, ADR-0015): its inventory valued at the last prices
 * (withdrawable assets first; internal ones go below zero once sold), what
 * it traded per pair with the result at those prices, and its contract
 * positions; refreshed every 30 seconds.
 */
export default function HousePage() {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "house"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/house")), refetchInterval: 30_000 });
  const assetColumns = useMemo<ColumnDef<Asset, unknown>[]>(
    () => [
      {
        accessorKey: "asset",
        header: t("admin.common.asset"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-2">
            {row.original.asset}
            {row.original.backed && <Badge tone="info">{t("admin.common.backed")}</Badge>}
          </span>
        ),
      },
      { id: "balance", header: t("admin.house.balance"), meta: right, cell: ({ row }) => <Num value={row.original.balance} signed={!row.original.backed} /> },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.price} /> },
      { id: "value", header: t("admin.house.value"), meta: right, cell: ({ row }) => <Num value={row.original.value_usdt} decimals={2} signed={!row.original.backed} /> },
    ],
    [t],
  );
  const pairColumns = useMemo<ColumnDef<Pair, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { accessorKey: "trades", header: t("admin.house.trades"), meta: right },
      { id: "bought", header: t("admin.house.bought"), meta: right, cell: ({ row }) => <Num value={row.original.bought_base} /> },
      { id: "sold", header: t("admin.house.sold"), meta: right, cell: ({ row }) => <Num value={row.original.sold_base} /> },
      { id: "netBase", header: t("admin.house.netBase"), meta: right, cell: ({ row }) => <Num value={row.original.net_base} signed /> },
      { id: "netQuote", header: t("admin.house.netQuote"), meta: right, cell: ({ row }) => <Num value={row.original.net_quote} decimals={2} signed /> },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.price} /> },
      { id: "pnl", header: t("admin.house.pnl"), meta: right, cell: ({ row }) => <Num value={row.original.pnl_usdt} decimals={2} signed /> },
      { id: "last", header: t("admin.house.lastAt"), cell: ({ row }) => <TimeText value={row.original.last_at} /> },
    ],
    [t],
  );
  const positionColumns = useMemo<ColumnDef<Position, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "qty", header: t("admin.common.quantity"), meta: right, cell: ({ row }) => <Num value={row.original.quantity} signed /> },
      { id: "entry", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.entry_price} /> },
      { id: "mark", header: t("admin.derivatives.mark"), meta: right, cell: ({ row }) => <Num value={row.original.mark_price} /> },
      { id: "upnl", header: t("admin.house.unrealized"), meta: right, cell: ({ row }) => <Num value={row.original.unrealized_pnl} decimals={2} signed /> },
    ],
    [t],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const h = q.data;
  const contracts = (h?.contracts ?? []) as Position[];
  return (
    <Page title={t("admin.house.title")} help={t("admin.house.help")}>
      {h && h.partial.length > 0 && <div className="rounded-2 border border-warn px-4 py-2 text-sm text-fg-2">{t("admin.partial", { parts: h.partial.join(", ") })}</div>}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {(
          [
            ["inventory", h?.totals.inventory_usdt, false],
            ["backed", h?.totals.backed_usdt, false],
            ["internal", h?.totals.internal_usdt, true],
            ["pnl", h?.totals.pnl_usdt, true],
          ] as const
        ).map(([key, value, signed]) => (
          <div key={key} className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Stat label={t(`admin.house.${key}`)} value={h ? <Num value={value} decimals={2} unit="USDT" signed={signed} /> : undefined} loading={!h} />
          </div>
        ))}
      </div>
      <Card title={t("admin.house.assets")}>
        <DataTable columns={assetColumns} data={h?.assets ?? []} getRowId={(a) => a.asset} loading={q.isPending} density="compact" />
      </Card>
      <Card title={t("admin.house.pairs")}>
        <DataTable columns={pairColumns} data={h?.pairs ?? []} getRowId={(p) => p.symbol} loading={q.isPending} density="compact" />
      </Card>
      <Card title={t("admin.house.contracts")}>
        <DataTable
          columns={positionColumns}
          data={contracts}
          getRowId={(p) => `${p.symbol}:${p.position_side}`}
          loading={q.isPending}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.house.noContracts")}</p>}
        />
      </Card>
    </Page>
  );
}
