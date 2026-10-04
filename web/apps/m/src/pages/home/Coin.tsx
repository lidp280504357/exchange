import {
  coinProfile,
  errorText,
  formatCompact,
  formatPercent,
  formatPrice,
  formatTime,
  routes,
  useAssets,
  useCandles,
  useContracts,
  usePairs,
  useSettings,
  type CandleInterval,
  type CoinProfile,
  type Contract,
  type TickerData,
} from "@exchange/core";
import { tagLabel, useMarketRows, useMarketTickers, type MarketRow } from "@exchange/core/markets/index";
import {
  Badge,
  Button,
  CandleChart,
  ChangeBadge,
  CoinIcon,
  EmptyState,
  ErrorState,
  KeyValue,
  PriceText,
  Skeleton,
  SkeletonLines,
  Tag,
  cn,
  intervalParts,
  listItem,
  type KeyValueItem,
} from "@exchange/ui";
import { BookOpen, ChevronRight, ExternalLink, Globe, Layers, Search } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useParams } from "react-router";
import { FavoriteStar } from "../../components/FavoriteStar";
import { PillBar } from "../../components/PillBar";
import { usePageHeader } from "../../layout/header";
import { coinFromParam, coinMarkets } from "./logic";
import { MarketName, tradePath, useFavoriteToggle } from "./parts";

// /coin/:symbol on the phone (design §7.2): the coin's header with its
// live price and a favourite star, a 260 px chart (1D by default, the
// chart library loads with it) with interval pills, its markets, the
// 24-hour figures with market cap and rank from the static profile, the
// introduction and links, and a trade bar fixed above the safe area.
// "/coin/btc" and "/coin/BTC-USDT" lead to "/coin/BTC".

const INTERVALS: CandleInterval[] = ["15m", "1h", "4h", "1d", "1w", "1M"];

/** The static profile may carry market figures (dated by asOf); none do yet. */
type ProfileStats = CoinProfile & { marketCap?: string; rank?: number };

