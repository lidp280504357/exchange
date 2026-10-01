import { formatPercent, routes } from "@exchange/core";
import { marqueeRows, useMarketRows, useMarketTickers } from "@exchange/core/markets/index";
import { PriceText, Skeleton, cn, listItem, toneOf } from "@exchange/ui";
import { motion } from "motion/react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DaySpark, MarketName, SectionHead, tradePath } from "../../home/parts";

const COINS = 3;
const TONE = { up: "text-up", down: "text-down", neutral: "text-fg-2" } as const;

/**
 * MarketGlance (design §7.3, visitors): the first three coins of the home
 * page's line-up with their price, 24-hour change and day line, live from
 * the tickers channel, as a way in.
 */
export function MarketGlance({ index }: { index: number }) {
  const { t } = useTranslation();
  const { rows, loading } = useMarketRows();
  const tickers = useMarketTickers();
  const list = useMemo(() => marqueeRows(rows, COINS), [rows]);
  if (!loading && list.length === 0) return null;
  return (
    <motion.section variants={listItem} initial="initial" animate="animate" custom={index} data-testid="me-glance" aria-label={t("mAccount.me.glance.title")}>
      <SectionHead title={t("mAccount.me.glance.title")} to={routes.markets} more={t("mAccount.me.glance.all")} />
      <ul className="divide-y divide-line-1 overflow-hidden rounded-3 border border-line-1 bg-bg-1">
        {loading
          ? [0, 1, 2].map((i) => (
              <li key={i} className="flex h-16 items-center gap-3 px-4">
                <Skeleton round className="size-7" />
                <Skeleton className="h-4 w-20" />
                <Skeleton className="ml-auto h-4 w-24" />
              </li>
            ))
          : list.map((r) => {
              const tk = tickers.get(r.symbol);
              return (
                <li key={r.symbol}>
                  <Link to={tradePath(r)} className="flex h-16 items-center gap-3 px-4 transition-colors active:bg-bg-2">
                    <span className="min-w-0 flex-1">
                      <MarketName row={r} size={28} />
                    </span>
                    <DaySpark symbol={r.symbol} width={64} height={28} className="shrink-0" />
                    <span className="flex w-24 shrink-0 flex-col items-end">
                      <PriceText value={tk?.last} decimals={r.priceDecimals} className="text-sm font-medium" />
                      <span className={cn("text-xs font-medium tabular-nums", TONE[toneOf(tk?.change)])}>{formatPercent(tk?.change)}</span>
                    </span>
                  </Link>
                </li>
              );
            })}
      </ul>
    </motion.section>
  );
}
