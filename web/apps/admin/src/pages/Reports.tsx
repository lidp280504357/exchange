import { errorText } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { DataTable, ErrorState, Segmented, Select, Skeleton, Tabs, TrendChart, type DataColumnMeta, type TrendSeries, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { createContext, useContext, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Num } from "../kit/format";
import { Card, Page } from "../kit/Page";
import { SignedChart } from "../kit/SignedChart";

type TradingDay = AdminSchemas["TradingDay"];
type WalletDay = AdminSchemas["WalletDay"];
type DerivativesDay = AdminSchemas["DerivativesDay"];
type OpenInterest = AdminSchemas["OpenInterest"];
type UsersBucket = AdminSchemas["UsersBucket"];
type HousePnLBucket = AdminSchemas["HousePnLBucket"];
type Bucket = "day" | "week" | "month";

const right: DataColumnMeta = { align: "right" };
const PERIODS = ["7", "30", "90", "custom"];
const TABS = ["trading", "wallet", "derivatives", "users", "housePnl", "openInterest"];

/** Period is a report's period as the API takes it: the last days, or from and to; and the bucket. */
type Period = { days?: number; from?: string; to?: string; bucket: Bucket };

/** today is the UTC date, as the reports count days. */
const today = () => new Date().toISOString().slice(0, 10);
const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000).toISOString().slice(0, 10);

/**
 * Reports (design §10.3, 2026-10-02 §4.6): the read models' figures as a
 * chart and a table, per day, week or month over the last 7, 30 or 90
 * days or between two dates; the period is a choice, not typing (A5).
 */
export default function Reports() {
  const { t } = useTranslation();
  const [tab, setTab] = useState("trading");
  const [preset, setPreset] = useState("30");
  const [from, setFrom] = useState(daysAgo(89));
  const [to, setTo] = useState(today());
  const [bucket, setBucket] = useState<Bucket>("day");
  const mode = useState("chart");
  const period: Period = preset === "custom" ? { from, to, bucket } : { days: Number(preset), bucket };
  const dateInput = "h-8 rounded-1 border border-line-1 bg-bg-1 px-2 text-sm text-fg-1";
  return (
    <Page title={t("admin.nav.reports")} help={t("admin.reports.help")}>
      <Tabs
        items={TABS.map((k) => ({ value: k, label: t(`admin.reports.tabs.${k}`) }))}
        value={tab}
        onValueChange={setTab}
        extra={
          tab !== "openInterest" && (
            <span className="flex flex-wrap items-center gap-2">
              <Select
                size="sm"
                value={preset}
                onValueChange={setPreset}
                options={PERIODS.map((d) => ({ value: d, label: d === "custom" ? t("admin.reports.custom") : t("admin.reports.lastDays", { n: d }) }))}
                className="w-28"
                aria-label={t("admin.reports.period")}
              />
              {preset === "custom" && (
                <>
                  <input type="date" value={from} max={to} onChange={(e) => e.target.value && setFrom(e.target.value)} className={dateInput} aria-label={t("admin.common.from")} />
                  <span className="text-fg-3">–</span>
                  <input type="date" value={to} min={from} max={today()} onChange={(e) => e.target.value && setTo(e.target.value)} className={dateInput} aria-label={t("admin.common.to")} />
                </>
              )}
              <Segmented
                size="sm"
                value={bucket}
                onValueChange={(v) => setBucket(v as Bucket)}
                items={(["day", "week", "month"] as const).map((b) => ({ value: b, label: t(`admin.reports.bucket.${b}`) }))}
                aria-label={t("admin.reports.bucketLabel")}
              />
            </span>
          )
        }
      />
      <ViewMode.Provider value={mode}>
        {tab === "trading" && <Trading period={period} />}
        {tab === "wallet" && <Wallet period={period} />}
        {tab === "derivatives" && <Futures period={period} />}
        {tab === "users" && <Users period={period} />}
        {tab === "housePnl" && <HousePnL period={period} />}
      </ViewMode.Provider>
      {tab === "openInterest" && <Interest />}
    </Page>
  );
}

/** ViewMode is the chart or table every report shows, kept across the tabs. */
const ViewMode = createContext<[string, (mode: string) => void]>(["chart", () => undefined]);

