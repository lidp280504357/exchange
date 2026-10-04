import { errorText } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { Badge, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import type { CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Card } from "../../kit/Page";

// The status cards the overview and the system health page share.

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
