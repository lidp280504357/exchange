import { dec, errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, ErrorState, Input, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../kit/actions";
import { EnumBadge, EnumText, useEnum } from "../kit/enums";
import { FilterBar, options, useFilters } from "../kit/filters";
import { Num, TimeText, UserCell } from "../kit/format";
import { ListTable, PAGE_SIZE, useCursorList } from "../kit/lists";
import { Card, Page } from "../kit/Page";

type Approval = AdminSchemas["Approval"];
type Run = AdminSchemas["ReconciliationRun"];
type SystemBalance = { account_type: string; asset: string; available: string; frozen: string };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const right: DataColumnMeta = { align: "right" };

/**
 * Ledger (design §10.3): two-person approvals of adjustments and insurance
 * fund contributions, requesting an adjustment, the reconciliation of the
 * invariants and the system accounts.
 */
export default function Ledger({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState("approvals");
  const tabs = ["approvals", ...(can(admin, "ledger.adjust.request") ? ["adjust"] : []), "reconciliation", "system"];
  return (
    <Page title={t("admin.nav.ledger")}>
      <Tabs items={tabs.map((k) => ({ value: k, label: t(`admin.ledger.tabs.${k}`) }))} value={tab} onValueChange={setTab} />
      {tab === "approvals" && <Approvals admin={admin} />}
      {tab === "adjust" && <Adjust />}
      {tab === "reconciliation" && <Reconciliation />}
      {tab === "system" && <System />}
    </Page>
  );
}

function Approvals({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["approval_status"]);
  const status = filters.values.approval_status || "PENDING";
  const list = useCursorList<Approval>(["admin", "approvals", status], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/approvals", { params: { query: { status: status === "ALL" ? undefined : (status as never), cursor, limit: PAGE_SIZE } } })),
  );
  const decider = can(admin, "ledger.adjust.approve");
  const columns = useMemo<ColumnDef<Approval, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "kind", header: t("admin.ledger.kind"), cell: ({ row }) => <EnumText group="approvalKind" code={row.original.kind} /> },
      { id: "payload", header: t("admin.ledger.payload"), cell: ({ row }) => <Payload a={row.original} /> },
      { accessorKey: "reason", header: t("admin.common.reason"), cell: ({ row }) => <span className="block max-w-64 truncate" title={row.original.reason}>{row.original.reason}</span> },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="approvalStatus" code={row.original.status} /> },
      { id: "decided", header: t("admin.ledger.decidedBy"), cell: ({ row }) => <span className="text-xs">{row.original.decided_by ?? "—"}</span> },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => (row.original.status === "PENDING" && decider ? <Decide a={row.original} /> : row.original.result ? <span className="text-xs text-fg-3">{row.original.result}</span> : null),
      },
    ],
    [t, decider],
  );
  return (
    <>
      <FilterBar
        page="approvals"
        filters={filters}
        defs={[
          {
            key: "approval_status",
            label: t("admin.common.status"),
            kind: "select",
            options: [
              ...options(label("approvalStatus", "PENDING"), [], (c) => c),
              { value: "ALL", label: t("admin.common.all") },
              ...["EXECUTED", "REJECTED", "FAILED"].map((s) => ({ value: s, label: label("approvalStatus", s) })),
            ],
          },
        ]}
      />
      <ListTable list={list} columns={columns} getRowId={(a) => a.id} />
    </>
  );
}

function Payload({ a }: { a: Approval }) {
  const p = a.payload as Record<string, string>;
  return (
    <span className="inline-flex items-center gap-2">
      {p.user_id && <UserCell id={p.user_id} />}
      <Num value={p.amount} unit={p.asset} signed />
    </span>
  );
}

function Decide({ a }: { a: Approval }) {
  const { t } = useTranslation();
  const p = a.payload as Record<string, string>;
  const target = (
    <span className="inline-flex items-center gap-2">
      <EnumText group="approvalKind" code={a.kind} /> <Num value={p.amount} unit={p.asset} signed />
    </span>
  );
  const run = (approve: boolean) => async (reason: string) =>
    adminData(await adminApi.POST("/admin/v1/approvals/{id}/decide", { params: { path: { id: a.id } }, body: { approve, reason } }));
  const invalidate = [["admin", "approvals"], ["admin", "count", "approvals"], ["admin", "derivatives"]];
  return (
    <span className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
      <DangerAction
        trigger={(open) => (
          <Button size="sm" onClick={open}>
            {t("admin.ledger.approve")}
          </Button>
        )}
        danger={false}
        title={t("admin.ledger.approveTitle")}
        target={target}
        confirmWord={lastFour(a.id)}
        run={run(true)}
        success={t("admin.ledger.decided")}
        invalidate={invalidate}
      />
      <DangerAction
        trigger={(open) => (
          <Button size="sm" variant="ghost" onClick={open}>
            {t("admin.ledger.reject")}
          </Button>
        )}
        title={t("admin.ledger.rejectTitle")}
        target={target}
        confirmWord={lastFour(a.id)}
        run={run(false)}
        success={t("admin.ledger.decided")}
        invalidate={invalidate}
      />
    </span>
  );
}

function Adjust() {
  const { t } = useTranslation();
  const [user, setUser] = useState("");
  const [asset, setAsset] = useState("USDT");
  const [amount, setAmount] = useState("");
  const userOk = UUID.test(user.trim());
  const amountOk = dec.isDecimal(amount.trim()) && !dec.isZero(amount.trim());
  return (
    <Card>
      <p className="mb-4 text-sm text-fg-3">{t("admin.ledger.adjustHint")}</p>
      <div className="flex flex-wrap items-start gap-3">
        <Input size="sm" value={user} onValueChange={setUser} placeholder={t("admin.ledger.userId")} containerClassName="w-80" error={user !== "" && !userOk ? t("admin.ledger.invalidUser") : undefined} />
        <Input size="sm" value={asset} onValueChange={(v) => setAsset(v.toUpperCase())} placeholder="USDT" containerClassName="w-28" />
        <Input
          size="sm"
          value={amount}
          onValueChange={setAmount}
          inputMode="decimal"
          placeholder="100"
          hint={t("admin.ledger.amountHint")}
          containerClassName="w-48"
          error={amount !== "" && !amountOk ? t("admin.ledger.invalidAmount") : undefined}
        />
        <DangerAction
          trigger={(open) => (
            <Button size="sm" disabled={!userOk || !amountOk || !asset} onClick={open}>
              {t("admin.ledger.submit")}
            </Button>
          )}
          danger={dec.isDecimal(amount.trim()) && dec.sign(amount.trim()) < 0}
          title={t("admin.ledger.tabs.adjust")}
          target={
            <span className="inline-flex items-center gap-2">
              <span className="font-mono text-xs">{user.trim()}</span> <Num value={amount.trim()} unit={asset} signed />
            </span>
          }
          confirmWord={lastFour(user.trim())}
          run={async (reason) =>
            adminData(await adminApi.POST("/admin/v1/ledger/adjustments", { body: { user_id: user.trim(), asset, amount: amount.trim(), reason } }))
          }
          success={t("admin.ledger.submitted")}
          invalidate={[["admin", "approvals"], ["admin", "count", "approvals"]]}
          onDone={() => setAmount("")}
        />
      </div>
    </Card>
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
