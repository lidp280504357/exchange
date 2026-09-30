import { errorText, formatPercent, marketApi, qk, routes, selectSignedIn, unwrap, useMarket, useSession, useTickers } from "@exchange/core";
import { Button, ErrorState, PriceText, Skeleton, cn } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

/**
 * Home (B0): a welcome card for visitors and the live markets as cards,
 * from the tickers channel. Phase 4 B3 builds the full page (design §7.2).
 */
export default function Home() {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const market = useMarket();
  const tickers = useTickers();
  const pairs = useQuery({ queryKey: qk.pairs, queryFn: () => unwrap(marketApi.GET("/v1/market/pairs")), staleTime: 60_000 });
  const rest = useQuery({ queryKey: qk.tickers, queryFn: () => unwrap(marketApi.GET("/v1/market/tickers")), staleTime: 5_000 });
  useEffect(() => {
    if (rest.data) market.seedTickers(rest.data.tickers);
  }, [market, rest.data]);
  return (
    <div className="flex flex-col gap-4 px-4 py-3">
      {!signedIn && (
        <div className="relative overflow-hidden rounded-3 bg-bg-1 p-5">
          <div className="absolute -right-10 -top-10 size-40 animate-float rounded-full bg-brand-soft blur-2xl" />
          <div className="relative text-lg font-semibold">{t("m.welcome")}</div>
          <div className="relative mt-1 text-sm text-fg-2">{t("m.welcomeHint")}</div>
          <Button asChild className="relative mt-4" block>
            <Link to={routes.register}>{t("m.start")}</Link>
          </Button>
        </div>
      )}
      <div className="flex flex-col divide-y divide-line-1 rounded-3 bg-bg-1">
        {pairs.isPending &&
          [0, 1, 2].map((i) => (
            <div key={i} className="flex min-h-14 items-center justify-between px-4 py-3">
              <Skeleton className="h-4 w-24" />
              <Skeleton className="h-6 w-32" />
            </div>
          ))}
        {pairs.isError && <ErrorState compact message={errorText(pairs.error)} onRetry={() => void pairs.refetch()} />}
        {(pairs.data?.pairs ?? [])
          .filter((p) => p.status !== "DELISTED")
          .map((p) => {
            const tk = tickers.get(p.symbol);
            return (
              <Link key={p.symbol} to={routes.trade(p.symbol)} className="flex min-h-14 items-center justify-between px-4 py-3">
                <div>
                  <div className="font-medium">
                    {p.base_asset}
                    <span className="text-fg-3">/{p.quote_asset}</span>
                  </div>
                  <div className="text-xs text-fg-3">{p.base_name}</div>
                </div>
                <div className="flex items-center gap-3">
                  <PriceText value={tk?.last} decimals={p.price_decimals} change={tk?.change} className="text-base" />
                  <span
                    className={cn(
                      "min-w-18 rounded-1 px-2 py-1 text-center text-sm text-white",
                      tk?.change?.startsWith("-") ? "bg-down" : "bg-up",
                    )}
                  >
                    {formatPercent(tk?.change)}
                  </span>
                </div>
              </Link>
            );
          })}
      </div>
    </div>
  );
}
