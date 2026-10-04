import { errorText } from "@exchange/core";
import { can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, cn, DataTable, ErrorState, Tooltip, type ColumnDef } from "@exchange/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { EnumBadge } from "../../kit/enums";
import { TimeText } from "../../kit/format";
import { Card } from "../../kit/Page";
import { StatusAction } from "./actions";
import { useRisk } from "./data";

type Assessment = AdminSchemas["Assessment"];

/** scoreTone colours a risk score: 70 and up is high, 40 and up elevated. */
export function scoreTone(score: number) {
  return score >= 70 ? "text-danger-strong" : score >= 40 ? "text-warn-strong" : "text-fg-2";
}

/** Score shows a risk score (0–100) in its colour. */
export function Score({ value, className }: { value: number; className?: string }) {
  return <span className={cn("font-mono tabular-nums font-semibold", scoreTone(value), className)}>{value}</span>;
}

/**
 * RiskTab is what the risk rules found on an account (design 2026-10-02
 * §4.1): each assessment with its hits and whether its action was carried
 * out, and the move to RISK_REVIEW and back.
 */
export function RiskTab({ admin, userId, status }: { admin: Admin; userId: string; status: string }) {
  const { t } = useTranslation();
  const risk = useRisk(userId);
  const columns = useMemo<ColumnDef<Assessment, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.time"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      { accessorKey: "source_event_type", header: t("admin.user.risk.event"), cell: ({ row }) => <span className="font-mono text-xs">{row.original.source_event_type}</span> },
      { id: "score", header: t("admin.user.risk.score"), meta: { align: "right" }, cell: ({ row }) => <Score value={row.original.score} /> },
      { id: "action", header: t("admin.user.risk.action"), cell: ({ row }) => <EnumBadge group="riskAction" code={row.original.action} /> },
      {
        id: "enforced", header: t("admin.user.risk.enforced"),
        cell: ({ row }) =>
          row.original.enforced ? <Badge tone="danger">{t("admin.user.risk.enforcedYes")}</Badge> : <span className="text-fg-3">{t("admin.user.risk.enforcedNo")}</span>,
      },
      {
        id: "hits", header: t("admin.user.risk.hits"),
        cell: ({ row }) => (
          <div className="flex flex-wrap gap-1">
            {row.original.hits.map((h) => (
              <Tooltip key={h.rule} content={h.detail || h.rule}>
                <span className="inline-flex items-center gap-1 rounded-1 bg-bg-2 px-1.5 py-0.5 font-mono text-xs">
                  {h.rule}
                  <span className="text-fg-3">+{h.score}</span>
                </span>
              </Tooltip>
            ))}
          </div>
        ),
      },
    ],
    [t],
  );
  const review = status === "RISK_REVIEW";
  return (
    <div className="flex flex-col gap-5">
      {can(admin, "users.status") && (status === "ACTIVE" || review) && (
        <Card title={review ? t("admin.user.risk.clearTitle") : t("admin.user.risk.reviewTitle")}>
          <p className="mb-3 text-sm text-fg-3">{review ? t("admin.user.risk.clearHelp") : t("admin.user.risk.reviewHelp")}</p>
          <div className="max-w-md">
            <StatusAction
              key={status}
              userId={userId}
              status={status}
              initialTo={review ? "ACTIVE" : "RISK_REVIEW"}
              initialReason={review ? "REVIEW_CLEARED" : "SUSPICIOUS_LOGIN"}
            />
          </div>
        </Card>
      )}
      {risk.isError ? (
        <ErrorState message={errorText(risk.error)} onRetry={() => void risk.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={risk.data ?? []}
          getRowId={(a) => a.id}
          loading={risk.isPending}
          density="compact"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.user.risk.none")}</p>}
        />
      )}
    </div>
  );
}
