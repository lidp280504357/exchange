import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Drawer, EmptyState, KeyValue, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge } from "../../kit/enums";
import { IdText, Num, TimeText, useTimeText, UserCell } from "../../kit/format";
import { ListTable, pageSize, RowActions, useCursorList, type CursorList } from "../../kit/lists";
import { todoKey } from "../../live";
import { clean } from "../records/tables";

// The deposits that need a person (design 2026-10-02 §4.3), straight from
// wallet-service: those booked to UNCLAIMED_DEPOSIT, unsupported tokens,
// backfills a custodian callback disagreed with; and the backfills still
// waiting for their callback.

export type ReviewDeposit = AdminSchemas["ReviewDeposit"];
export type ReviewView = "attention" | "manual";
export type ReviewQuery = { user_id?: string; network?: string };

const right: DataColumnMeta = { align: "right" };

export function useReviewDeposits(view: ReviewView, q: ReviewQuery) {
  return useCursorList<ReviewDeposit>(["admin", "deposits", "review", view, q], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/deposits/review", {
        params: {
          query: {
            ...clean(q), attention: view === "attention" ? "true" : undefined, manual_pending: view === "manual" ? "true" : undefined, cursor,
            limit: pageSize(),
          },
        },
      }),
    ),
  );
}

/** Problem says why a deposit waits: its reason, an unsupported token, a disagreeing callback. */
function Problem({ d }: { d: ReviewDeposit }) {
  const { t } = useTranslation();
  if (d.discrepancy) {
    return (
      <span className="flex max-w-64 flex-col items-start gap-0.5">
        <Badge tone="danger">{t("admin.depositReview.discrepancy")}</Badge>
        <span className="truncate text-xs text-fg-3" title={d.discrepancy}>
          {d.discrepancy}
        </span>
      </span>
    );
  }
  return <EnumBadge group="depositReason" code={d.reason} />;
}

/** Amount is a deposit's amount in its asset, or the token's amount without one. */
function Amount({ d }: { d: ReviewDeposit }) {
  const { t } = useTranslation();
  if (d.asset) return <Num value={d.amount} unit={d.asset} />;
  return (
    <span className="inline-flex items-center gap-1.5">
      <Num value={d.amount} />
      <Badge tone="danger">{t("admin.depositReview.unsupported")}</Badge>
    </span>
  );
}

/** Source is automatic, or a backfill with who entered it. */
function Source({ d }: { d: ReviewDeposit }) {
  return (
    <span className="inline-flex flex-col items-start gap-0.5">
      <EnumBadge group="depositSource" code={d.source} />
      {d.entered_by && <span className="text-xs text-fg-3">{d.entered_by}</span>}
    </span>
  );
}

export function ReviewDepositsTable({
  admin, view, list, onRowClick,
}: {
  admin: Admin;
  view: ReviewView;
  list: CursorList<ReviewDeposit>;
  onRowClick: (d: ReviewDeposit) => void;
}) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<ReviewDeposit, unknown>[]>(
    () => [
      { id: "time", header: t("admin.depositReview.detected"), cell: ({ row }) => <TimeText value={row.original.detected_at} /> },
      { id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> },
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Amount d={row.original} /> },
      { accessorKey: "network", header: t("admin.common.network") },
      ...(view === "attention"
        ? [{ id: "problem", header: t("admin.depositReview.problem"), cell: ({ row }) => <Problem d={row.original} /> } as ColumnDef<ReviewDeposit, unknown>]
        : [
            {
              id: "trade",
              header: t("admin.depositReview.tradeId"),
              cell: ({ row }) => <IdText value={row.original.trade_id} chars={14} />,
            } as ColumnDef<ReviewDeposit, unknown>,
          ]),
      { id: "source", header: t("admin.depositReview.source"), cell: ({ row }) => <Source d={row.original} /> },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="depositStatus" code={row.original.status} /> },
      ...(view === "attention" && can(admin, "deposits.review")
        ? [
            {
              id: "actions",
              header: "",
              cell: ({ row }) => (
                <RowActions className="flex justify-end gap-1">
                  <Decisions d={row.original} size="sm" />
                </RowActions>
              ),
            } as ColumnDef<ReviewDeposit, unknown>,
          ]
        : []),
    ],
    [t, view, admin],
  );
  return (
    <ListTable
      list={list}
      columns={columns}
      getRowId={(d) => d.id}
      onRowClick={onRowClick}
      empty={<EmptyState compact title={view === "attention" ? t("admin.depositReview.empty") : t("admin.depositReview.emptyManual")} />}
      aria-label="deposits to handle"
    />
  );
}

/** creditable reports whether a deposit can go to its user: booked to UNCLAIMED_DEPOSIT in its own asset, undecided. */
const creditable = (d: ReviewDeposit) => d.attention && d.unclaimed && !!d.asset && !!d.journal_id && !d.discrepancy;

/**
 * Decisions are what can be done with a deposit that waits: credit it to
 * its user (only one booked to UNCLAIMED_DEPOSIT, at its own asset and
 * amount) or reject it (mark it handled, no funds move).
 */
