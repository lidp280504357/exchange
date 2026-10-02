import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, Button, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { lastFour } from "../../kit/actions";
import { EnumBadge, EnumText } from "../../kit/enums";
import { Num, TimeText, UserCell } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { ListTable, PAGE_SIZE, useCursorList, type CursorList } from "../../kit/lists";

const right: DataColumnMeta = { align: "right" };

/** useApprovals pages through the fund operations in a status ("" for all). */
export function useApprovals(status: string) {
  return useCursorList<Approval>(["admin", "approvals", status], async (cursor) =>
    adminData(
      await adminApi.GET("/admin/v1/approvals", {
        params: { query: { status: (status || undefined) as Approval["status"] | undefined, cursor, limit: PAGE_SIZE } },
      }),
    ),
  );
}

/** ApprovalsTable lists fund operations with what the administrator may do with each. */
export function ApprovalsTable({ admin, list }: { admin: Admin; list: CursorList<Approval> }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Approval, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "kind", header: t("admin.ledger.kind"), cell: ({ row }) => <EnumText group="approvalKind" code={row.original.kind} /> },
      { id: "payload", header: t("admin.ledger.payload"), cell: ({ row }) => <Payload a={row.original} /> },
      {
        id: "value",
        header: t("admin.funds.value"),
        meta: right,
        cell: ({ row }) => (row.original.value_usdt ? <Num value={row.original.value_usdt} decimals={2} /> : <span className="text-fg-3">—</span>),
      },
      { id: "mode", header: t("admin.funds.mode"), cell: ({ row }) => <Mode a={row.original} /> },
      {
        accessorKey: "reason",
        header: t("admin.common.reason"),
        cell: ({ row }) => (
          <span className="block max-w-56 truncate" title={row.original.reason}>
            {row.original.reason}
          </span>
        ),
      },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="approvalStatus" code={row.original.status} /> },
      {
        id: "people",
        header: t("admin.funds.people"),
        cell: ({ row }) => (
          <span className="flex flex-col text-xs">
            <span title={t("admin.ledger.requestedBy")}>{row.original.requested_by_email || row.original.requested_by.slice(0, 8)}</span>
            {row.original.decided_by_email && row.original.decided_by !== row.original.requested_by && (
              <span className="text-fg-3" title={t("admin.ledger.decidedBy")}>
                → {row.original.decided_by_email}
              </span>
            )}
          </span>
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => {
          const a = row.original;
          if (a.status !== "PENDING") {
            return a.result ? (
              <span className="block max-w-48 truncate text-xs text-fg-3" title={a.result}>
                {a.result}
              </span>
            ) : null;
          }
          return <Decide admin={admin} a={a} />;
        },
      },
    ],
    [t, admin],
  );
  return <ListTable list={list} columns={columns} getRowId={(a) => a.id} aria-label="fund operations" />;
}

function Payload({ a }: { a: Approval }) {
  const p = a.payload as Record<string, string>;
  return (
    <span className="inline-flex items-center gap-2">
      {p.user_id && <UserCell id={p.user_id} />}
      <Num value={p.amount} unit={p.asset} signed />
      {p.reference && <span className="text-xs text-fg-3">#{p.reference}</span>}
      {p.trade_id && (
        <span className="font-mono text-xs text-fg-3" title={p.tx_hash}>
          {p.network} · {p.trade_id}
        </span>
      )}
    </span>
  );
}

/** Mode says who carries it out: its requester alone, or a second administrator (and why). */
export function Mode({ a }: { a: Approval }) {
  const { t } = useTranslation();
  if (a.mode === "SINGLE") return <Badge tone="info">{t("admin.funds.single")}</Badge>;
  return (
    <span className="inline-flex flex-col gap-0.5">
      <Badge tone="neutral">{t("admin.funds.twoPerson")}</Badge>
      {a.escalation && <span className="text-xs text-fg-3">{t(`admin.funds.escalationShort.${a.escalation}`)}</span>}
    </span>
  );
}

/**
 * Decide offers what the administrator may do with a pending operation:
 * approve or reject another's request; finish their own single-person
 * operation whose outcome was unknown; withdraw their own request.
 */
function Decide({ admin, a }: { admin: Admin; a: Approval }) {
  const { t } = useTranslation();
  if (!can(admin, "ledger.adjust.approve")) return null;
  const p = a.payload as Record<string, string>;
  const mine = a.requested_by === admin.id;
  const target = (
    <span className="inline-flex items-center gap-2">
      <EnumText group="approvalKind" code={a.kind} /> <Num value={p.amount} unit={p.asset} signed />
    </span>
  );
  const run = (approve: boolean) => async (reason: string) =>
    adminData(await adminApi.POST("/admin/v1/approvals/{id}/decide", { params: { path: { id: a.id } }, body: { approve, reason } }));
  const approve = !mine || a.mode === "SINGLE";
  return (
    <span className="flex justify-end gap-1" onClick={(e) => e.stopPropagation()}>
      {approve && (
        <FundAction
          trigger={(open) => (
            <Button size="sm" onClick={open}>
              {mine ? t("admin.funds.finish") : t("admin.ledger.approve")}
            </Button>
          )}
          danger={false}
          title={mine ? t("admin.funds.finishTitle") : t("admin.ledger.approveTitle")}
          target={target}
          confirmWord={lastFour(a.id)}
          run={run(true)}
        />
      )}
      <FundAction
        trigger={(open) => (
          <Button size="sm" variant="ghost" onClick={open}>
            {mine ? t("admin.funds.withdraw") : t("admin.ledger.reject")}
          </Button>
        )}
        title={mine ? t("admin.funds.withdrawTitle") : t("admin.ledger.rejectTitle")}
        target={target}
        confirmWord={lastFour(a.id)}
        run={run(false)}
      />
    </span>
  );
}
