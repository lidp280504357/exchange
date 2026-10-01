import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Button, type ColumnDef } from "@exchange/ui";
import { ArrowRight } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge, EnumText, useEnum } from "../../kit/enums";
import { ALL, FilterBar, useFilters } from "../../kit/filters";
import { TimeText, UserCell } from "../../kit/format";
import { ListTable, PAGE_SIZE, useCursorList } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { todoKey } from "../../live";

type Request = AdminSchemas["IdentityRequest"];
type Status = Request["status"];

const STATUSES: Status[] = ["PENDING_REVIEW", "APPROVED", "REJECTED"];

/**
 * IdentityRequests is the queue of identity rebind requests (design
 * 2026-10-02 §4.1): a user whose only email or phone is lost proves a new
 * one and asks to move to it; an administrator approves (the identity takes
 * the new value, the user is told) or rejects. Values are masked.
 */
export default function IdentityRequests({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["status", "user_id"]);
  const f = filters.values;
  // No filter is the queue of pending requests; ANY lists every status.
  const status = f.status === "ANY" ? undefined : ((f.status || "PENDING_REVIEW") as Status);
  const userId = f.user_id?.trim() || undefined;
  const list = useCursorList<Request>(["admin", "identity-requests", status ?? "ANY", userId ?? ""], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/identity-requests", { params: { query: { status, user_id: userId, cursor, limit: PAGE_SIZE } } })),
  );
  const decide = can(admin, "users.security");
  const [deciding, setDeciding] = useState<{ r: Request; approve: boolean } | null>(null);
  const columns = useMemo<ColumnDef<Request, unknown>[]>(
    () => [
      { id: "created", header: t("admin.common.createdAt"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> },
      { id: "kind", header: t("admin.idreq.kind"), cell: ({ row }) => <EnumText group="identityKind" code={row.original.kind} /> },
      {
        id: "change", header: t("admin.idreq.change"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-1.5 font-mono text-xs">
            <span className="text-fg-3">{row.original.current_value || t("admin.idreq.gone")}</span>
            <ArrowRight size={12} className="text-fg-3" />
            <span>{row.original.new_value}</span>
          </span>
        ),
      },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="identityRequestStatus" code={row.original.status} /> },
      {
        id: "decided", header: t("admin.idreq.decided"),
        cell: ({ row }) =>
          row.original.decided_at ? (
            <span className="flex flex-col text-xs">
              <span>
                {row.original.decided_by} · <TimeText value={row.original.decided_at} />
              </span>
              {row.original.reason && <span className="text-fg-3">{row.original.reason}</span>}
            </span>
          ) : (
            <span className="text-fg-3">—</span>
          ),
      },
      ...(decide
        ? [
            {
              id: "act", header: "", meta: { align: "right" as const },
              cell: ({ row }: { row: { original: Request } }) =>
                row.original.status === "PENDING_REVIEW" ? (
                  <span className="inline-flex gap-1.5">
                    <Button size="sm" variant="primary" onClick={() => setDeciding({ r: row.original, approve: true })}>
                      {t("admin.idreq.approve")}
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => setDeciding({ r: row.original, approve: false })}>
                      {t("admin.idreq.reject")}
                    </Button>
                  </span>
                ) : null,
            },
          ]
        : []),
    ],
    [t, decide],
  );
  return (
    <Page title={t("admin.nav.identityRequests")} help={t("admin.idreq.help")}>
      <FilterBar
        page="identity-requests"
        filters={filters}
        defs={[
          {
            key: "status", label: t("admin.common.status"), kind: "select", width: 150,
            options: [
              { value: ALL, label: t("admin.idreq.queue") },
              { value: "ANY", label: t("admin.common.all") },
              ...STATUSES.slice(1).map((s) => ({ value: s, label: label("identityRequestStatus", s) })),
            ],
          },
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
        ]}
      />
      <ListTable
        list={list}
        columns={columns}
        getRowId={(r) => r.id}
        aria-label={t("admin.nav.identityRequests")}
        empty={<p className="py-6 text-center text-sm text-fg-3">{t("admin.idreq.empty")}</p>}
      />
      {deciding && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setDeciding(null)}
          danger={deciding.approve}
          title={deciding.approve ? t("admin.idreq.approveTitle") : t("admin.idreq.rejectTitle")}
          description={deciding.approve ? t("admin.idreq.approveDesc") : undefined}
          target={
            <span className="inline-flex items-center gap-1.5 font-mono text-xs">
              {deciding.r.current_value || t("admin.idreq.gone")} <ArrowRight size={12} /> {deciding.r.new_value}
            </span>
          }
          confirmWord={lastFour(deciding.r.id)}
          run={async (reason) =>
            adminData(
              await adminApi.POST("/admin/v1/identity-requests/{id}/decide", {
                params: { path: { id: deciding.r.id } },
                body: { approve: deciding.approve, reason },
              }),
            )
          }
          success={deciding.approve ? t("admin.idreq.approved") : t("admin.idreq.rejected")}
          invalidate={[["admin", "identity-requests"], todoKey, ["admin", "user", deciding.r.user_id]]}
        />
      )}
    </Page>
  );
}
