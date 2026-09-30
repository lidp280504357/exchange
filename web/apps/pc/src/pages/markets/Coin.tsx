import {
  coinProfile,
  errorText,
  formatCompact,
  formatDecimal,
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
  type TickerData,
} from "@exchange/core";
import { defaultOrder, useMarketRows, useMarketTickers, type MarketRow } from "@exchange/core/markets/index";
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
  Segmented,
  Skeleton,
  SkeletonLines,
  Tag,
  listItem,
  type KeyValueItem,
} from "@exchange/ui";
import { BookOpen, ChevronRight, ExternalLink, Globe, Layers, Search } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useParams } from "react-router";
import { FavoriteStar } from "../../features/markets/FavoriteStar";
import { usePageTitle } from "./hooks";
import { MarketName, marketLabel, tradePath, useFavoriteToggle } from "./parts";

// /coin/:symbol (design §6.2): the coin's header with its live price and
// a favourite star, a large chart (1D by default, the chart library loads
// with the chart only), 24-hour figures, market cap and rank from the
// static profile with its date, the introduction and links, its markets
// and the related perpetual contract. "/coin/btc" and "/coin/BTC-USDT"
// lead to "/coin/BTC"; a coin the site does not list says so.

const INTERVALS: CandleInterval[] = ["15m", "1h", "4h", "1d", "1w", "1M"];

/** The static profile may carry market figures (asOf dated); none do yet. */
type ProfileStats = CoinProfile & { marketCap?: string; rank?: number };

