import { dec } from "@exchange/core";
import type { Admin } from "@exchange/core/api/admin";
import { DataTable, ErrorState, Segmented, Stat, TrendChart, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { FilterBar, useFilters } from "../../kit/filters";
import { Num } from "../../kit/format";
import { Card, Page } from "../../kit/Page";
import { useMarginInterest, type InterestQuery, type MarginInterestBucket } from "./api";
import { Rate } from "./common";

const right: DataColumnMeta = { align: "right" };
const BUCKETS = ["day", "week", "month"] as const;

/**
 * Margin interest (design 2026-10-06 §4.3, §8; A55): per day, week or
 * month (UTC) and asset, the interest charged and repaid, what users owe
 * at the bucket's end, the average principal and hourly rate, and the
 * accounts charged (the ledger's interest rows and the read model
 * margin_interest); the chart sums the assets in USDT.
 */
export default function Interest(_: { admin: Admin }) {
  const { t } = useTranslation();
  const filters = useFilters(["bucket", "asset"]);
  const f = filters.values;
  const bucket: InterestQuery["bucket"] = (BUCKETS as readonly string[]).includes(f.bucket ?? "") ? (f.bucket as InterestQuery["bucket"]) : "day";
  const q = useMarginInterest({ days: 30, bucket, asset: f.asset?.toUpperCase() || undefined });
  const rows = q.data?.items ?? [];
  const sum = (pick: (r: MarginInterestBucket) => string | null) => rows.reduce((s, r) => (pick(r) !== null ? dec.add(s, pick(r)!) : s), "0");
  const chart = useMemo(() => {
    const days = new Map<string, { charged: number; repaid: number }>();
    for (const r of rows) {
      const v = days.get(r.day) ?? { charged: 0, repaid: 0 };
      v.charged += Number(r.charged_usdt ?? 0);
      v.repaid += Number(r.repaid_usdt ?? 0);
      days.set(r.day, v);
    }
    return [...days.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([x, values]) => ({ x, label: x.slice(5), values }));
  }, [rows]);
  const columns = useMemo<ColumnDef<MarginInterestBucket, unknown>[]>(
    () => [
      { accessorKey: "day", header: t("admin.reports.day") },
      { accessorKey: "asset", header: t("admin.common.asset") },
      { id: "charged", header: t("admin.margin.interestReport.charged"), meta: right, cell: ({ row }) => <Num value={row.original.charged} /> },
      { id: "repaid", header: t("admin.margin.interestReport.repaid"), meta: right, cell: ({ row }) => <Num value={row.original.repaid} /> },
      { id: "owed", header: t("admin.margin.interestReport.owed"), meta: right, cell: ({ row }) => <Num value={row.original.owed} /> },
      { id: "principal", header: t("admin.margin.interestReport.principal"), meta: right, cell: ({ row }) => <Num value={row.original.principal_avg} decimals={4} /> },
      { id: "rate", header: t("admin.margin.fields.rate"), meta: right, cell: ({ row }) => <Rate hourly={row.original.hourly_rate_avg} /> },
      { id: "accounts", header: t("admin.margin.interestReport.accounts"), meta: right, cell: ({ row }) => row.original.accounts },
      { id: "usdt", header: t("admin.margin.interestReport.chargedUsdt"), meta: right, cell: ({ row }) => <Num value={row.original.charged_usdt} decimals={2} /> },
    ],
    [t],
  );
  return (
    <Page
      title={t("admin.nav.marginInterest")}
      help={t("admin.margin.interestReport.help")}
      actions={
        <Segmented
          size="sm"
          value={bucket}
          onValueChange={(v) => filters.set({ bucket: v === "day" ? "" : v })}
          items={BUCKETS.map((b) => ({ value: b, label: t(`admin.margin.interestReport.${b}`) }))}
          aria-label={t("admin.margin.interestReport.bucket")}
        />
      }
    >
      <FilterBar page="margin-interest" filters={filters} defs={[{ key: "asset", label: t("admin.common.asset"), kind: "text", placeholder: "USDT", width: 120 }]} />
      {q.isError ? (
        <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />
      ) : (
        <>
          <div className="grid gap-3 sm:grid-cols-3">
            <Stat size="sm" label={t("admin.margin.interestReport.chargedTotal")} value={<Num value={sum((r) => r.charged_usdt)} decimals={2} />} unit="USDT" loading={q.isPending} />
            <Stat size="sm" label={t("admin.margin.interestReport.repaidTotal")} value={<Num value={sum((r) => r.repaid_usdt)} decimals={2} />} unit="USDT" loading={q.isPending} />
            <Stat size="sm" label={t("admin.margin.interestReport.rows")} value={rows.length} loading={q.isPending} />
          </div>
          <Card title={t("admin.margin.interestReport.chart")}>
            <TrendChart
              data={chart}
              series={[
                { key: "charged", label: t("admin.margin.interestReport.charged"), kind: "bar", color: "chart-1" },
                { key: "repaid", label: t("admin.margin.interestReport.repaid"), kind: "bar", color: "chart-2" },
              ]}
              height={220}
              aria-label={t("admin.margin.interestReport.chart")}
            />
          </Card>
          <DataTable columns={columns} data={rows} getRowId={(r) => `${r.day} ${r.asset}`} loading={q.isPending} density="compact" aria-label="margin-interest-report" />
        </>
      )}
    </Page>
  );
}
