import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Drawer, KeyValue, Stepper, type DataColumnMeta, type ColumnDef } from "@exchange/ui";

import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge, useEnum } from "../../kit/enums";
import { IdText, Num, TimeText, useTimeText, UserCell } from "../../kit/format";
import { ListTable, PAGE_SIZE, useCursorList, type CursorList } from "../../kit/lists";
import { clean } from "../records/tables";

export type Withdrawal = AdminSchemas["Withdrawal"];
export type WithdrawalQuery = { user_id?: string; asset?: string; network?: string; status?: string };

const right: DataColumnMeta = { align: "right" };

export function useWithdrawals(q: WithdrawalQuery) {
  return useCursorList<Withdrawal>(["admin", "withdrawals", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/withdrawals", { params: { query: { ...clean(q), cursor, limit: PAGE_SIZE } } })),
  );
}

export function WithdrawalsTable({ list, withUser = true, onRowClick }: { list: CursorList<Withdrawal>; withUser?: boolean; onRowClick?: (w: Withdrawal) => void }) {
  const { t } = useTranslation();
  const label = useEnum();
  const columns = useMemo<ColumnDef<Withdrawal, unknown>[]>(
    () => [
      { id: "time", header: t("admin.withdrawals.requested"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      ...(withUser ? [{ id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> } as ColumnDef<Withdrawal, unknown>] : []),
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} unit={row.original.asset} /> },
      { accessorKey: "network", header: t("admin.common.network") },
      { id: "address", header: t("admin.withdrawals.address"), cell: ({ row }) => <IdText value={row.original.address} chars={10} /> },
      { id: "value", header: t("admin.withdrawals.value"), meta: right, cell: ({ row }) => <Num value={row.original.value_usdt} decimals={2} /> },
      {
        id: "risk",
        header: t("admin.withdrawals.risk"),
        cell: ({ row }) => {
          const w = row.original;
          const score = w.risk_score ?? 0;
          return (
            <span className="inline-flex flex-wrap items-center gap-1" title={w.risk_reasons.map((r) => label("riskReason", r)).join("、")}>
              <Badge tone={score >= 50 ? "danger" : score > 0 ? "warn" : "neutral"}>{score}</Badge>
              {w.risk_reasons.length > 0 && <span className="text-xs text-fg-3">{w.risk_reasons.length}</span>}
            </span>
          );
        },
      },
      {
        id: "approvals",
        header: t("admin.withdrawals.approvals"),
        cell: ({ row }) => (
          <span className="tabular-nums">
            {row.original.approvals?.length ?? 0}/{row.original.approvals_required}
          </span>
        ),
      },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="withdrawalStatus" code={row.original.status} /> },
    ],
    [t, withUser, label],
  );
  return <ListTable list={list} columns={columns} getRowId={(w) => w.id} onRowClick={onRowClick} aria-label="withdrawals" />;
}

