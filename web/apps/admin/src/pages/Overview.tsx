import { errorText, formatAmount } from "@exchange/core";
import { adminApi, adminData, type Admin } from "@exchange/core/api/admin";
import { Badge, cn, ErrorState, Segmented, Skeleton, Stat, TrendChart } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { EnumBadge } from "../kit/enums";
import { Num } from "../kit/format";
import { Card, Page } from "../kit/Page";

/**
 * Overview (design §10.3): the figures of the last 24 hours, trading and
 * new accounts over 7 or 30 days, every service's readiness and HOUSE's
 * book; refreshed every 15 seconds.
 */
export default function Overview({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [days, setDays] = useState("7");
  const q = useQuery({
    queryKey: ["admin", "dashboard", days],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/dashboard", { params: { query: { days: Number(days) } } })),
    refetchInterval: 15_000,
  });
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const d = q.data;
  // USDT first, the other quote assets on a line of their own.
  const turnovers = [...(d?.trading.turnover_24h ?? [])].sort((a, b) => (a.quote_asset === "USDT" ? -1 : b.quote_asset === "USDT" ? 1 : 0));
  const turnover = turnovers.length ? (
    <span className="flex flex-col">
      <span>
        {formatAmount(turnovers[0]!.amount, 2)} <span className="text-sm font-normal text-fg-3">{turnovers[0]!.quote_asset}</span>
      </span>
      {turnovers.slice(1).map((x) => (
        <span key={x.quote_asset} className="text-xs font-normal text-fg-3">
          {formatAmount(x.amount, 6)} {x.quote_asset}
        </span>
      ))}
    </span>
  ) : (
    "0"
  );
  const stats: { key: string; value: React.ReactNode; to?: string }[] = [
    { key: "users", value: d?.users.total, to: "/users" },
    { key: "new24h", value: d?.users.new_24h },
    { key: "trades24h", value: d?.trading.trades_24h, to: "/orders?tab=trades" },
    { key: "traders24h", value: d?.trading.active_traders_24h },
    { key: "turnover24h", value: turnover },
    { key: "pendingWithdrawals", value: d?.wallet.pending_withdrawals, to: "/withdrawals" },
    { key: "pendingDeposits", value: d?.wallet.pending_deposits, to: "/deposits?status=CONFIRMING" },
    { key: "riskEvents", value: d?.risk.events_24h },
    { key: "feed", value: d?.feed ? <EnumBadge group="feed" code={d.feed.state} /> : "—" },
    { key: "halted", value: d?.feed ? d.feed.halted.map((h) => h.symbol).join(", ") || "0" : "—" },
  ];
  return (
    <Page title={t("admin.nav.overview")} help={admin.email}>
      {d && d.partial.length > 0 && <div className="rounded-2 border border-warn px-4 py-2 text-sm text-fg-2">{t("admin.partial", { parts: d.partial.join(", ") })}</div>}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        {stats.map((s) => {
          const body = <Stat label={t(`admin.stats.${s.key}`)} value={q.isPending ? undefined : (s.value ?? "—")} loading={q.isPending} />;
          return (
            <div key={s.key} className={cn("rounded-3 border border-line-1 bg-bg-1 p-4", s.to && "transition-colors hover:border-brand")}>
              {s.to ? <Link to={s.to}>{body}</Link> : body}
            </div>
          );
        })}
      </div>
      <Card
        title={t("admin.overview.trend")}
        extra={
          <Segmented
            size="sm"
            value={days}
            onValueChange={setDays}
            items={[
              { value: "7", label: t("admin.overview.days7") },
              { value: "30", label: t("admin.overview.days30") },
            ]}
          />
        }
      >
        {d ? (
          <TrendChart
            aria-label={t("admin.overview.trend")}
            data={d.series.map((s) => ({
              x: s.day,
              label: s.day.slice(5),
              values: { users: s.new_users, trades: s.trades, turnover: Number(s.turnover_usdt) },
            }))}
            series={[
              { key: "users", label: t("admin.overview.newUsers"), kind: "bar", color: "chart-1" },
              { key: "trades", label: t("admin.overview.trades"), kind: "bar", color: "chart-2" },
              { key: "turnover", label: t("admin.overview.turnover"), kind: "line", color: "chart-3" },
            ]}
          />
        ) : (
          <Skeleton className="h-[220px] w-full" />
        )}
      </Card>
      <div className="grid gap-4 xl:grid-cols-2">
        <Health />
        <HouseSummary />
      </div>
      <Card title={t("admin.overview.custody")}>
        <p className="text-sm text-fg-3">{t("admin.overview.custodySoon")}</p>
      </Card>
    </Page>
  );
}

function Health() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["admin", "health"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/health")).services,
    refetchInterval: 15_000,
  });
  const list = q.data ?? [];
  const down = list.filter((s) => !s.ready).length;
  return (
    <Card
      title={t("admin.overview.health")}
      extra={
        q.data && (
          <Badge tone={down ? "danger" : "success"}>{down ? t("admin.overview.someDown", { down }) : t("admin.overview.allReady", { n: list.length })}</Badge>
        )
      }
    >
      {q.isError ? (
        <p className="text-sm text-danger">{errorText(q.error)}</p>
      ) : (
        <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 text-sm sm:grid-cols-3">
          {q.isPending
            ? Array.from({ length: 9 }, (_, i) => <Skeleton key={i} className="h-5 w-32" />)
            : list.map((s) => (
                <div key={s.service} className="flex items-center gap-2" title={s.error || t("admin.overview.latency", { ms: s.latency_ms })}>
                  <span className={cn("size-2 shrink-0 rounded-full", s.ready ? "bg-success" : "bg-danger")} />
                  <span className="truncate">{s.service}</span>
                  <span className="ml-auto text-xs tabular-nums text-fg-3">{s.ready ? `${s.latency_ms}ms` : s.error}</span>
                </div>
              ))}
        </div>
      )}
    </Card>
  );
}

function HouseSummary() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["admin", "house"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/house")),
    refetchInterval: 30_000,
  });
  const h = q.data;
  return (
    <Card title={t("admin.overview.house")} extra={<Link to="/house" className="text-sm text-info hover:underline">{t("admin.common.details")}</Link>}>
      {q.isError ? (
        <p className="text-sm text-danger">{errorText(q.error)}</p>
      ) : (
        <div className="grid grid-cols-2 gap-4">
          <Stat label={t("admin.overview.houseInventory")} value={h ? <Num value={h.totals.inventory_usdt} decimals={2} unit="USDT" /> : undefined} loading={!h} />
          <Stat label={t("admin.overview.housePnl")} value={h ? <Num value={h.totals.pnl_usdt} decimals={2} unit="USDT" signed /> : undefined} loading={!h} />
        </div>
      )}
    </Card>
  );
}
