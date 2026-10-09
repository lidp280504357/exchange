import { errorText } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { cn, DataTable, ErrorState, Input, Skeleton, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { EnumText } from "../kit/enums";
import { Num } from "../kit/format";
import { Card, Page } from "../kit/Page";
import { ReconciliationTable } from "./system/status";

type SystemBalance = { account_type: string; asset: string; available: string; frozen: string };

const right: DataColumnMeta = { align: "right" };

/**
 * Ledger (design 2026-10-02 §3, funds → reconciliation and system
 * accounts): the reconciliation of the invariants and the system
 * accounts. Fund operations have their own pages (adjustments, approvals).
 */
export default function Ledger() {
  const { t } = useTranslation();
  const [tab, setTab] = useState("reconciliation");
  return (
    <Page title={t("admin.nav.ledger")}>
      <Tabs items={["reconciliation", "system"].map((k) => ({ value: k, label: t(`admin.ledger.tabs.${k}`) }))} value={tab} onValueChange={setTab} />
      {tab === "reconciliation" && <Reconciliation />}
      {tab === "system" && <System />}
    </Page>
  );
}

/** Reconciliation lists every check's latest run, then the latest runs that found mismatches; a run's examples open with its row (A94). */
function Reconciliation() {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "reconciliation"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/ledger/reconciliation")) });
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  if (!q.data) return <Skeleton className="h-96 w-full" />;
  const bad = q.data.latest.filter((r) => r.mismatches > 0).length;
  return (
    <div className="flex flex-col gap-4">
      <div className={cn("rounded-2 border px-4 py-2 text-sm text-fg-2", bad ? "border-danger" : "border-success")}>
        {bad ? t("admin.health.reconBad", { n: bad }) : t("admin.ledger.allGood")}
      </div>
      <ReconciliationTable runs={q.data.latest} label={t("admin.ledger.tabs.reconciliation")} />
      {q.data.failures.length > 0 && (
        <Card title={t("admin.ledger.failures")}>
          <ReconciliationTable runs={q.data.failures} label={t("admin.ledger.failures")} at={(r) => `${r.check}@${r.started_at}`} />
        </Card>
      )}
    </div>
  );
}

function System() {
  const { t } = useTranslation();
  const [asset, setAsset] = useState("");
  const q = useQuery({
    queryKey: ["admin", "system-balances", asset],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/ledger/system-balances", { params: { query: asset ? { asset } : {} } })).balances as SystemBalance[],
  });
  const columns = useMemo<ColumnDef<SystemBalance, unknown>[]>(
    () => [
      { id: "type", header: t("admin.ledger.accountType"), cell: ({ row }) => <EnumText group="accountType" code={row.original.account_type} /> },
      { accessorKey: "asset", header: t("admin.common.asset") },
      { id: "available", header: t("admin.users.available"), meta: right, cell: ({ row }) => <Num value={row.original.available} signed /> },
      { id: "frozen", header: t("admin.users.frozen"), meta: right, cell: ({ row }) => <Num value={row.original.frozen} /> },
    ],
    [t],
  );
  return (
    <div className="flex flex-col gap-3">
      <Input size="sm" value={asset} onValueChange={(v) => setAsset(v.toUpperCase().trim())} placeholder={t("admin.ledger.assetFilter")} containerClassName="w-56" clearable onClear={() => setAsset("")} />
      {q.isError ? (
        <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : (
        <DataTable columns={columns} data={q.data ?? []} getRowId={(b) => `${b.account_type}:${b.asset}`} loading={q.isPending} density="compact" virtual height={560} />
      )}
    </div>
  );
}