export default function Coin() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const params = useParams();
  const raw = params.symbol ?? "";
  const coin = (raw.split("-")[0] ?? "").toUpperCase();

  const { rows, loading, error, refetch } = useMarketRows();
  const tickers = useMarketTickers();
  const assets = useAssets();
  const contracts = useContracts();
  const pairs = usePairs();
  const fav = useFavoriteToggle();

  const markets = useMemo(() => rows.filter((r) => r.base === coin).sort(defaultOrder), [rows, coin]);
  const primary = markets.find((r) => r.kind === "spot" && r.quote === "USDT") ?? markets.find((r) => r.kind === "spot") ?? markets[0];
  // The trade button goes where orders are taken: a trading spot pair first.
  const tradeTarget = markets.find((r) => r.kind === "spot" && r.status === "TRADING") ?? markets.find((r) => r.status === "TRADING") ?? primary;
  const perp = markets.find((r) => r.kind === "perp");
  const contract = contracts.data?.contracts.find((c) => c.symbol === perp?.symbol);
  const pair = pairs.data?.pairs.find((p) => p.symbol === primary?.symbol);
  const asset = assets.data?.assets.find((a) => a.asset_code === coin);
  const profile = coinProfile(coin) as ProfileStats | undefined;
  const name = profile?.name[locale] ?? primary?.name ?? asset?.name ?? coin;

  const [picked, setPicked] = useState<string | null>(null);
  const chartRow = markets.find((r) => r.symbol === picked) ?? primary;
  const [interval, setChartInterval] = useState<CandleInterval>("1d");
  const candles = useCandles(chartRow?.symbol ?? "", interval);

  usePageTitle(coin ? `${coin} ${name === coin ? "" : name}`.trim() : undefined);

  if (raw !== coin && coin) return <Navigate to={routes.coin(coin)} replace />;

  if (loading || (markets.length === 0 && assets.isPending)) return <CoinSkeleton />;
  if (error && rows.length === 0) {
    return (
      <Page>
        <ErrorState message={errorText(error)} onRetry={refetch} />
      </Page>
    );
  }
  if (markets.length === 0 && !asset) {
    return (
      <Page>
        <EmptyState
          title={t("pcMarkets.coin.notFound")}
          description={t("pcMarkets.coin.notFoundHint", { symbol: coin || raw })}
          action={
            <Button asChild size="sm" icon={<Search size={14} />}>
              <Link to={routes.markets}>{t("pcMarkets.coin.back")}</Link>
            </Button>
          }
        />
      </Page>
    );
  }

  const tk = primary ? tickers.get(primary.symbol) : undefined;
  const rank = profile?.rank ?? primary?.rank ?? asset?.rank ?? null;
  const categories = primary?.categories ?? asset?.categories ?? [];

  return (
    <Page>
      <nav aria-label={t("pcMarkets.breadcrumb")} className="flex items-center gap-1 text-sm text-fg-3">
        <Link to={routes.markets} className="transition-colors hover:text-fg-1">
          {t("nav.markets")}
        </Link>
        <ChevronRight size={14} />
        <span className="text-fg-2">{coin}</span>
      </nav>

      <motion.section
        variants={listItem}
        initial="initial"
        animate="animate"
        custom={0}
        className="relative mt-4 overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-6"
      >
        <div aria-hidden className="pointer-events-none absolute -right-20 -top-24 size-72 rounded-full bg-brand-soft blur-3xl" />
        <div className="relative flex flex-wrap items-center gap-5">
          <CoinIcon symbol={coin} size={56} label={name} />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-xl font-semibold text-fg-1">{name}</h1>
              <span className="text-md text-fg-3">{coin}</span>
              {rank !== null && <Badge tone="brand">{t("pcMarkets.coin.rank", { rank })}</Badge>}
              {categories.slice(0, 3).map((c) => (
                <Tag key={c}>{t(`pcMarkets.tags.${c}`, { defaultValue: c })}</Tag>
              ))}
            </div>
            {primary ? (
              <div className="mt-2 flex items-baseline gap-3">
                <PriceText value={tk?.last} decimals={primary.priceDecimals} className="text-2xl font-semibold" />
                <span className="text-sm text-fg-3">{primary.quote}</span>
                <ChangeBadge value={tk?.change} size="md" />
              </div>
            ) : (
              <p className="mt-2 text-sm text-fg-3">{t("pcMarkets.coin.noMarketHint", { symbol: coin })}</p>
            )}
          </div>
          {primary && (
            <div className="ml-auto flex items-center gap-2">
              <FavoriteStar
                active={fav.has(primary.symbol)}
                onToggle={() => fav.toggle(primary.symbol)}
                label={fav.has(primary.symbol) ? t("pcMarkets.favoriteRemove") : t("pcMarkets.favoriteAdd")}
                size={20}
                boxed
              />
              <Button asChild size="lg">
                <Link to={tradePath(tradeTarget ?? primary)}>{t("pcMarkets.coin.goTrade")}</Link>
              </Button>
              {perp && (
                <Button asChild size="lg" variant="secondary" icon={<Layers size={18} />}>
                  <Link to={routes.futures(perp.symbol)}>{t("pcMarkets.coin.futures")}</Link>
                </Button>
              )}
            </div>
          )}
        </div>
      </motion.section>

      <div className="mt-6 grid grid-cols-[minmax(0,1fr)_320px] gap-6 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="flex min-w-0 flex-col gap-6">
          {chartRow ? (
            <Card
              title={t("pcMarkets.coin.chart")}
              extra={
                markets.length > 1 && (
                  <Segmented
                    size="xs"
                    value={chartRow.symbol}
                    onValueChange={setPicked}
                    aria-label={t("pcMarkets.coin.markets")}
                    items={markets.map((r) => ({ value: r.symbol, label: r.kind === "perp" ? `${marketLabel(r)} ${t("pcMarkets.perp")}` : marketLabel(r) }))}
                  />
                )
              }
              flush
            >
              {candles.error && candles.candles.length === 0 ? (
                <ErrorState message={`${t("pcMarkets.coin.chartError")} · ${errorText(candles.error)}`} onRetry={() => void candles.refetch()} />
              ) : (
                <CandleChart
                  symbol={chartRow.symbol}
                  candles={candles.candles}
                  interval={interval}
                  intervals={INTERVALS}
                  onIntervalChange={(i) => setChartInterval(i as CandleInterval)}
                  priceDecimals={chartRow.priceDecimals}
                  onLoadMore={candles.loadMore}
                  hasMore={candles.hasMore}
                  loading={candles.loading}
                  height={460}
                  className="rounded-b-3"
                />
              )}
            </Card>
          ) : (
            <Card title={t("pcMarkets.coin.chart")}>
              <EmptyState
                compact
                title={t("pcMarkets.coin.noMarket")}
                description={t("pcMarkets.coin.noMarketHint", { symbol: coin })}
                action={
                  <Button asChild size="sm" variant="secondary">
                    <Link to={routes.markets}>{t("pcMarkets.coin.back")}</Link>
                  </Button>
                }
              />
            </Card>
          )}

          {markets.length > 0 && (
            <Card title={t("pcMarkets.coin.markets")} flush>
              <ul>
                {markets.map((r) => (
                  <MarketLine key={r.symbol} row={r} ticker={tickers.get(r.symbol)} />
                ))}
              </ul>
            </Card>
          )}
        </div>

        <aside className="flex min-w-0 flex-col gap-6">
          <KeyData row={primary} ticker={tk} profile={profile} rank={rank} feeMaker={pair?.maker_fee_rate} feeTaker={pair?.taker_fee_rate} />
          <About profile={profile} />
          {perp && contract && (
            <Card title={t("pcMarkets.coin.contract")}>
              <div className="flex items-center justify-between gap-3">
                <MarketName row={perp} size={28} />
                <div className="flex flex-col items-end gap-0.5">
                  <PriceText value={tickers.get(perp.symbol)?.last} decimals={perp.priceDecimals} className="font-medium" />
                  <ChangeBadge value={tickers.get(perp.symbol)?.change} variant="soft" className="min-w-0" />
                </div>
              </div>
              <KeyValue
                className="mt-4"
                items={[
                  { key: "lev", label: t("pcMarkets.coin.maxLeverage"), value: `${contract.max_leverage}x` },
                  { key: "funding", label: t("pcMarkets.coin.funding"), value: t("pcMarkets.coin.fundingEvery", { hours: contract.funding_interval_hours }) },
                  {
                    key: "fees",
                    label: t("pcMarkets.coin.fees"),
                    value: `${formatPercent(contract.maker_fee_rate, 2, false)} / ${formatPercent(contract.taker_fee_rate, 2, false)}`,
                  },
                  { key: "min", label: t("pcMarkets.coin.minQty"), value: `${formatDecimal(contract.min_quantity)} ${contract.base_asset}` },
                ]}
              />
              <Button asChild block variant="secondary" className="mt-4" icon={<Layers size={16} />}>
                <Link to={routes.futures(perp.symbol)}>{t("pcMarkets.coin.futures")}</Link>
              </Button>
            </Card>
          )}
        </aside>
      </div>
    </Page>
  );
}

