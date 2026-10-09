import { errorText } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, KeyTag, Skeleton, SummaryRow, SummaryTable } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import type { CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useEnum } from "../../kit/enums";
import { TimeText } from "../../kit/format";
import { Card } from "../../kit/Page";

// The status cards the overview and the system health page share, and the
// reconciliation's checks the health and ledger pages share.

/**
 * ReconciliationTable lists reconciliation runs (A94): a check each - its
 * name over its key, consistent or not, how many mismatches, when - the
 * mismatches' examples once a row is opened. Compact (half a page): the
 * name alone, and the mismatches and the time of day as the value.
 */
export function ReconciliationTable({ runs, label, compact, at }: { runs: AdminSchemas["ReconciliationRun"][]; label: string; compact?: boolean; at?: (r: AdminSchemas["ReconciliationRun"]) => string }) {
  const { t } = useTranslation();
  const name = useEnum();
  return (
    <SummaryTable label={label} compact={compact}>
      {runs.map((r) => (
        <SummaryRow
          key={at ? at(r) : r.check}
          data-testid={`recon-${r.check}`}
          title={name("check", r.check)}
          source={compact ? undefined : <KeyTag>{r.check}</KeyTag>}
          status={r.mismatches ? { tone: "danger", label: t("admin.summary.recon.bad") } : { tone: "success", label: t("admin.summary.recon.ok") }}
          summary={
            compact ? (
              <span className="flex flex-wrap items-baseline gap-x-3">
                {r.mismatches > 0 && <span className="text-danger-strong">{t("admin.summary.recon.some", { n: r.mismatches })}</span>}
                <span className="text-xs text-fg-3">
                  <TimeText value={r.started_at} style="timeSeconds" />
                </span>
              </span>
            ) : r.mismatches ? (
              <span className="text-danger-strong">{t("admin.summary.recon.some", { n: r.mismatches })}</span>
            ) : (
              <span className="text-fg-3">{t("admin.summary.recon.allOk")}</span>
            )
          }
          action={
            compact ? undefined : (
              <span className="text-xs text-fg-3">
                <TimeText value={r.started_at} />
              </span>
            )
          }
          details={
            r.details.length > 0 ? (
              <ul className="flex flex-col gap-1.5">
                {r.details.map((d, i) => (
                  <li key={i} className="flex flex-wrap items-baseline gap-2">
                    <KeyTag>{d.key}</KeyTag>
                    <span className="break-all text-fg-1">{d.detail}</span>
                  </li>
                ))}
                {r.mismatches > r.details.length && <li className="text-xs text-fg-3">{t("admin.summary.recon.firstOnly", { n: r.details.length })}</li>}
              </ul>
            ) : undefined
          }
        />
      ))}
    </SummaryTable>
  );
}

/** CustodySummary is the custodian at a glance: reachable, short of nothing, no callback or withdrawal stuck. */
export function CustodySummary({ className, style }: { className?: string; style?: CSSProperties }) {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "custody"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/custody")), refetchInterval: 30_000 });
  const o = q.data;
  const short = (o?.checks ?? []).filter((c) => Number(c.shortfall) > 0);
  return (
    <Card
      title={t("admin.overview.custody")}
      className={className}
      style={style}
      extra={
        <Link className="text-sm text-info-strong hover:underline" to="/custody">
          {t("admin.common.details")}
        </Link>
      }
    >
      {q.isError ? (
        <p className="text-sm text-danger-strong">{errorText(q.error)}</p>
      ) : !o ? (
        <Skeleton className="h-6 w-64" />
      ) : (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          {!o.configured ? (
            <Badge tone="neutral">{t("admin.overview.custodyOff")}</Badge>
          ) : o.error ? (
            <Badge tone="danger">{t("admin.overview.custodyDown", { error: o.error })}</Badge>
          ) : (
            <Badge tone="success">{t("admin.overview.custodyOk", { n: o.coins.length })}</Badge>
          )}
          {short.map((c) => (
            <Badge key={`${c.holder}/${c.asset}`} tone="danger">
              {t("admin.overview.custodyShort", { asset: c.asset, amount: c.shortfall })}
            </Badge>
          ))}
          {o.callbacks.attention > 0 && <Badge tone="warn">{t("admin.overview.custodyCallbacks", { n: o.callbacks.attention })}</Badge>}
          {o.submitted.count > 0 && <Badge tone="info">{t("admin.overview.custodySubmitted", { n: o.submitted.count })}</Badge>}
        </div>
      )}
    </Card>
  );
}
