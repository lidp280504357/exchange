import { errorText, formatCompact, formatPercent, marketApi, qk, routes, unwrap, useMarket, useSettings, useTickers } from "@exchange/core";
import { Button, ErrorState, PriceText, Skeleton, cn } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * Home (B0): the hero and a live overview of the spot markets from the
 * tickers channel — one subscription for every market, re-rendered at most
 * once per frame. Phase 4 B2 builds the full page (design §6.2).
 */
export default function Home() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const market = useMarket();
  const tickers = useTickers();
  const pairs = useQuery({ queryKey: qk.pairs, queryFn: () => unwrap(marketApi.GET("/v1/market/pairs")), staleTime: 60_000 });
  const rest = useQuery({ queryKey: qk.tickers, queryFn: () => unwrap(marketApi.GET("/v1/market/tickers")), staleTime: 5_000 });
  useEffect(() => {
    if (rest.data) market.seedTickers(rest.data.tickers);
  }, [market, rest.data]);
  const list = (pairs.data?.pairs ?? []).filter((p) => p.status === "TRADING" || p.status === "PREPARE");
  return (
    <div>
      <section className="relative overflow-hidden border-b border-line-1">
        <div className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,var(--line-1)_1px,transparent_1px),linear-gradient(to_bottom,var(--line-1)_1px,transparent_1px)] bg-[size:48px_48px] opacity-40" />
        <div className="pointer-events-none absolute -top-40 right-0 size-[640px] animate-float rounded-full bg-brand-soft blur-3xl" />
        <div className="relative mx-auto flex max-w-[1440px] flex-col items-start gap-6 px-6 py-24">
          <h1 className="max-w-2xl text-2xl font-semibold leading-tight text-fg-1 lg:text-[44px]">{t("pc.heroTitle")}</h1>
          <p className="max-w-xl text-md text-fg-2">{t("pc.heroSubtitle")}</p>
          <div className="flex gap-3">
            <Button asChild size="lg">
              <Link to={routes.register}>{t("pc.start")}</Link>
            </Button>
            <Button asChild size="lg" variant="secondary">
              <Link to={routes.trade("BTC-USDT")}>{t("pc.trade")}</Link>
            </Button>
          </div>
        </div>
      </section>
      <section className="mx-auto max-w-[1440px] px-6 py-12">
        <h2 className="mb-4 text-lg font-semibold">{t("pc.overview")}</h2>
        <div className="overflow-hidden rounded-3 border border-line-1 bg-bg-1">
          <div className="grid grid-cols-[2fr_1.5fr_1fr_1.5fr_1fr] gap-4 border-b border-line-1 px-5 py-3 text-sm text-fg-3">
            <span>{t("market.pair")}</span>
            <span className="text-right">{t("market.last")}</span>
            <span className="text-right">{t("market.change")}</span>
            <span className="text-right">{t("market.turnover")}</span>
            <span className="text-right">{t("common.action")}</span>
          </div>
          {pairs.isError && <ErrorState compact message={errorText(pairs.error)} onRetry={() => void pairs.refetch()} />}
          {pairs.isPending
            ? [0, 1, 2].map((i) => (
                <div key={i} className="grid grid-cols-[2fr_1.5fr_1fr_1.5fr_1fr] gap-4 px-5 py-4">
                  <Skeleton className="h-4 w-24" />
                  <Skeleton className="ml-auto h-4 w-20" />
                  <Skeleton className="ml-auto h-4 w-14" />
                  <Skeleton className="ml-auto h-4 w-20" />
                  <Skeleton className="ml-auto h-4 w-12" />
                </div>
              ))
            : list.map((p) => {
                const tk = tickers.get(p.symbol);
                return (
                  <div key={p.symbol} className="grid grid-cols-[2fr_1.5fr_1fr_1.5fr_1fr] items-center gap-4 border-b border-line-1 px-5 py-4 last:border-0 hover:bg-bg-2">
                    <div>
                      <span className="font-medium text-fg-1">{p.base_asset}</span>
                      <span className="text-fg-3">/{p.quote_asset}</span>
                      <div className="text-xs text-fg-3">{p.base_name}</div>
                    </div>
                    <div className="text-right">
                      <PriceText value={tk?.last} decimals={p.price_decimals} change={tk?.change} />
                    </div>
                    <div className={cn("text-right", tk?.change?.startsWith("-") ? "text-down" : "text-up")}>{formatPercent(tk?.change)}</div>
                    <div className="text-right text-fg-2">{formatCompact(tk?.quote_volume, locale)}</div>
                    <div className="text-right">
                      <Button asChild size="sm" variant="secondary">
                        <Link to={routes.trade(p.symbol)}>{t("nav.trade")}</Link>
                      </Button>
                    </div>
                  </div>
                );
              })}
        </div>
      </section>
    </div>
  );
}
