import { errorText } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, DataTable, ErrorState, KeyValue, Skeleton, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { EnumBadge, useEnum } from "../../kit/enums";
import { Num, TimeText } from "../../kit/format";
import { Pulse, stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";
import { CustodySummary } from "./status";

type Service = AdminSchemas["ServiceHealth"];

const right: DataColumnMeta = { align: "right" };

/**
 * System health (design 2026-10-02 §4.6): every service's readiness,
 * version, Kafka consumer lag and DLQ count (from its metrics), the
 * ledger's reconciliation, the custodian and the reference feed;
 * refreshed every 15 seconds.
 */
export default function Health() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["admin", "health", "details"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/health", { params: { query: { details: "true" } } })),
    refetchInterval: 15_000,
  });
  const services = q.data?.services ?? [];
  const down = services.filter((s) => !s.ready).length;
  const versions = [...new Set(services.map((s) => s.version).filter((v): v is string => !!v))];
  const columns = useMemo<ColumnDef<Service, unknown>[]>(
    () => [
      {
        id: "service", header: t("admin.health.service"),
        cell: ({ row: { original: s } }) => (
          <span className="flex items-center gap-2">
            <Pulse ok={s.ready} />
            <span className="font-medium text-fg-1">{s.service}</span>
          </span>
        ),
      },
      {
        id: "ready", header: t("admin.common.status"),
        cell: ({ row: { original: s } }) =>
          s.ready ? <Badge tone="success">{t("admin.health.ready")}</Badge> : <Badge tone="danger">{s.error || t("admin.overview.notReady")}</Badge>,
      },
      { id: "latency", header: t("admin.health.latency"), meta: right, cell: ({ row }) => <span className="tabular-nums">{row.original.latency_ms} ms</span> },
      {
        id: "version", header: t("admin.health.version"),
        cell: ({ row }) => <span className="font-mono text-xs text-fg-2">{row.original.version || "—"}</span>,
      },
      {
        id: "lag", header: () => <span title={t("admin.health.lagHint")}>{t("admin.health.lag")}</span>, meta: right,
        cell: ({ row: { original: s } }) =>
          s.kafka_lag === undefined ? (
            <span className="text-fg-3">—</span>
          ) : (
            <span className={s.kafka_lag > 1000 ? "font-medium text-warn-strong" : undefined}>
              <Num value={String(s.kafka_lag)} decimals={0} />
            </span>
          ),
      },
      {
        id: "dlq", header: () => <span title={t("admin.health.dlqHint")}>{t("admin.health.dlq")}</span>, meta: right,
        cell: ({ row: { original: s } }) =>
          s.dlq === undefined ? <span className="text-fg-3">—</span> : <Badge tone={s.dlq > 0 ? "danger" : "neutral"}>{s.dlq}</Badge>,
      },
    ],
    [t],
  );
  return (
    <Page title={t("admin.nav.health")} help={t("admin.health.help")}>
      <Card
        title={t("admin.health.services")}
        className="stagger"
        extra={
          q.data && (
            <span className="flex flex-wrap items-center gap-2">
              <Badge tone={down ? "danger" : "success"}>{down ? t("admin.health.someDown", { down }) : t("admin.health.allReady", { n: services.length })}</Badge>
              {versions.length === 1 && <Badge tone="neutral">{t("admin.health.sameVersion", { v: versions[0] })}</Badge>}
              {versions.length > 1 && <Badge tone="warn">{t("admin.health.versions", { n: versions.length })}</Badge>}
            </span>
          )
        }
      >
        <DataTable
          columns={columns}
          data={services}
          getRowId={(s) => s.service}
          loading={q.isPending}
          error={q.error}
          onRetry={() => void q.refetch()}
          density="compact"
          aria-label={t("admin.health.services")}
        />
      </Card>
      <div className="grid gap-4 xl:grid-cols-2">
        <Reconciliation />
        <Feed feed={q.data?.feed} loading={q.isPending} />
      </div>
      <CustodySummary className="stagger" style={stagger(3)} />
    </Page>
  );
}

function Reconciliation() {
  const { t } = useTranslation();
  const label = useEnum();
  const q = useQuery({
    queryKey: ["admin", "reconciliation"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/ledger/reconciliation")),
    refetchInterval: 60_000,
  });
  const latest = q.data?.latest ?? [];
  const bad = latest.filter((r) => r.mismatches > 0).length;
  return (
    <Card
      title={t("admin.health.reconciliation")}
      className="stagger"
      style={stagger(1)}
      extra={
        <>
          {q.data && <Badge tone={bad ? "danger" : "success"}>{bad ? t("admin.health.reconBad", { n: bad }) : t("admin.health.reconOk")}</Badge>}
          <Link to="/ledger" className="text-sm text-info-strong hover:underline">
            {t("admin.common.details")}
          </Link>
        </>
      }
    >
      {q.isError ? (
        <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : q.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : (
        <ul className="flex flex-col gap-1.5 text-sm">
          {latest.map((r) => (
            <li key={r.check} className="flex items-center gap-2">
              <Pulse ok={r.mismatches === 0} />
              <span className="truncate" title={r.check}>
                {label("check", r.check)}
              </span>
              <span className="ml-auto text-xs text-fg-3">
                <TimeText value={r.started_at} />
              </span>
              <Badge tone={r.mismatches ? "danger" : "neutral"}>{r.mismatches}</Badge>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function Feed({ feed, loading }: { feed?: AdminSchemas["FeedStatus"]; loading: boolean }) {
  const { t } = useTranslation();
  return (
    <Card title={t("admin.health.feed")} className="stagger" style={stagger(2)} extra={feed && <EnumBadge group="feed" code={feed.state} />}>
      {loading ? (
        <Skeleton className="h-24 w-full" />
      ) : !feed ? (
        <p className="text-sm text-danger-strong">{t("admin.health.feedUnknown")}</p>
      ) : (
        <KeyValue
          density="compact"
          items={[
            { key: "received", label: t("admin.health.received"), value: feed.received_at ? <TimeText value={feed.received_at} /> : "—" },
            {
              key: "followed", label: t("admin.health.followed"),
              value: <span title={feed.followed.join(", ")}>{feed.followed.length}</span>,
            },
            {
              key: "halted", label: t("admin.health.halted"),
              value: feed.halted.length ? (
                <span className="text-danger-strong">{feed.halted.map((h) => h.symbol).join(", ")}</span>
              ) : (
                t("admin.health.noHalted")
              ),
            },
          ]}
        />
      )}
    </Card>
  );
}