function Page({ children }: { children: ReactNode }) {
  return <div className="mx-auto max-w-[1440px] px-6 py-6">{children}</div>;
}

function Card({ title, extra, children, flush }: { title: ReactNode; extra?: ReactNode; children: ReactNode; flush?: boolean }) {
  return (
    <section className="min-w-0 rounded-3 border border-line-1 bg-bg-1">
      <header className="flex min-h-12 items-center justify-between gap-3 border-b border-line-1 px-5 py-2">
        <h2 className="text-base font-medium text-fg-1">{title}</h2>
        {extra}
      </header>
      <div className={flush ? undefined : "p-5"}>{children}</div>
    </section>
  );
}

function MarketLine({ row, ticker }: { row: MarketRow; ticker: TickerData | undefined }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  return (
    <li className="flex items-center gap-4 border-b border-line-1 px-5 py-3 last:border-0">
      <div className="min-w-0 flex-1">
        <MarketName row={row} size={28} />
      </div>
      <PriceText value={ticker?.last} decimals={row.priceDecimals} className="w-32 text-right font-medium" />
      <div className="w-24 text-right">
        <ChangeBadge value={ticker?.change} />
      </div>
      <span className="w-28 text-right text-sm text-fg-2">
        {formatCompact(ticker?.quote_volume, locale)}
        <span className="ml-1 text-xs text-fg-3">{row.quote}</span>
      </span>
      <Button asChild size="sm" variant="secondary">
        <Link to={tradePath(row)}>{t("pcMarkets.trade")}</Link>
      </Button>
    </li>
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
    {
      key: "volume",
      label: t("market.volume"),
      value: row && ticker ? `${formatCompact(ticker.volume, locale)} ${row.base}` : dash,
    },
    {
      key: "turnover",
      label: t("market.turnover"),
      value: row && ticker ? `${formatCompact(ticker.quote_volume, locale)} ${row.quote}` : dash,
    },
    { key: "cap", label: t("pcMarkets.coin.marketCap"), value: profile?.marketCap ? `${formatCompact(profile.marketCap, locale)} USD` : dash },
    { key: "rank", label: t("pcMarkets.coin.marketCapRank"), value: rank !== null ? `#${rank}` : dash },
  ];
  if (feeMaker && feeTaker) {
    items.push({ key: "fees", label: t("pcMarkets.coin.pairFees"), value: `${formatPercent(feeMaker, 2, false)} / ${formatPercent(feeTaker, 2, false)}` });
  }
  return (
    <Card title={t("pcMarkets.coin.keyData")}>
      <KeyValue items={items} />
      {profile && (
        <p className="mt-4 border-t border-line-1 pt-3 text-xs text-fg-3">
          {t("pcMarkets.coin.asOf", { date: formatTime(profile.asOf, "date", locale, "UTC") })}
        </p>
      )}
    </Card>
  );
}