/** WithdrawalDrawer shows a withdrawal's risk, approvals and progress, with the review actions. */
export function WithdrawalDrawer({ admin, w, onClose }: { admin: Admin; w: Withdrawal; onClose: () => void }) {
  const { t } = useTranslation();
  const label = useEnum();
  const time = useTimeText();
  const steps = [
    { key: "requested", at: w.created_at },
    { key: "approvedAt", at: w.approved_at },
    w.custody ? { key: "submittedAt", at: w.submitted_at } : { key: "broadcastAt", at: w.broadcast_at },
    { key: "confirmedAt", at: w.confirmed_at },
  ];
  const current = steps.reduce((n, s, i) => (s.at ? i : n), 0);
  const reviewable = w.status === "PENDING_REVIEW" && can(admin, "withdrawals.review");
  const review = (approve: boolean) => async (reason: string) =>
    adminData(await adminApi.POST("/admin/v1/withdrawals/{id}/review", { params: { path: { id: w.id } }, body: { approve, reason } }));
  const target = (
    <span className="text-sm">
      <Num value={w.amount} unit={w.asset} /> → <span className="font-mono text-xs">{w.address}</span>
    </span>
  );
  return (
    <Drawer
      open
      onOpenChange={(open) => !open && onClose()}
      title={`${w.amount} ${w.asset}`}
      description={<span className="font-mono">{w.id}</span>}
      actions={<EnumBadge group="withdrawalStatus" code={w.status} />}
    >
      <div className="flex flex-col gap-5">
        <Stepper
          steps={steps.map((s) => ({ key: s.key, title: t(`admin.withdrawals.${s.key}`), description: s.at ? time(s.at) : undefined }))}
          current={current}
        />
        <KeyValue
          items={[
            { label: t("admin.common.user"), value: <UserCell id={w.user_id} /> },
            {
              label: t("admin.common.network"),
              value: `${w.network}${w.internal ? ` · ${t("admin.withdrawals.internal")}` : w.custody ? ` · ${t("admin.withdrawals.custody")}` : ""}`,
            },
            ...(w.custody && w.provider_status
              ? [{ label: t("admin.withdrawals.providerStatus"), value: <EnumBadge group="providerStatus" code={w.provider_status} /> }]
              : []),
            { label: t("admin.withdrawals.address"), value: <span className="font-mono text-xs">{w.address}</span>, copy: w.address },
            { label: t("admin.withdrawals.fee"), value: <Num value={w.fee} unit={w.asset} /> },
            { label: t("admin.withdrawals.value"), value: <Num value={w.value_usdt} decimals={2} unit="USDT" /> },
            {
              label: t("admin.withdrawals.risk"),
              value: (
                <span className="inline-flex flex-wrap gap-1">
                  <Badge tone={(w.risk_score ?? 0) >= 50 ? "danger" : "neutral"}>{w.risk_score ?? 0}</Badge>
                  {w.risk_reasons.map((r) => (
                    <Badge key={r} tone="warn" title={r}>
                      {label("riskReason", r)}
                    </Badge>
                  ))}
                </span>
              ),
            },
            {
              label: t("admin.withdrawals.approvals"),
              value: `${w.approvals?.length ?? 0}/${w.approvals_required}${w.approvals?.length ? ` · ${w.approvals.join("、")}` : ""}`,
            },
            ...(w.tx_hash ? [{ label: t("admin.withdrawals.txHash"), value: <span className="font-mono text-xs">{w.tx_hash}</span>, copy: w.tx_hash }] : []),
            ...(w.reject_reason ? [{ label: t("admin.withdrawals.rejectReason"), value: w.reject_reason }] : []),
          ]}
        />
        {reviewable && (
          <div className="flex gap-2">
            <DangerAction
              trigger={(open) => <Button onClick={open}>{t("admin.withdrawals.approve")}</Button>}
              danger={false}
              title={t("admin.withdrawals.approveTitle")}
              target={target}
              confirmWord={lastFour(w.id)}
              run={review(true)}
              success={t("admin.withdrawals.approved")}
              invalidate={[["admin", "withdrawals"], ["admin", "count", "withdrawals"]]}
              onDone={onClose}
            >
              {(w.approvals?.length ?? 0) + 1 < w.approvals_required && (
                <p className="text-sm text-fg-3">{t("admin.withdrawals.secondReviewer", { n: (w.approvals?.length ?? 0) + 1, required: w.approvals_required })}</p>
              )}
            </DangerAction>
            <DangerAction
              trigger={(open) => (
                <Button variant="danger" onClick={open}>
                  {t("admin.withdrawals.reject")}
                </Button>
              )}
              title={t("admin.withdrawals.rejectTitle")}
              target={target}
              confirmWord={lastFour(w.id)}
              run={review(false)}
              success={t("admin.withdrawals.rejected")}
              invalidate={[["admin", "withdrawals"], ["admin", "count", "withdrawals"]]}
              onDone={onClose}
            />
          </div>
        )}
      </div>
    </Drawer>
  );
}
