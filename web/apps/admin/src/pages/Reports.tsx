import { errorText } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { DataTable, ErrorState, Segmented, Select, Skeleton, Tabs, TrendChart, type DataColumnMeta, type TrendSeries, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Num } from "../kit/format";
import { Card, Page } from "../kit/Page";

type TradingDay = AdminSchemas["TradingDay"];
type WalletDay = AdminSchemas["WalletDay"];
type DerivativesDay = AdminSchemas["DerivativesDay"];
type OpenInterest = AdminSchemas["OpenInterest"];

const right: DataColumnMeta = { align: "right" };
const DAYS = ["7", "30", "90"];

/** Reports (design §10.3): the read models' daily figures as a chart and a table; the period is a choice, not typing (A5). */
export default function Reports() {
  const { t } = useTranslation();
  const [tab, setTab] = useState("trading");
  const [days, setDays] = useState("30");
  return (
    <Page title={t("admin.nav.reports")}>
      <Tabs
        items={["trading", "wallet", "derivatives", "openInterest"].map((k) => ({ value: k, label: t(`admin.reports.tabs.${k}`) }))}
        value={tab}
        onValueChange={setTab}
        extra={
          tab !== "openInterest" && (
            <Select size="sm" value={days} onValueChange={setDays} options={DAYS.map((d) => ({ value: d, label: t("admin.reports.lastDays", { n: d }) }))} className="w-28" />
          )
        }
      />
      {tab === "trading" && <Trading days={Number(days)} />}
      {tab === "wallet" && <Wallet days={Number(days)} />}
      {tab === "derivatives" && <Futures days={Number(days)} />}
      {tab === "openInterest" && <Interest />}
    </Page>
  );
}

/** byDay sums a numeric field of the rows per day, oldest first. */
function byDay<T extends { day: string }>(rows: T[], fields: Record<string, (r: T) => number>) {
  const days = new Map<string, Record<string, number>>();
  for (const r of rows) {
    const v = days.get(r.day) ?? {};
    for (const [k, f] of Object.entries(fields)) v[k] = (v[k] ?? 0) + f(r);
    days.set(r.day, v);
  }
  return [...days.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([x, values]) => ({ x, label: x.slice(5), values }));
}

function View<T>({
  title, rows, loading, error, retry, columns, rowId, chart, series,
}: {
  title: string;
  rows: T[];
  loading: boolean;
  error: unknown;
  retry: () => void;
  columns: ColumnDef<T, unknown>[];
  rowId: (r: T) => string;
  chart: ReturnType<typeof byDay>;
  series: TrendSeries[];
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState("chart");
  if (error) return <ErrorState message={errorText(error)} onRetry={retry} />;
  return (
    <Card
      title={title}
      extra={
        <Segmented
          size="sm"
          value={mode}
          onValueChange={setMode}
          items={[
            { value: "chart", label: t("admin.reports.chart") },
            { value: "table", label: t("admin.reports.table") },
          ]}
        />
      }
    >
      {mode === "chart" ? (
        loading ? <Skeleton className="h-[240px] w-full" /> : <TrendChart data={chart} series={series} height={240} aria-label={title} />
      ) : (
        <DataTable columns={columns} data={rows} getRowId={rowId} loading={loading} density="compact" />
      )}
    </Card>
  );
}

function Trading({ days }: { days: number }) {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "report", "trading", days], queryFn: async () => adminData(await adminApi.GET("/admin/v1/reports/trading", { params: { query: { days } } })).items });
  const columns = useMemo<ColumnDef<TradingDay, unknown>[]>(
    () => [
      { accessorKey: "day", header: t("admin.reports.day") },
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { accessorKey: "trades", header: t("admin.reports.trades"), meta: right },
      { id: "volume", header: t("admin.reports.volume"), meta: right, cell: ({ row }) => <Num value={row.original.volume} /> },
      { id: "quote", header: t("admin.reports.quoteVolume"), meta: right, cell: ({ row }) => <Num value={row.original.quote_volume} decimals={2} /> },
      { accessorKey: "orders", header: t("admin.reports.orders"), meta: right },
      { accessorKey: "rejected", header: t("admin.reports.rejected"), meta: right },
    ],
    [t],
  );
  const rows = q.data ?? [];
  return (
    <View
      title={t("admin.reports.tabs.trading")}
      rows={rows}
      loading={q.isPending}
      error={q.error}
      retry={() => void q.refetch()}
      columns={columns}
      rowId={(r) => `${r.day}:${r.symbol}`}
      chart={byDay(rows, { trades: (r) => r.trades, orders: (r) => r.orders, value: (r) => (r.symbol.endsWith("-USDT") ? Number(r.quote_volume) : 0) })}
      series={[
        { key: "trades", label: t("admin.reports.trades"), kind: "bar", color: "chart-2" },
        { key: "orders", label: t("admin.reports.orders"), kind: "bar", color: "chart-4" },
        { key: "value", label: `${t("admin.reports.quoteVolume")} (USDT)`, kind: "line", color: "chart-3" },
      ]}
    />
  );
}

