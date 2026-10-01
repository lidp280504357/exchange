import { errorText } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, DataTable, ErrorState, Input, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { EnumText, useEnum } from "../kit/enums";
import { Num, TimeText } from "../kit/format";
import { Card, Page } from "../kit/Page";

type Run = AdminSchemas["ReconciliationRun"];
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

function Reconciliation() {
  const { t } = useTranslation();
  const label = useEnum();
  const q = useQuery({ queryKey: ["admin", "reconciliation"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/ledger/reconciliation")) });
  const columns = useMemo<ColumnDef<Run, unknown>[]>(
    () => [
      { id: "check", header: t("admin.ledger.check"), cell: ({ row }) => <span title={row.original.check}>{label("check", row.original.check)}</span> },
      { id: "time", header: t("admin.ledger.lastRun"), cell: ({ row }) => <TimeText value={row.original.started_at} /> },
      {
        id: "mismatches",
        header: t("admin.ledger.mismatches"),
        meta: right,
        cell: ({ row }) => <Badge tone={row.original.mismatches ? "danger" : "success"}>{row.original.mismatches}</Badge>,
      },
      {
        id: "examples",
        header: t("admin.ledger.examples"),
        cell: ({ row }) => (
          <span className="block max-w-[28rem] truncate font-mono text-xs text-fg-2">{row.original.details.map((d) => `${d.key}: ${d.detail}`).join(" · ")}</span>
        ),
      },
    ],
    [t, label],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const bad = (q.data?.latest ?? []).some((r) => r.mismatches > 0);
  return (
    <div className="flex flex-col gap-4">
      {q.data && !bad && <div className="rounded-2 border border-success px-4 py-2 text-sm text-fg-2">{t("admin.ledger.allGood")}</div>}
      <DataTable columns={columns} data={q.data?.latest ?? []} getRowId={(r) => r.check} loading={q.isPending} density="compact" />
      {(q.data?.failures.length ?? 0) > 0 && (
        <Card title={t("admin.ledger.failures")}>
          <DataTable columns={columns} data={q.data!.failures} getRowId={(r) => `${r.check}@${r.started_at}`} density="compact" />
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