function Decisions({ d, size = "md", onDone }: { d: ReviewDeposit; size?: "sm" | "md"; onDone?: () => void }) {
  const { t } = useTranslation();
  if (!d.attention) return null;
  const target: ReactNode = (
    <span className="inline-flex flex-wrap items-center gap-2 text-sm">
      <Amount d={d} />
      <span className="text-fg-3">{d.network}</span>
      <span className="font-mono text-xs">{d.user_id}</span>
    </span>
  );
  const decide = (credit: boolean) => async (reason: string) => {
    const path = { params: { path: { id: d.id } }, body: { reason } };
    return adminData(
      credit ? await adminApi.POST("/admin/v1/deposits/{id}/credit", path) : await adminApi.POST("/admin/v1/deposits/{id}/reject", path),
    );
  };
  const invalidate = [["admin", "deposits"], todoKey, ["admin", "user"]];
  return (
    <>
      {creditable(d) && (
        <DangerAction
          trigger={(open) => (
            <Button size={size} onClick={open}>
              {t("admin.depositReview.credit")}
            </Button>
          )}
          danger={false}
          title={t("admin.depositReview.creditTitle")}
          description={t("admin.depositReview.creditHint")}
          target={target}
          confirmWord={lastFour(d.id)}
          run={decide(true)}
          success={t("admin.depositReview.credited")}
          invalidate={invalidate}
          onDone={onDone}
        />
      )}
      <DangerAction
        trigger={(open) => (
          <Button size={size} variant="secondary" onClick={open}>
            {t("admin.depositReview.reject")}
          </Button>
        )}
        title={t("admin.depositReview.rejectTitle")}
        description={t("admin.depositReview.rejectHint")}
        target={target}
        confirmWord={lastFour(d.id)}
        run={decide(false)}
        success={t("admin.depositReview.rejected")}
        invalidate={invalidate}
        onDone={onDone}
      />
    </>
  );
}

/** ReviewDepositDrawer shows a deposit from wallet-service with its decision, or the decisions it waits for. */
export function ReviewDepositDrawer({ admin, d, onClose }: { admin: Admin; d: ReviewDeposit; onClose: () => void }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const decide = d.attention && can(admin, "deposits.review");
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={d.asset ? `${d.amount} ${d.asset}` : `${d.amount} · ${t("admin.depositReview.unsupported")}`}
      description={<span className="font-mono">{d.id}</span>}
      actions={<EnumBadge group="depositStatus" code={d.status} />}
    >
      <div className="flex flex-col gap-5">
        <KeyValue
          items={[
            { label: t("admin.common.user"), value: <UserCell id={d.user_id} /> },
            { label: t("admin.common.amount"), value: <Amount d={d} /> },
            { label: t("admin.common.network"), value: d.network },
            { label: t("admin.deposits.address"), value: <span className="font-mono text-xs">{d.address}</span>, copy: d.address },
            { label: t("admin.deposits.txHash"), value: <span className="font-mono text-xs">{d.tx_hash || "—"}</span>, copy: d.tx_hash || undefined },
            ...(d.trade_id ? [{ label: t("admin.depositReview.tradeId"), value: <span className="font-mono text-xs">{d.trade_id}</span>, copy: d.trade_id }] : []),
            { label: t("admin.deposits.confirmations"), value: `${d.confirmations}/${d.required_confirmations}` },
            { label: t("admin.depositReview.detected"), value: <TimeText value={d.detected_at} /> },
            { label: t("admin.depositReview.source"), value: <Source d={d} /> },
            ...(d.source === "MANUAL"
              ? [
                  {
                    label: t("admin.depositReview.callback"),
                    value: d.callback_at ? t("admin.depositReview.callbackAt", { time: time(d.callback_at) }) : t("admin.depositReview.noCallback"),
                  },
                ]
              : []),
            ...(d.reason ? [{ label: t("admin.deposits.reason"), value: <EnumBadge group="depositReason" code={d.reason} /> }] : []),
            ...(d.discrepancy ? [{ label: t("admin.depositReview.discrepancy"), value: <span className="text-danger">{d.discrepancy}</span> }] : []),
            { label: t("admin.depositReview.journal"), value: <IdText value={d.journal_id} chars={13} /> },
            ...(d.resolution
              ? [
                  {
                    label: t("admin.depositReview.resolution"),
                    value: (
                      <span className="flex flex-col items-end gap-0.5">
                        <EnumBadge group="depositResolution" code={d.resolution} />
                        <span className="text-xs text-fg-3">{t("admin.depositReview.resolvedBy", { by: d.resolved_by, time: time(d.resolved_at!) })}</span>
                        {d.resolution_note && <span>{d.resolution_note}</span>}
                      </span>
                    ),
                  },
                ]
              : []),
            ...(d.release_journal_id ? [{ label: t("admin.depositReview.releaseJournal"), value: <IdText value={d.release_journal_id} chars={13} /> }] : []),
          ]}
        />
        {decide && (
          <div className="flex flex-col gap-2">
            {!creditable(d) && (
              <p className="text-sm text-fg-3">{d.discrepancy ? t("admin.depositReview.noCreditDiscrepancy") : t("admin.depositReview.noCredit")}</p>
            )}
            <div className="flex gap-2">
              <Decisions d={d} onDone={onClose} />
            </div>
          </div>
        )}
      </div>
    </Drawer>
  );
}