/** label is a bucket's name on the axis: the day, its week's Monday, or the month. */
function label(day: string, bucket: Bucket) {
  return bucket === "month" ? day.slice(0, 7) : day.slice(5);
}

/** byBucket sums a numeric field of the rows per bucket, oldest first. */
function byBucket<T extends { day: string }>(rows: T[], bucket: Bucket, fields: Record<string, (r: T) => number>) {
  const days = new Map<string, Record<string, number>>();
  for (const r of rows) {
    const v = days.get(r.day) ?? {};
    for (const [k, f] of Object.entries(fields)) v[k] = (v[k] ?? 0) + f(r);
    days.set(r.day, v);
  }
  return [...days.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([x, values]) => ({ x, label: label(x, bucket), values }));
}

function View<T>({
  title, rows, loading, error, retry, columns, rowId, chart, series, signed, note,
}: {
  title: string;
  rows: T[];
  loading: boolean;
  error: unknown;
  retry: () => void;
  columns: ColumnDef<T, unknown>[];
  rowId: (r: T) => string;
  chart: ReturnType<typeof byBucket>;
  series: TrendSeries[];
  /** Values below zero: the chart draws a zero line and bars down. */
  signed?: boolean;
  note?: string;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useContext(ViewMode);
  if (error) return <ErrorState message={errorText(error)} onRetry={retry} />;
  const Chart = signed ? SignedChart : TrendChart;
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
      {note && <p className="mb-3 rounded-2 bg-warn/10 px-3 py-2 text-xs text-warn">{note}</p>}
      {mode === "chart" ? (
        loading ? <Skeleton className="h-[240px] w-full" /> : <Chart data={chart} series={series} height={240} aria-label={title} />
      ) : (
        <DataTable columns={columns} data={rows} getRowId={rowId} loading={loading} density="compact" />
      )}
    </Card>
  );
}

/** useReport reads a report for the period. */
function useReport<T>(name: string, period: Period, read: (query: Period) => Promise<T>) {
  return useQuery({ queryKey: ["admin", "report", name, period], queryFn: () => read(period) });
}

function Trading({ period }: { period: Period }) {
  const { t } = useTranslation();
  const q = useReport("trading", period, async (query) => adminData(await adminApi.GET("/admin/v1/reports/trading", { params: { query } })).items);
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
      chart={byBucket(rows, period.bucket, { trades: (r) => r.trades, orders: (r) => r.orders, value: (r) => (r.symbol.endsWith("-USDT") ? Number(r.quote_volume) : 0) })}
      series={[
        { key: "trades", label: t("admin.reports.trades"), kind: "bar", color: "chart-2" },
        { key: "orders", label: t("admin.reports.orders"), kind: "bar", color: "chart-4" },
        { key: "value", label: `${t("admin.reports.quoteVolume")} (USDT)`, kind: "line", color: "chart-3" },
      ]}
    />
  );
}

function Wallet({ period }: { period: Period }) {
  const { t } = useTranslation();
  const q = useReport("wallet", period, async (query) => adminData(await adminApi.GET("/admin/v1/reports/wallet", { params: { query } })).items);
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
      chart={byBucket(rows, period.bucket, { deposits: (r) => r.deposits, withdrawals: (r) => r.withdrawals })}
      series={[
        { key: "deposits", label: t("admin.reports.deposits"), kind: "bar", color: "chart-1" },
        { key: "withdrawals", label: t("admin.reports.withdrawals"), kind: "bar", color: "chart-5" },
      ]}
    />
  );
}

function Futures({ period }: { period: Period }) {
  const { t } = useTranslation();
  const q = useReport("derivatives", period, async (query) => adminData(await adminApi.GET("/admin/v1/reports/derivatives", { params: { query } })).items);
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
      chart={byBucket(rows, period.bucket, { fills: (r) => r.fills, notional: (r) => Number(r.notional) })}
      series={[
        { key: "fills", label: t("admin.reports.fills"), kind: "bar", color: "chart-2" },
        { key: "notional", label: `${t("admin.reports.notional")} (USDT)`, kind: "line", color: "chart-3" },
      ]}
    />
  );
}