export default function Coin() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const params = useParams();
  const raw = params.symbol ?? "";
  const coin = coinFromParam(raw);

  const { rows, loading, error, refetch } = useMarketRows();
  const tickers = useMarketTickers();
  const assets = useAssets();
  const contracts = useContracts();
  const pairs = usePairs();
  const fav = useFavoriteToggle();

  const { markets, primary, tradeTarget, perp } = useMemo(() => coinMarkets(rows, coin), [rows, coin]);
  const pair = pairs.data?.pairs.find((p) => p.symbol === primary?.symbol);
  const asset = assets.data?.assets.find((a) => a.asset_code === coin);
  const profile = coinProfile(coin) as ProfileStats | undefined;
  const name = profile?.name[locale] ?? primary?.name ?? asset?.name ?? coin;
  const [interval, setChartInterval] = useState<CandleInterval>("1d");
  const candles = useCandles(primary?.symbol ?? "", interval);

  usePageHeader({ title: coin || t("nav.markets"), back: routes.markets }, [coin, t]);

  if (raw !== coin && coin) return <Navigate to={routes.coin(coin)} replace />;
  if (loading || (markets.length === 0 && assets.isPending)) return <CoinSkeleton />;
  if (error && rows.length === 0) {
    return (
      <div className="px-4 py-8">
        <ErrorState message={errorText(error)} onRetry={refetch} />
      </div>
    );
  }
  if (markets.length === 0 && !asset) {
    return (
      <div className="px-4 py-8">
        <EmptyState
          title={t("mMarkets.coin.notFound")}
          description={t("mMarkets.coin.notFoundHint", { symbol: coin || raw })}
          action={
            <Button asChild size="lg" variant="secondary" icon={<Search size={16} />}>
              <Link to={routes.markets}>{t("mMarkets.coin.back")}</Link>
            </Button>
          }
        />
      </div>
    );
  }

  const tk = primary ? tickers.get(primary.symbol) : undefined;
  const rank = profile?.rank ?? primary?.rank ?? asset?.rank ?? null;
  const categories = primary?.categories ?? asset?.categories ?? [];
  const intervalLabel = (i: string) => {
    const p = intervalParts(i);
    return p ? t(`ui.chart.intervals.${p.unit}`, { n: p.n }) : i;
  };

  return (
    <div className={cn("flex flex-col gap-4 px-4 pt-3", tradeTarget ? "pb-24" : "pb-8")}>
      <motion.section
        variants={listItem}
        initial="initial"
        animate="animate"
        custom={0}
        className="relative overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-4"
      >
        <div aria-hidden className="pointer-events-none absolute -right-16 -top-20 size-48 rounded-full bg-brand-soft blur-3xl" />
        <div className="relative flex items-start gap-3">
          <CoinIcon symbol={coin} size={44} label={name} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <h1 className="min-w-0 truncate text-lg font-semibold text-fg-1">{name}</h1>
              <span className="text-sm text-fg-3">{coin}</span>
              {rank !== null && <Badge tone="brand">{t("mMarkets.coin.rank", { rank })}</Badge>}
            </div>
            {categories.length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {categories.slice(0, 3).map((c) => (
                  <Tag key={c}>{t(`mMarkets.tags.${c}`, { defaultValue: tagLabel(c) })}</Tag>
                ))}
              </div>
            )}
          </div>
          {primary && (
            <FavoriteStar
              boxed
              active={fav.has(primary.symbol)}
              onToggle={() => fav.toggle(primary.symbol)}
              label={fav.has(primary.symbol) ? t("mMarkets.favoriteRemove") : t("mMarkets.favoriteAdd")}
              size={20}
            />
          )}
        </div>
        {primary ? (
          <div className="relative mt-4 flex flex-wrap items-center gap-x-2 gap-y-1">
            <PriceText value={tk?.last} decimals={primary.priceDecimals} flash={false} arrow className="text-2xl font-semibold" />
            <span className="text-sm text-fg-3">{primary.quote}</span>
            <ChangeBadge value={tk?.change} size="md" />
          </div>
        ) : (
          <p className="relative mt-3 text-sm text-fg-3">{t("mMarkets.coin.noMarketHint", { symbol: coin })}</p>
        )}
      </motion.section>

      <section aria-label={t("mMarkets.coin.chart")} className="overflow-hidden rounded-3 border border-line-1 bg-bg-1">
        {primary ? (
          <>
            <h2 className="sr-only">{t("mMarkets.coin.chart")}</h2>
            <PillBar
              aria-label={t("ui.chart.interval")}
              items={INTERVALS.map((i) => ({ value: i, label: intervalLabel(i) }))}
              value={interval}
              onValueChange={(v) => setChartInterval(v as CandleInterval)}
              className="border-b border-line-1 px-2"
            />
            {candles.error && candles.candles.length === 0 ? (
              <ErrorState
                compact
                className="h-[260px]"
                message={`${t("mMarkets.coin.chartError")} · ${errorText(candles.error)}`}
                onRetry={() => void candles.refetch()}
              />
            ) : (
              <CandleChart
                symbol={primary.symbol}
                candles={candles.candles}
                interval={interval}
                priceDecimals={primary.priceDecimals}
                onLoadMore={candles.loadMore}
                hasMore={candles.hasMore}
                loading={candles.loading}
                height={260}
                toolbar={false}
              />
            )}
          </>
        ) : (
          <EmptyState
            compact
            title={t("mMarkets.coin.noMarket")}
            action={
              <Button asChild size="lg" variant="secondary">
                <Link to={routes.markets}>{t("mMarkets.coin.back")}</Link>
              </Button>
            }
          />
        )}
      </section>

      {markets.length > 1 && <Markets markets={markets} tickers={tickers} contracts={contracts.data?.contracts} />}

      <KeyData row={primary} ticker={tk} profile={profile} rank={rank} feeMaker={pair?.maker_fee_rate} feeTaker={pair?.taker_fee_rate} />
      <About profile={profile} />

      {tradeTarget && (
        <div className="fixed inset-x-0 bottom-0 z-[var(--z-sticky)] border-t border-line-1 bg-bg-1/95 px-4 pb-[max(8px,env(safe-area-inset-bottom))] pt-2 backdrop-blur">
          <div className="mx-auto flex max-w-[480px] gap-3">
            <Button asChild size="lg" className="flex-1">
              <Link to={tradePath(tradeTarget)}>{t("mMarkets.coin.goTrade")}</Link>
            </Button>
            {perp && perp.symbol !== tradeTarget.symbol && (
              <Button asChild size="lg" variant="secondary" icon={<Layers size={18} />}>
                <Link to={routes.futures(perp.symbol)}>{t("mMarkets.coin.futures")}</Link>
              </Button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function Card({ title, children, className }: { title: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cn("rounded-3 border border-line-1 bg-bg-1", className)}>
      <h2 className="px-4 pb-1 pt-3 text-base font-medium text-fg-1">{title}</h2>
      {children}
    </section>
  );
}

/** Markets: each market of the coin with its live price; a tap opens its terminal. */
function Markets({ markets, tickers, contracts }: { markets: MarketRow[]; tickers: ReadonlyMap<string, TickerData>; contracts: Contract[] | undefined }) {
  const { t } = useTranslation();
  return (
    <Card title={t("mMarkets.coin.markets")} className="overflow-hidden">
      <ul className="divide-y divide-line-1">
        {markets.map((r) => {
          const tk = tickers.get(r.symbol);
          const contract = r.kind === "perp" ? contracts?.find((c) => c.symbol === r.symbol) : undefined;
          return (
            <li key={r.symbol}>
              <Link to={tradePath(r)} className="flex min-h-14 items-center gap-3 py-2 pl-4 pr-2 transition-colors active:bg-bg-2">
                <div className="min-w-0 flex-1">
                  <MarketName row={r} size={24} hideName />
                  {contract && <div className="pl-[34px] text-xs text-fg-3">{t("mMarkets.coin.maxLeverage", { leverage: contract.max_leverage })}</div>}
                </div>
                <PriceText value={tk?.last} decimals={r.priceDecimals} className="shrink-0 text-base font-medium" />
                <ChangeBadge value={tk?.change} className="shrink-0" />
                <ChevronRight size={16} className="shrink-0 text-fg-3" />
              </Link>
            </li>
          );
        })}
      </ul>
    </Card>
  );
}

type KeyDataProps = {
  row: MarketRow | undefined;
  ticker: TickerData | undefined;
  profile: ProfileStats | undefined;
  rank: number | null;
  feeMaker?: string;
  feeTaker?: string;
};

function KeyData({ row, ticker, profile, rank, feeMaker, feeTaker }: KeyDataProps) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const dash = "—";
  const items: KeyValueItem[] = [
    { key: "high", label: t("market.high"), value: row ? formatPrice(ticker?.high, row.priceDecimals) : dash },
    { key: "low", label: t("market.low"), value: row ? formatPrice(ticker?.low, row.priceDecimals) : dash },
    { key: "volume", label: t("market.volume"), value: row && ticker ? `${formatCompact(ticker.volume, locale)} ${row.base}` : dash },
    { key: "turnover", label: t("market.turnover"), value: row && ticker ? `${formatCompact(ticker.quote_volume, locale)} ${row.quote}` : dash },
    { key: "cap", label: t("mMarkets.coin.marketCap"), value: profile?.marketCap ? `${formatCompact(profile.marketCap, locale)} USD` : dash },
    { key: "rank", label: t("mMarkets.coin.marketCapRank"), value: rank !== null ? `#${rank}` : dash },
  ];
  if (feeMaker && feeTaker) {
    items.push({ key: "fees", label: t("mMarkets.coin.pairFees"), value: `${formatPercent(feeMaker, 2, false)} / ${formatPercent(feeTaker, 2, false)}` });
  }
  return (
    <Card title={t("mMarkets.coin.keyData")}>
      <div className="px-4 pb-4 pt-2">
        <KeyValue items={items} layout="grid" columns={2} />
        {profile && (
          <p className="mt-4 border-t border-line-1 pt-3 text-xs text-fg-3">{t("mMarkets.coin.asOf", { date: formatTime(profile.asOf, "date", locale, "UTC") })}</p>
        )}
      </div>
    </Card>
  );
}

function About({ profile }: { profile: ProfileStats | undefined }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const links: { key: string; label: string; href?: string; icon: ReactNode }[] = [
    { key: "website", label: t("mMarkets.coin.website"), href: profile?.links.website, icon: <Globe size={14} /> },
    { key: "explorer", label: t("mMarkets.coin.explorer"), href: profile?.links.explorer, icon: <Search size={14} /> },
    { key: "whitepaper", label: t("mMarkets.coin.whitepaper"), href: profile?.links.whitepaper, icon: <BookOpen size={14} /> },
  ];
  const present = links.filter((l) => l.href && /^https?:\/\//.test(l.href));
  return (
    <Card title={t("mMarkets.coin.about")}>
      <div className="px-4 pb-4 pt-1">
        <p className="text-sm leading-relaxed text-fg-2">{profile?.intro[locale] || t("mMarkets.coin.noIntro")}</p>
        {present.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-2">
            {present.map((l) => (
              <a
                key={l.key}
                href={l.href}
                target="_blank"
                rel="noopener noreferrer"
                className="flex h-tap items-center gap-1.5 rounded-2 border border-line-2 px-3 text-sm text-fg-2 transition-colors active:border-brand active:text-brand"
              >
                {l.icon}
                {l.label}
                <ExternalLink size={12} className="text-fg-3" />
              </a>
            ))}
          </div>
        )}
      </div>
    </Card>
  );
}

function CoinSkeleton() {
  return (
    <div className="flex flex-col gap-4 px-4 pb-8 pt-3">
      <div className="rounded-3 border border-line-1 bg-bg-1 p-4">
        <div className="flex items-center gap-3">
          <Skeleton round className="size-11" />
          <div className="flex flex-1 flex-col gap-2">
            <Skeleton className="h-5 w-32" />
            <Skeleton className="h-4 w-20" />
          </div>
        </div>
        <Skeleton className="mt-4 h-8 w-48" />
      </div>
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <div className="h-11" />
        <Skeleton className="mx-3 mb-3 h-[248px]" />
      </div>
      <div className="rounded-3 border border-line-1 bg-bg-1 p-4">
        <SkeletonLines lines={4} />
      </div>
    </div>
  );
}
