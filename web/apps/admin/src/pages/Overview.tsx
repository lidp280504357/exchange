import { errorText, formatAmount, formatTime, useSettings } from "@exchange/core";
import { adminApi, adminData } from "@exchange/core/api/admin";
import { ErrorState, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

/**
 * Overview (B0): the dashboard figures of GET /admin/v1/dashboard. Phase 4
 * B5 adds the charts and the health of every service (design §10.3).
 */
export default function Overview() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const q = useQuery({
    queryKey: ["admin", "dashboard"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/dashboard", { params: { query: { days: 7 } } })),
    refetchInterval: 15_000,
  });
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const d = q.data;
  const stats: [string, string | number | undefined][] = [
    ["users", d?.users.total],
    ["new24h", d?.users.new_24h],
    ["trades24h", d?.trading.trades_24h],
    ["traders24h", d?.trading.active_traders_24h],
    ["turnover24h", d?.trading.turnover_24h.map((x) => `${formatAmount(x.amount, 2)} ${x.quote_asset}`).join(" · ") || "0"],
    ["pendingWithdrawals", d?.wallet.pending_withdrawals],
    ["pendingDeposits", d?.wallet.pending_deposits],
    ["riskEvents", d?.risk.events_24h],
    ["feed", d?.feed ? t(`admin.feed.${d.feed.state}`) : "—"],
    ["halted", d?.feed ? d.feed.halted.map((h) => h.symbol).join(", ") || "0" : "—"],
  ];
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-lg font-semibold">{t("admin.nav.overview")}</h1>
      {d && d.partial.length > 0 && (
        <div className="rounded-2 border border-warn bg-bg-1 px-4 py-2 text-sm text-fg-2">{t("admin.partial", { parts: d.partial.join(", ") })}</div>
      )}
      <div className="grid grid-cols-2 gap-4 xl:grid-cols-5">
        {stats.map(([key, value]) => (
          <div key={key} className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <div className="text-sm text-fg-3">{t(`admin.stats.${key}`)}</div>
            <div className="mt-2 text-xl font-semibold">{q.isPending ? <Skeleton className="h-7 w-20" /> : String(value ?? "—")}</div>
          </div>
        ))}
      </div>
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <div className="grid grid-cols-4 border-b border-line-1 px-4 py-2 text-sm text-fg-3">
          <span>Day</span>
          <span className="text-right">{t("admin.stats.new24h")}</span>
          <span className="text-right">{t("admin.stats.trades24h")}</span>
          <span className="text-right">USDT</span>
        </div>
        {(d?.series ?? []).map((s) => (
          <div key={s.day} className="grid grid-cols-4 border-b border-line-1 px-4 py-2 text-sm last:border-0">
            <span>{formatTime(`${s.day}T00:00:00Z`, "date", locale, "UTC")}</span>
            <span className="text-right">{s.new_users}</span>
            <span className="text-right">{s.trades}</span>
            <span className="text-right">{formatAmount(s.turnover_usdt, 2)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