function About({ profile }: { profile: ProfileStats | undefined }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const links: { key: string; label: string; href?: string; icon: ReactNode }[] = [
    { key: "website", label: t("pcMarkets.coin.website"), href: profile?.links.website, icon: <Globe size={14} /> },
    { key: "explorer", label: t("pcMarkets.coin.explorer"), href: profile?.links.explorer, icon: <Search size={14} /> },
    { key: "whitepaper", label: t("pcMarkets.coin.whitepaper"), href: profile?.links.whitepaper, icon: <BookOpen size={14} /> },
  ];
  const present = links.filter((l) => l.href && /^https?:\/\//.test(l.href));
  return (
    <Card title={t("pcMarkets.coin.about")}>
      <p className="text-sm leading-relaxed text-fg-2">{profile?.intro[locale] ?? t("pcMarkets.coin.noIntro")}</p>
      {present.length > 0 && (
        <div className="mt-4 flex flex-wrap gap-2">
          {present.map((l) => (
            <a
              key={l.key}
              href={l.href}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1.5 rounded-2 border border-line-2 px-3 py-1.5 text-sm text-fg-2 transition-colors hover:border-brand hover:text-brand"
            >
              {l.icon}
              {l.label}
              <ExternalLink size={12} className="text-fg-3" />
            </a>
          ))}
        </div>
      )}
    </Card>
  );
}

function CoinSkeleton() {
  return (
    <Page>
      <Skeleton className="h-4 w-32" />
      <div className="mt-4 flex items-center gap-5 rounded-3 border border-line-1 bg-bg-1 p-6">
        <Skeleton round className="size-14" />
        <div className="flex flex-col gap-2">
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-8 w-64" />
        </div>
      </div>
      <div className="mt-6 grid grid-cols-[minmax(0,1fr)_320px] gap-6 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="rounded-3 border border-line-1 bg-bg-1 p-5">
          <Skeleton className="h-[460px] w-full" />
        </div>
        <div className="flex flex-col gap-6">
          <div className="rounded-3 border border-line-1 bg-bg-1 p-5">
            <SkeletonLines lines={6} />
          </div>
          <div className="rounded-3 border border-line-1 bg-bg-1 p-5">
            <SkeletonLines lines={4} />
          </div>
        </div>
      </div>
    </Page>
  );
}