function Users({ period }: { period: Period }) {
  const { t } = useTranslation();
  const q = useReport("users", period, async (query) => adminData(await adminApi.GET("/admin/v1/reports/users", { params: { query } })));
  const columns = useMemo<ColumnDef<UsersBucket, unknown>[]>(
    () => [
      { accessorKey: "day", header: t("admin.reports.day") },
      { accessorKey: "registered", header: t("admin.reports.registered"), meta: right },
      { accessorKey: "signed_in", header: t("admin.reports.signedIn"), meta: right },
      { accessorKey: "traders", header: t("admin.reports.traders"), meta: right },
      { accessorKey: "depositors", header: t("admin.reports.depositors"), meta: right },
      { accessorKey: "total", header: t("admin.reports.totalUsers"), meta: right },
    ],
    [t],
  );
  const rows = q.data?.items ?? [];
  return (
    <View
      title={t("admin.reports.tabs.users")}
      rows={[...rows].reverse()}
      loading={q.isPending}
      error={q.error}
      retry={() => void q.refetch()}
      columns={columns}
      rowId={(r) => r.day}
      note={q.data?.partial.includes("bots") ? t("admin.reports.botsUnknown") : undefined}
      chart={byBucket(rows, period.bucket, { registered: (r) => r.registered, signedIn: (r) => r.signed_in, traders: (r) => r.traders, depositors: (r) => r.depositors })}
      series={[
        { key: "registered", label: t("admin.reports.registered"), kind: "bar", color: "chart-1" },
        { key: "depositors", label: t("admin.reports.depositors"), kind: "bar", color: "chart-5" },
        { key: "signedIn", label: t("admin.reports.signedIn"), kind: "line", color: "chart-2" },
        { key: "traders", label: t("admin.reports.traders"), kind: "line", color: "chart-3" },
      ]}
    />
  );
}

/** usdtColumn shows a result in USDT with its sign. */
function usdtColumn(key: Exclude<keyof HousePnLBucket, "day">, name: string): ColumnDef<HousePnLBucket, unknown> {
  return { id: key, header: name, meta: right, cell: ({ row }) => <Num value={row.original[key]} decimals={2} signed /> };
}

function HousePnL({ period }: { period: Period }) {
  const { t } = useTranslation();
  const q = useReport("house-pnl", period, async (query) => adminData(await adminApi.GET("/admin/v1/reports/house-pnl", { params: { query } })));
  const columns = useMemo<ColumnDef<HousePnLBucket, unknown>[]>(
    () => [
      { accessorKey: "day", header: t("admin.reports.day") },
      usdtColumn("spot_pnl", t("admin.reports.spotPnl")),
      usdtColumn("contracts_pnl", t("admin.reports.contractsPnl")),
      usdtColumn("funding", t("admin.reports.funding")),
      usdtColumn("total", t("admin.reports.totalPnl")),
      usdtColumn("cumulative", t("admin.reports.cumulative")),
      usdtColumn("spot_result", t("admin.reports.spotResult")),
    ],
    [t],
  );
  const rows = q.data?.items ?? [];
  const unpriced = q.data?.unpriced ?? [];
  const usdt = (v: number) => `${v >= 0 ? "" : "−"}${Math.abs(Math.round(v * 100) / 100)}`;
  return (
    <View
      title={t("admin.reports.tabs.housePnl")}
      rows={[...rows].reverse()}
      loading={q.isPending}
      error={q.error}
      retry={() => void q.refetch()}
      columns={columns}
      rowId={(r) => r.day}
      signed
      note={unpriced.length ? t("admin.reports.unpriced", { pairs: unpriced.join(", ") }) : undefined}
      chart={byBucket(rows, period.bucket, {
        spot: (r) => Number(r.spot_pnl), contracts: (r) => Number(r.contracts_pnl), funding: (r) => Number(r.funding), cumulative: (r) => Number(r.cumulative),
      })}
      series={[
        { key: "spot", label: `${t("admin.reports.spotPnl")} (USDT)`, kind: "bar", color: "chart-1", format: usdt },
        { key: "contracts", label: `${t("admin.reports.contractsPnl")} (USDT)`, kind: "bar", color: "chart-2", format: usdt },
        { key: "funding", label: `${t("admin.reports.funding")} (USDT)`, kind: "bar", color: "chart-4", format: usdt },
        { key: "cumulative", label: `${t("admin.reports.cumulative")} (USDT)`, kind: "line", color: "chart-3", format: usdt },
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
