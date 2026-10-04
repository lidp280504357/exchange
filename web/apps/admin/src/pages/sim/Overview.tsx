import { adminApi, adminData, type Admin } from "@exchange/core/api/admin";
import { Badge, ErrorState, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { Num, TimeText, useTimeText } from "../../kit/format";
import { Card, Page } from "../../kit/Page";
import { PriceLines, type PricePoint } from "../../kit/PriceLines";
import { pct, price, simKey, useEventText, useSim } from "./common";
import { TargetNowOf } from "./target";

const MAX_POINTS = 360;

/**
 * The simulated market's overview (ASTRA design §6.1): the target and the
 * last price over the day, the price band and the cluster's state, the
 * bots' inventory, and the events under way.
 */
export default function SimOverview(_: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useSim(3_000);
  const time = useTimeText();
  const eventText = useEventText();
  const history = useQuery({
    queryKey: [...simKey, "history"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim/history", { params: { query: { minutes: 1440 } } })).items,
    refetchInterval: 60_000,
  });
  const points = useMemo<PricePoint[]>(() => {
    const items = history.data ?? [];
    const step = Math.max(1, Math.ceil(items.length / MAX_POINTS));
    const out: PricePoint[] = [];
    items.forEach((s, i) => {
      if (i % step !== 0 && i !== items.length - 1) return;
      out.push({
        at: Date.parse(s.at), label: time(s.at, "time"),
        values: { target: Number(s.target_price), last: s.last_price === null ? null : Number(s.last_price) },
      });
    });
    const st = q.data;
    if (st?.at && st.target_price && (!out.length || Date.parse(st.at) > out[out.length - 1]!.at)) {
      out.push({ at: Date.parse(st.at), label: time(st.at, "time"), values: { target: Number(st.target_price), last: st.last_price === null ? null : Number(st.last_price) } });
    }
    return out;
  }, [history.data, q.data, time]);
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  const st = q.data;
  const running = (st?.events ?? []).filter((e) => e.status === "RUNNING");
  const target = running.find((e) => e.type === "TARGET");
  const bots = st?.bots ?? [];
  const sum = (f: (b: (typeof bots)[number]) => string) => bots.reduce((a, b) => a + (Number(f(b)) || 0), 0);
  const gap = st?.target_price && st.last_price ? Number(st.last_price) / Number(st.target_price) - 1 : null;
  return (
    <Page title={t("admin.nav.simOverview")} help={t("admin.sim.overviewHelp")}>
      {running.length > 0 && (
        <Link to="/sim/events" className="flex items-center gap-2 rounded-2 border border-brand bg-brand-soft px-4 py-2 text-sm text-fg-1" data-testid="sim-running">
          <Badge tone="brand" dot>
            {t("admin.sim.eventRunning")}
          </Badge>
          {running.map((e) => eventText(e)).join(" · ")}
          {target && <TargetNowOf id={target.id} />}
        </Link>
      )}
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Card title={t("admin.sim.target")}>
          {st ? (
            <div className="flex flex-col gap-1">
              <span className="font-mono text-2xl tabular-nums" data-testid="sim-target">{price(st.target_price)}</span>
              <span className="text-xs text-fg-3">
                {t("admin.sim.last")} <span className="font-mono text-fg-1">{price(st.last_price)}</span>
                {gap !== null && <span className={gap >= 0 ? " text-up-strong" : " text-down-strong"}> ({pct(gap)})</span>}
              </span>
              <span className="text-xs text-fg-3">{st.last_trade_at ? <>{t("admin.sim.lastTrade")} <TimeText value={st.last_trade_at} style="timeSeconds" /></> : t("admin.sim.noTrade")}</span>
            </div>
          ) : <Skeleton className="h-16 w-full" />}
        </Card>
        <Card title={t("admin.sim.band")}>
          {st ? (
            <div className="flex flex-col gap-1 text-sm">
              <span>{t("admin.sim.anchor")} <span className="font-mono">{price(st.anchor_price)}</span> · ±{pct(Number(st.price_band), 0).replace("+", "")}</span>
              <span>{t("admin.sim.quoteCenter")} <span className="font-mono">{price(st.quote_center)}</span></span>
              <span className="flex items-center gap-1.5">
                {st.walking ? <Badge tone="warn">{t("admin.sim.walking")}</Badge> : <Badge tone="success">{t("admin.sim.inBand")}</Badge>}
                {st.band_distance !== null && <span className="text-xs text-fg-3">{t("admin.sim.bandDistance", { n: st.band_distance })}</span>}
              </span>
            </div>
          ) : <Skeleton className="h-16 w-full" />}
        </Card>
        <Card title={t("admin.sim.cluster")}>
          {st ? (
            <div className="flex flex-wrap gap-1.5">
              <Badge tone={st.enabled ? "success" : "neutral"}>{t(st.enabled ? "admin.sim.enabled" : "admin.sim.disabled")}</Badge>
              <Badge tone={st.running ? "success" : "warn"} dot={st.running}>{t(st.running ? "admin.sim.running" : "admin.sim.stopped")}</Badge>
              <Badge tone={st.references_fresh ? "success" : "danger"}>{t(st.references_fresh ? "admin.sim.refsFresh" : "admin.sim.refsStale")}</Badge>
              {st.perp && <Badge tone={st.perp_running ? "success" : "neutral"}>{st.perp} · {t(st.perp_running ? "admin.sim.running" : "admin.sim.stopped")}</Badge>}
              <Badge tone={st.watchdog.fired > 0 ? "warn" : "neutral"} title={st.watchdog.last_at ?? ""}>{t("admin.sim.watchdog", { n: st.watchdog.fired })}</Badge>
            </div>
          ) : <Skeleton className="h-16 w-full" />}
        </Card>
        <Card title={t("admin.sim.inventory")}>
          {st ? (
            <div className="flex flex-col gap-1 text-sm">
              <span><Num value={String(Math.round(sum((b) => b.coin)))} className="font-mono" /> {st.symbol.split("-")[0]}</span>
              <span><Num value={String(Math.round(sum((b) => b.usdt)))} className="font-mono" /> USDT</span>
              {st.perp && <span className="text-xs text-fg-3">{t("admin.sim.perpPosition")} <Num value={sum((b) => b.perp_position).toFixed(2)} className="font-mono" /></span>}
              <Link to="/sim/bots" className="text-xs text-info hover:underline">
                {t("admin.sim.botsLink", { n: bots.length, errors: bots.filter((b) => b.error).length })}
              </Link>
            </div>
          ) : <Skeleton className="h-16 w-full" />}
        </Card>
      </div>
      <Card title={t("admin.sim.chart")}>
        {history.isPending && !points.length ? (
          <Skeleton className="h-[260px] w-full" />
        ) : (
          <PriceLines
            data={points}
            lines={[
              { key: "target", label: t("admin.sim.target"), className: "stroke-chart-3", dotClassName: "bg-chart-3" },
              { key: "last", label: t("admin.sim.last"), className: "stroke-chart-1", dotClassName: "bg-chart-1" },
            ]}
            format={(v) => v.toFixed(4)}
            aria-label={t("admin.sim.chart")}
          />
        )}
      </Card>
    </Page>
  );
}
