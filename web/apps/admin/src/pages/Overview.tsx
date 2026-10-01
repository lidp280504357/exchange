import { errorText, formatAmount } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, cn, CountUp, ErrorState, Segmented, Skeleton, TrendChart } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpFromLine, ChevronRight, Stamp } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { EnumBadge } from "../kit/enums";
import { Num } from "../kit/format";
import { Pulse, Reveal, stagger } from "../kit/motion";
import { Card, Page } from "../kit/Page";
import { CountBadge } from "../layout/CountBadge";
import { useTodo } from "../live";

/**
 * Overview (design 2026-10-02 §3, §6): what waits for the administrator,
 * the figures of the last 24 hours rolling up to their values, trading and
 * new accounts over 7 or 30 days drawn in, every service's readiness (a
 * failing one breathes), HOUSE's book and the custodian; refreshed every
 * 15 seconds.
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
        <CountUp value={turnovers[0]!.amount} decimals={2} /> <span className="text-sm font-normal text-fg-3">{turnovers[0]!.quote_asset}</span>
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
  const count = (n: number | undefined) => (n === undefined ? undefined : <CountUp value={String(n)} decimals={0} />);
  const stats: { key: string; value: ReactNode; to?: string }[] = [
    { key: "users", value: count(d?.users.total), to: "/users" },
    { key: "new24h", value: count(d?.users.new_24h) },
    { key: "trades24h", value: count(d?.trading.trades_24h), to: "/orders?tab=trades" },
    { key: "traders24h", value: count(d?.trading.active_traders_24h) },
    { key: "turnover24h", value: turnover },
    { key: "pendingWithdrawals", value: count(d?.wallet.pending_withdrawals), to: "/withdrawals" },
    { key: "pendingDeposits", value: count(d?.wallet.pending_deposits), to: "/deposits?status=CONFIRMING" },
    { key: "riskEvents", value: count(d?.risk.events_24h) },
    { key: "feed", value: d?.feed ? <EnumBadge group="feed" code={d.feed.state} /> : "—" },
    { key: "halted", value: d?.feed ? d.feed.halted.map((h) => h.symbol).join(", ") || "0" : "—" },
  ];
  return (
    <Page title={t("admin.overview.hello", { name: admin.name || admin.email })} help={t("admin.overview.help")}>
      <Todo admin={admin} />
      {d && d.partial.length > 0 && <div className="rounded-2 border border-warn px-4 py-2 text-sm text-fg-2">{t("admin.partial", { parts: d.partial.join(", ") })}</div>}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        {stats.map((s, i) => {
          const body = (
            <>
              <div className="text-sm text-fg-3">{t(`admin.stats.${s.key}`)}</div>
              {q.isPending ? (
                <Skeleton className="mt-2 h-6 w-24" />
              ) : (
                <div className="mt-1.5 truncate font-mono text-xl font-semibold tabular-nums text-fg-1">{s.value ?? "—"}</div>
              )}
            </>
          );
          return (
            <div
              key={s.key}
              style={stagger(i)}
              className={cn("card stagger p-4", s.to && "transition-[transform,border-color] duration-[var(--t-fast)] hover:-translate-y-0.5 hover:border-brand")}
            >
              {s.to ? (
                <Link to={s.to} className="block">
                  {body}
                </Link>
              ) : (
                body
              )}
            </div>
          );
        })}
      </div>
      <Card
        title={t("admin.overview.trend")}
        className="stagger"
        style={stagger(4)}
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
          <Reveal key={days}>
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
          </Reveal>
        ) : (
          <Skeleton className="h-[220px] w-full" />
        )}
      </Card>
      <div className="grid gap-4 xl:grid-cols-2">
        <Health />
        <HouseSummary />
      </div>
      <CustodySummary />
    </Page>
  );
}

/** Todo leads to what waits: withdrawals to review and fund operations to decide. */
function Todo({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const todo = useTodo();
  const items = [
    { key: "withdrawals", to: "/withdrawals", icon: ArrowUpFromLine, n: todo?.withdrawals, show: can(admin, "withdrawals.read") },
    {
      key: "approvals", to: "/approvals", icon: Stamp, n: todo?.approvals,
      show: can(admin, "ledger.adjust.request") || can(admin, "ledger.adjust.approve"),
    },
  ].filter((x) => x.show);
  if (items.length === 0) return null;
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {items.map((x, i) => (
        <Link
          key={x.key}
          to={x.to}
          style={stagger(i)}
          className="card stagger group flex items-center gap-3 p-4 transition-[transform,border-color] duration-[var(--t-fast)] hover:-translate-y-0.5 hover:border-brand"
        >
          <span className={cn("grid size-10 place-items-center rounded-2", x.n ? "bg-warn/15 text-warn" : "bg-bg-2 text-fg-3")}>
            <x.icon size={18} />
          </span>
          <span className="min-w-0 flex-1">
            <span className="block text-sm text-fg-3">{t(`admin.shell.todo_${x.key}`)}</span>
            <span className="block text-sm text-fg-1">{x.n ? t("admin.overview.waiting", { n: x.n }) : t("admin.overview.clear")}</span>
          </span>
          {x.n ? <CountBadge value={x.n} /> : null}
          <ChevronRight size={16} className="text-fg-3 transition-transform group-hover:translate-x-0.5" />
        </Link>
      ))}
    </div>
  );
}

/** CustodySummary is the custodian at a glance: reachable, short of nothing, no callback or withdrawal stuck. */
function CustodySummary() {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "custody"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/custody")), refetchInterval: 30_000 });
  const o = q.data;
  const short = (o?.checks ?? []).filter((c) => Number(c.shortfall) > 0);
  return (
    <Card
      title={t("admin.overview.custody")}
      extra={
        <Link className="text-sm text-info hover:underline" to="/custody">
          {t("admin.common.details")}
        </Link>
      }
    >
      {q.isError ? (
        <p className="text-sm text-danger">{errorText(q.error)}</p>
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
                  <Pulse ok={s.ready} />
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
          <div>
            <div className="text-sm text-fg-3">{t("admin.overview.houseInventory")}</div>
            {h ? <div className="mt-1 text-xl font-semibold"><Num value={h.totals.inventory_usdt} decimals={2} unit="USDT" /></div> : <Skeleton className="mt-2 h-6 w-32" />}
          </div>
          <div>
            <div className="text-sm text-fg-3">{t("admin.overview.housePnl")}</div>
            {h ? <div className="mt-1 text-xl font-semibold"><Num value={h.totals.pnl_usdt} decimals={2} unit="USDT" signed /></div> : <Skeleton className="mt-2 h-6 w-32" />}
          </div>
        </div>
      )}
    </Card>
  );
}