function Wallet({ days }: { days: number }) {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "report", "wallet", days], queryFn: async () => adminData(await adminApi.GET("/admin/v1/reports/wallet", { params: { query: { days } } })).items });
  const columns = useMemo<ColumnDef<WalletDay, unknown>[]>(
    () => [
      { accessorKey: "day", header: t("admin.reports.day") },
      { accessorKey: "asset", header: t("admin.common.asset") },
      { accessorKey: "deposits", header: t("admin.reports.deposits"), meta: right },
      { id: "da", header: t("admin.reports.depositAmount"), meta: right, cell: ({ row }) => <Num value={row.original.deposit_amount} /> },
      { accessorKey: "withdrawals", header: t("admin.reports.withdrawals"), meta: right },
      { id: "wa", header: t("admin.reports.withdrawalAmount"), meta: right, cell: ({ row }) => <Num value={row.original.withdrawal_amount} /> },
      { id: "wf", header: t("admin.reports.withdrawalFees"), meta: right, cell: ({ row }) => <Num value={row.original.withdrawal_fees} /> },
    ],
    [t],
  );
  const rows = q.data ?? [];
  return (
    <View
      title={t("admin.reports.tabs.wallet")}
      rows={rows}
      loading={q.isPending}
      error={q.error}
      retry={() => void q.refetch()}
      columns={columns}
      rowId={(r) => `${r.day}:${r.asset}`}
      chart={byDay(rows, { deposits: (r) => r.deposits, withdrawals: (r) => r.withdrawals })}
      series={[
        { key: "deposits", label: t("admin.reports.deposits"), kind: "bar", color: "chart-1" },
        { key: "withdrawals", label: t("admin.reports.withdrawals"), kind: "bar", color: "chart-5" },
      ]}
    />
  );
}

function Futures({ days }: { days: number }) {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "report", "derivatives", days], queryFn: async () => adminData(await adminApi.GET("/admin/v1/reports/derivatives", { params: { query: { days } } })).items });
  const columns = useMemo<ColumnDef<DerivativesDay, unknown>[]>(
    () => [
      { accessorKey: "day", header: t("admin.reports.day") },
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { accessorKey: "fills", header: t("admin.reports.fills"), meta: right },
      { id: "notional", header: t("admin.reports.notional"), meta: right, cell: ({ row }) => <Num value={row.original.notional} decimals={2} /> },
      { id: "fees", header: t("admin.reports.fees"), meta: right, cell: ({ row }) => <Num value={row.original.fees} decimals={2} /> },
      { id: "pnl", header: t("admin.reports.realizedPnl"), meta: right, cell: ({ row }) => <Num value={row.original.realized_pnl} decimals={2} signed /> },
      { id: "fp", header: t("admin.reports.fundingPaid"), meta: right, cell: ({ row }) => <Num value={row.original.funding_paid} /> },
      { id: "fr", header: t("admin.reports.fundingReceived"), meta: right, cell: ({ row }) => <Num value={row.original.funding_received} /> },
      { accessorKey: "liquidations", header: t("admin.reports.liquidations"), meta: right },
      { accessorKey: "adl", header: t("admin.reports.adl"), meta: right },
    ],
    [t],
  );
  const rows = q.data ?? [];
  return (
    <View
      title={t("admin.reports.tabs.derivatives")}
      rows={rows}
      loading={q.isPending}
      error={q.error}
      retry={() => void q.refetch()}
      columns={columns}
      rowId={(r) => `${r.day}:${r.symbol}`}
      chart={byDay(rows, { fills: (r) => r.fills, notional: (r) => Number(r.notional) })}
      series={[
        { key: "fills", label: t("admin.reports.fills"), kind: "bar", color: "chart-2" },
        { key: "notional", label: `${t("admin.reports.notional")} (USDT)`, kind: "line", color: "chart-3" },
      ]}
    />
  );
}

function Interest() {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "report", "oi"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/reports/open-interest")).items });
  const columns = useMemo<ColumnDef<OpenInterest, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "long", header: t("admin.reports.long"), meta: right, cell: ({ row }) => <Num value={row.original.long} /> },
      { id: "short", header: t("admin.reports.short"), meta: right, cell: ({ row }) => <Num value={row.original.short} /> },
      { accessorKey: "positions", header: t("admin.reports.positions"), meta: right },
    ],
    [t],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  return <DataTable columns={columns} data={q.data ?? []} getRowId={(r) => r.symbol} loading={q.isPending} density="compact" />;
}
