import {
  DEFAULT_SYMBOL,
  dec,
  errorText,
  formatPercent,
  formatTime,
  marketApi,
  qk,
  routes,
  selectSignedIn,
  unwrap,
  useSession,
  useSettings,
  usePairs,
  type TickerData,
} from "@exchange/core";
import { latestArticles, useArticles, useHero } from "@exchange/core/content/index";
import {
  compactParts,
  headline,
  marqueeRows,
  rankOverview,
  useMarketRows,
  useMarketTickers,
  type MarketRow,
  type TickerOf,
} from "@exchange/core/markets/index";
import { useBranding, useWelcomeCredits } from "@exchange/core/platform/index";
import {
  Badge,
  Button,
  ChangeBadge,
  CoinIcon,
  CountUp,
  EmptyState,
  ErrorState,
  Marquee,
  PriceText,
  Skeleton,
  SkeletonLines,
  Tag,
  cn,
  listItem,
} from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  BarChart3,
  CandlestickChart,
  Check,
  Layers,
  Megaphone,
  Network,
  ShieldCheck,
  Sparkles,
  TrendingDown,
  TrendingUp,
  UserPlus,
  Wallet,
  Waves,
} from "lucide-react";
import { motion } from "motion/react";
import { memo, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { MarketName, SparkCell, marketLabel, tradePath, useCoinName } from "./parts";

// Home (design §6.2): the hero with the headline figures and a live card,
// the top coins scrolling by, the three market boards, why us, three
// steps to start and the latest announcements. Every price comes from the
// one tickers subscription (at most one render per frame); nothing needs
// a session.

export default function Home() {
  const { rows, loading, error, refetch } = useMarketRows();
  const tickers = useMarketTickers();
  const summary = useQuery({
    queryKey: qk.summary(5),
    queryFn: () => unwrap(marketApi.GET("/v1/market/summary", { params: { query: { limit: 5 } } })),
    staleTime: 60_000,
  });
  // The summary's tickers stand in for symbols the live map lacks.
  const fallback = useMemo(() => {
    const d = summary.data;
    return new Map<string, TickerData>(d ? [...d.gainers, ...d.losers, ...d.turnover].map((t) => [t.symbol, t]) : []);
  }, [summary.data]);
  const tickerOf: TickerOf = useCallback((s) => tickers.get(s) ?? fallback.get(s), [tickers, fallback]);

  return (
    <div>
      <Hero rows={rows} loading={loading} error={error} onRetry={refetch} tickerOf={tickerOf} />
      <TopStrip rows={rows} loading={loading} tickerOf={tickerOf} />
      <Overview rows={rows} loading={loading} error={error} onRetry={refetch} tickerOf={tickerOf} />
      <Why rows={rows} />
      <Steps />
      <News />
      <Cta />
    </div>
  );
}

type Section = { rows: MarketRow[]; loading: boolean; tickerOf: TickerOf };
type WithError = Section & { error: unknown; onRetry: () => void };

/** useRollIn gives CountUp "0" first, then the value, so figures roll in on arrival. */
function useRollIn(value: string | null | undefined): string | null | undefined {
  const [ready, setReady] = useState(false);
  useEffect(() => {
    if (!value || ready) return;
    const id = requestAnimationFrame(() => setReady(true));
    return () => cancelAnimationFrame(id);
  }, [value, ready]);
  return value && !ready ? "0" : value;
}

function Hero({ rows, loading, error, onRetry, tickerOf }: WithError) {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  // The console's home-hero (design 2026-10-04 §4.4), else the bundled draft, else these strings.
  const hero = useHero().data;
  const cta = hero?.cta ?? { text: t("pc.start"), href: routes.register };
  const learning = useBranding().learning_mode.enabled;
  return (
    <section className="relative overflow-hidden border-b border-line-1">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[linear-gradient(to_right,var(--line-1)_1px,transparent_1px),linear-gradient(to_bottom,var(--line-1)_1px,transparent_1px)] bg-[size:48px_48px] opacity-60 mask-b-from-10%"
      />
      <div aria-hidden className="pointer-events-none absolute -top-48 right-[-8%] size-[640px] animate-float rounded-full bg-brand-soft blur-3xl" />
      <div
        aria-hidden
        className="pointer-events-none absolute -bottom-56 left-[-10%] size-[560px] animate-float rounded-full bg-info/10 blur-3xl [animation-delay:-7s] [animation-duration:19s]"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute left-[42%] top-1/4 size-[320px] animate-float rounded-full bg-glow blur-3xl [animation-delay:-3s] [animation-duration:23s]"
      />
      <div className="relative mx-auto grid max-w-[1440px] grid-cols-[minmax(0,1fr)_380px] items-center gap-10 px-6 py-16 xl:grid-cols-[minmax(0,1fr)_440px] xl:py-20">
        <motion.div className="flex min-w-0 flex-col items-start gap-6" variants={listItem} initial="initial" animate="animate" custom={0}>
          {learning && (
            <Badge tone="brand" size="md" dot>
              {t("pcMarkets.home.badge")}
            </Badge>
          )}
          <h1 className="max-w-2xl text-2xl font-semibold leading-tight text-fg-1 xl:text-[44px]">{hero?.title || t("pc.heroTitle")}</h1>
          <p className="max-w-xl text-md leading-relaxed text-fg-2">{hero ? hero.subtitle : t("pc.heroSubtitle")}</p>
          <div className="flex flex-wrap gap-3">
            {signedIn ? (
              <>
                <Button asChild size="lg" icon={<CandlestickChart size={18} />}>
                  <Link to={routes.trade(DEFAULT_SYMBOL)}>{t("pc.trade")}</Link>
                </Button>
                <Button asChild size="lg" variant="secondary" icon={<Wallet size={18} />}>
                  <Link to={routes.assets}>{t("pcMarkets.home.assets")}</Link>
                </Button>
              </>
            ) : (
              <>
                <Button asChild size="lg" icon={<UserPlus size={18} />}>
                  {cta.href.startsWith("/") ? <Link to={cta.href}>{cta.text}</Link> : <a href={cta.href}>{cta.text}</a>}
                </Button>
                <Button asChild size="lg" variant="secondary" icon={<CandlestickChart size={18} />}>
                  <Link to={routes.trade(DEFAULT_SYMBOL)}>{t("pc.trade")}</Link>
                </Button>
              </>
            )}
          </div>
          <HeadlineStats rows={rows} loading={loading} tickerOf={tickerOf} />
        </motion.div>
        <LiveCard rows={rows} loading={loading} error={error} onRetry={onRetry} tickerOf={tickerOf} />
      </div>
    </section>
  );
}

function HeadlineStats({ rows, loading, tickerOf }: Section) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const pairs = usePairs();
  const h = useMemo(() => headline(rows, tickerOf), [rows, tickerOf]);
  const turnover = compactParts(h.turnover, locale);
  // The lowest taker fee of the trading spot pairs (0.10% today).
  const fee = useMemo(() => {
    const rates = (pairs.data?.pairs ?? []).filter((p) => p.status === "TRADING").map((p) => p.taker_fee_rate);
    return rates.length ? rates.reduce((a, b) => (dec.lte(a, b) ? a : b)) : null;
  }, [pairs.data]);
  const turnoverValue = useRollIn(loading ? null : turnover.value);
  const marketsValue = useRollIn(loading ? null : String(h.spot + h.perps));
  const leverageValue = useRollIn(loading || h.maxLeverage === 0 ? null : String(h.maxLeverage));
  const stats: { key: string; label: string; value: ReactNode }[] = [
    {
      key: "turnover",
      label: t("pcMarkets.home.stats.turnover"),
      value: <CountUp value={turnoverValue} decimals={2} suffix={<span className="ml-1 text-md text-fg-2">{turnover.suffix}</span>} />,
    },
    { key: "markets", label: t("pcMarkets.home.stats.markets"), value: <CountUp value={marketsValue} decimals={0} /> },
    { key: "leverage", label: t("pcMarkets.home.stats.leverage"), value: <CountUp value={leverageValue} decimals={0} suffix="x" /> },
    { key: "fee", label: t("pcMarkets.home.stats.fee"), value: <span>{formatPercent(fee, 2, false)}</span> },
  ];
  return (
    // Side by side, each as wide as its one-line label, a fixed gap apart; a
    // figure that does not fit moves to the next row whole.
    <dl className="mt-4 flex w-fit max-w-full flex-wrap gap-x-12 gap-y-4 border-t border-line-1 pt-6">
      {stats.map((s) => (
        <div key={s.key} className="flex flex-col-reverse">
          <dt className="mt-1 whitespace-nowrap text-xs text-fg-3">{s.label}</dt>
          <dd className="text-xl font-semibold text-fg-1 tabular-nums">{loading ? <Skeleton className="h-7 w-20" /> : s.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** LiveCard: the top markets with prices flashing as they move, beside the hero text. */
function LiveCard({ rows, loading, error, onRetry, tickerOf }: WithError) {
  const { t } = useTranslation();
  const list = useMemo(() => marqueeRows(rows, 4), [rows]);
  return (
    <motion.aside
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={2}
      className="relative rounded-3 border border-line-2 bg-bg-1/80 p-5 shadow-pop backdrop-blur transition-transform duration-[var(--t-slow)] hover:-translate-y-1"
    >
      <div className="flex items-center justify-between">
        <h2 className="flex items-center gap-2 text-sm font-medium text-fg-1">
          <span className="relative flex size-2">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-up opacity-60" />
            <span className="relative inline-flex size-2 rounded-full bg-up" />
          </span>
          {t("pcMarkets.home.live")}
        </h2>
        <Link to={routes.markets} className="flex items-center gap-1 text-xs text-fg-3 transition-colors hover:text-brand">
          {t("pcMarkets.viewAll")} <ArrowRight size={12} />
        </Link>
      </div>
      <ul className="mt-4 flex flex-col gap-1">
        {loading ? (
          [0, 1, 2, 3].map((i) => (
            <li key={i} className="flex h-14 items-center gap-3">
              <Skeleton round className="size-8" />
              <SkeletonLines lines={2} className="flex-1" />
            </li>
          ))
        ) : error && rows.length === 0 ? (
          <li>
            <ErrorState compact message={errorText(error)} onRetry={onRetry} />
          </li>
        ) : list.length === 0 ? (
          <li>
            <EmptyState compact />
          </li>
        ) : (
          list.map((r) => {
            const tk = tickerOf(r.symbol);
            return (
              <li key={r.symbol}>
                <Link to={tradePath(r)} className="-mx-2 flex h-14 items-center gap-3 rounded-2 px-2 transition-colors hover:bg-bg-2">
                  <div className="min-w-0 flex-1">
                    <MarketName row={r} size={32} />
                  </div>
                  <div className="hidden shrink-0 xl:block">
                    <SparkCell symbol={r.symbol} width={72} height={28} />
                  </div>
                  <div className="flex w-24 shrink-0 flex-col items-end gap-0.5">
                    <PriceText value={tk?.last} decimals={r.priceDecimals} className="text-sm font-medium" />
                    <ChangeBadge value={tk?.change} variant="soft" className="min-w-0" />
                  </div>
                </Link>
              </li>
            );
          })
        )}
      </ul>
      <p className="mt-4 border-t border-line-1 pt-3 text-xs text-fg-3">{t("pcMarkets.home.liveHint")}</p>
    </motion.aside>
  );
}

/** TopStrip: the top coins by rank scrolling sideways (paused on hover). */
function TopStrip({ rows, loading, tickerOf }: Section) {
  const { t } = useTranslation();
  const empty = !loading && rows.length === 0;
  const items = useMemo(() => {
    const top = marqueeRows(rows, 10);
    if (top.length === 0) return [];
    // A short list repeats so the strip is wider than the widest screen.
    const reps = Math.max(1, Math.ceil(10 / top.length));
    return Array.from({ length: reps }, () => top).flat();
  }, [rows]);
  // Nothing to scroll (the pairs failed to load): the boards below show the error.
  if (empty) return null;
  return (
    <section aria-label={t("pcMarkets.home.marquee")} className="border-b border-line-1 bg-bg-1/60">
      <div className="h-12">
        {loading || items.length === 0 ? (
          <div className="mx-auto flex h-full max-w-[1440px] items-center gap-10 px-6">
            {[0, 1, 2, 3, 4].map((i) => (
              <Skeleton key={i} className="h-4 w-40" />
            ))}
          </div>
        ) : (
          <Marquee duration={Math.max(28, items.length * 5)} gap={40} className="h-full">
            {items.map((r, i) => {
              const tk = tickerOf(r.symbol);
              return (
                <Link key={`${r.symbol}-${i}`} to={tradePath(r)} className="flex h-12 items-center gap-2 whitespace-nowrap text-sm transition-colors hover:text-brand">
                  <CoinIcon symbol={r.base} size={18} />
                  <span className="font-medium text-fg-1">{marketLabel(r)}</span>
                  <PriceText value={tk?.last} decimals={r.priceDecimals} className="text-fg-2" />
                  <ChangeBadge value={tk?.change} variant="soft" className="min-w-0" />
                </Link>
              );
            })}
          </Marquee>
        )}
      </div>
    </section>
  );
}

const BOARD_ROWS = 5;

function Overview({ rows, loading, error, onRetry, tickerOf }: WithError) {
  const { t } = useTranslation();
  const lists = useMemo(() => rankOverview(rows, tickerOf, BOARD_ROWS), [rows, tickerOf]);
  const boards = [
    { id: "gainers", title: t("market.gainers"), icon: <TrendingUp size={18} className="text-up" />, list: lists.gainers, to: `${routes.markets}?sort=change&dir=desc` },
    { id: "losers", title: t("market.losers"), icon: <TrendingDown size={18} className="text-down" />, list: lists.losers, to: `${routes.markets}?sort=change&dir=asc` },
    { id: "turnover", title: t("market.hot"), icon: <BarChart3 size={18} className="text-brand" />, list: lists.turnover, to: `${routes.markets}?sort=turnover&dir=desc` },
  ];
  return (
    <section className="mx-auto max-w-[1440px] px-6 pt-14">
      <SectionTitle title={t("pc.overview")} hint={t("pcMarkets.home.overviewHint")} action={<MoreLink to={routes.markets}>{t("pcMarkets.viewAll")}</MoreLink>} />
      {error && rows.length === 0 && !loading ? (
        <div className="mt-6 rounded-3 border border-line-1 bg-bg-1">
          <ErrorState message={errorText(error)} onRetry={onRetry} />
        </div>
      ) : (
        <div className="mt-6 grid grid-cols-3 gap-4">
          {boards.map((b, i) => (
            <motion.section
              key={b.id}
              variants={listItem}
              initial="initial"
              animate="animate"
              custom={i}
              className="flex min-w-0 flex-col rounded-3 border border-line-1 bg-bg-1 p-4 transition-[transform,border-color,box-shadow] duration-[var(--t-base)] hover:-translate-y-1 hover:border-line-2 hover:shadow-pop"
            >
              <header className="flex items-center justify-between">
                <h3 className="flex items-center gap-2 text-base font-medium text-fg-1">
                  {b.icon}
                  {b.title}
                </h3>
                <MoreLink to={b.to}>{t("pcMarkets.viewAll")}</MoreLink>
              </header>
              <ol className="mt-3 flex flex-col">
                {loading ? (
                  Array.from({ length: BOARD_ROWS }, (_, k) => (
                    <li key={k} className="flex h-[52px] items-center gap-3">
                      <Skeleton round className="size-7" />
                      <Skeleton className="h-4 w-24" />
                      <Skeleton className="ml-auto h-4 w-16" />
                    </li>
                  ))
                ) : (
                  <>
                    {b.list.map((r, k) => (
                      <BoardRow key={r.symbol} row={r} index={k} ticker={tickerOf(r.symbol)} />
                    ))}
                    {b.list.length < BOARD_ROWS && <BoardFiller rows={BOARD_ROWS - b.list.length} empty={b.list.length === 0} />}
                  </>
                )}
              </ol>
            </motion.section>
          ))}
        </div>
      )}
    </section>
  );
}

function BoardRow({ row, index, ticker }: { row: MarketRow; index: number; ticker: TickerData | undefined }) {
  const nameOf = useCoinName();
  return (
    <li>
      <Link to={tradePath(row)} className="-mx-2 flex h-[52px] items-center gap-3 rounded-2 px-2 transition-colors hover:bg-bg-2">
        <span className="w-4 text-center text-xs text-fg-3 tabular-nums">{index + 1}</span>
        <CoinIcon symbol={row.base} size={28} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium text-fg-1">{marketLabel(row)}</div>
          <div className="truncate text-xs text-fg-3">{nameOf(row)}</div>
        </div>
        <div className="hidden shrink-0 xl:block">
          <SparkCell symbol={row.symbol} width={64} height={24} />
        </div>
        <div className="flex w-24 shrink-0 flex-col items-end gap-0.5">
          <PriceText value={ticker?.last} decimals={row.priceDecimals} className="text-sm" />
          <ChangeBadge value={ticker?.change} variant="soft" className="min-w-0" />
        </div>
      </Link>
    </li>
  );
}

/** BoardFiller keeps the three boards the same height while few pairs trade. */
function BoardFiller({ rows, empty }: { rows: number; empty: boolean }) {
  const { t } = useTranslation();
  return (
    <li
      className="flex flex-col items-center justify-center gap-1 rounded-2 border border-dashed border-line-1 text-center"
      style={{ height: rows * 52 - 8, marginTop: empty ? 0 : 8 }}
    >
      <Sparkles size={18} className="text-fg-3" />
      <span className="text-sm text-fg-2">{t(empty ? "state.emptyTitle" : "pcMarkets.home.moreSoon")}</span>
      <span className="px-4 text-xs text-fg-3">{t("pcMarkets.home.moreSoonHint")}</span>
    </li>
  );
}

// The sections below do not show prices: memo keeps the per-second ticker renders away from them.
const Why = memo(function Why({ rows }: { rows: MarketRow[] }) {
  const { t } = useTranslation();
  // The contracts' own leverage (instrument-service); 0 until they load.
  const leverage = rows.reduce((m, r) => Math.max(m, r.maxLeverage), 0);
  const tiles = [
    { id: "wallet", icon: <Network size={22} /> },
    { id: "futures", icon: <Layers size={22} /> },
    { id: "liquidity", icon: <Waves size={22} /> },
    { id: "security", icon: <ShieldCheck size={22} /> },
  ];
  return (
    <section className="mx-auto max-w-[1440px] px-6 pt-20">
      <SectionTitle title={t("pcMarkets.home.whyTitle")} hint={t("pcMarkets.home.whyHint")} />
      <div className="mt-6 grid grid-cols-4 gap-4">
        {tiles.map((tile, i) => (
          <motion.article
            key={tile.id}
            variants={listItem}
            initial="initial"
            whileInView="animate"
            viewport={{ once: true, margin: "-40px" }}
            custom={i}
            className="group relative min-w-0 overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-5 transition-[transform,border-color,box-shadow] duration-[var(--t-base)] hover:-translate-y-1 hover:border-line-2 hover:shadow-pop"
          >
            <span aria-hidden className="pointer-events-none absolute -right-6 -top-6 text-fg-3 opacity-10 transition-transform duration-[var(--t-slow)] group-hover:scale-110 [&_svg]:size-24">
              {tile.icon}
            </span>
            <span className="grid size-11 place-items-center rounded-2 bg-brand-soft text-brand">{tile.icon}</span>
            <h3 className="mt-4 text-md font-semibold text-fg-1">{t(`pcMarkets.home.why.${tile.id}.title`)}</h3>
            <p className="mt-2 text-sm leading-relaxed text-fg-3">{t(`pcMarkets.home.why.${tile.id}.${tile.id === "futures" && leverage === 0 ? "descAny" : "desc"}`, { leverage })}</p>
          </motion.article>
        ))}
      </div>
    </section>
  );
});

const Steps = memo(function Steps() {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const credits = useWelcomeCredits();
  const steps = [
    { id: "register", icon: <UserPlus size={20} />, to: routes.register, done: signedIn },
    { id: "deposit", icon: <Wallet size={20} />, to: routes.deposit, done: false },
    { id: "trade", icon: <CandlestickChart size={20} />, to: routes.trade(DEFAULT_SYMBOL), done: false },
  ];
  return (
    <section className="mx-auto max-w-[1440px] px-6 pt-20">
      <SectionTitle
        title={t("pcMarkets.home.stepsTitle")}
        hint={credits ? t("pcMarkets.home.stepsHintCredits", { credits }) : t("pcMarkets.home.stepsHint")}
      />
      <ol className="relative mt-8 grid grid-cols-3 gap-6">
        <span aria-hidden className="absolute left-[16.6%] right-[16.6%] top-6 h-px bg-[linear-gradient(to_right,var(--line-2)_50%,transparent_50%)] bg-[size:8px_1px]" />
        {steps.map((s, i) => (
          <motion.li
            key={s.id}
            variants={listItem}
            initial="initial"
            whileInView="animate"
            viewport={{ once: true, margin: "-40px" }}
            custom={i}
            className="relative flex min-w-0 flex-col items-center text-center"
          >
            <span
              className={cn(
                "relative z-10 grid size-12 place-items-center rounded-full border-2 bg-bg-0 transition-colors",
                s.done ? "border-brand bg-brand text-brand-fg" : "border-line-2 text-brand",
              )}
            >
              {s.done ? <Check size={20} strokeWidth={3} /> : s.icon}
            </span>
            <span className="mt-3 text-xs font-medium tracking-wide text-fg-3">{`0${i + 1}`}</span>
            <h3 className="mt-1 text-md font-semibold text-fg-1">{t(`pcMarkets.home.steps.${s.id}.title`)}</h3>
            <p className="mt-2 max-w-xs text-sm leading-relaxed text-fg-3">
              {s.id === "register" && credits ? t("pcMarkets.home.steps.register.descCredits", { credits }) : t(`pcMarkets.home.steps.${s.id}.desc`)}
            </p>
            {s.done ? (
              <Badge tone="success" size="md" className="mt-4" icon={<Check size={12} />}>
                {t("pcMarkets.home.stepDone")}
              </Badge>
            ) : (
              <Button asChild size="sm" variant={i === 0 || (signedIn && i === 1) ? "primary" : "secondary"} className="mt-4">
                <Link to={s.to}>{t(`pcMarkets.home.steps.${s.id}.action`)}</Link>
              </Button>
            )}
          </motion.li>
        ))}
      </ol>
    </section>
  );
});

const News = memo(function News() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const articles = useArticles("announcements");
  const latest = useMemo(() => latestArticles(articles.data ?? [], 3), [articles.data]);
  return (
    <section className="mx-auto max-w-[1440px] px-6 pt-20">
      <SectionTitle title={t("pcMarkets.home.newsTitle")} action={<MoreLink to={routes.announcements}>{t("pcMarkets.home.newsAll")}</MoreLink>} />
      <div className="mt-6 grid grid-cols-3 gap-4">
        {articles.isPending ? (
          [0, 1, 2].map((i) => (
            <div key={i} className="rounded-3 border border-line-1 bg-bg-1 p-5">
              <Skeleton className="h-3 w-24" />
              <SkeletonLines lines={3} className="mt-4" />
            </div>
          ))
        ) : articles.isError ? (
          <div className="col-span-3 rounded-3 border border-line-1 bg-bg-1">
            <ErrorState compact message={errorText(articles.error)} onRetry={() => void articles.refetch()} />
          </div>
        ) : latest.length === 0 ? (
          <div className="col-span-3 rounded-3 border border-line-1 bg-bg-1">
            <EmptyState compact title={t("pcMarkets.home.newsEmpty")} />
          </div>
        ) : (
          latest.map((a, i) => (
            <motion.article
              key={a.slug}
              variants={listItem}
              initial="initial"
              whileInView="animate"
              viewport={{ once: true, margin: "-40px" }}
              custom={i}
              className="min-w-0"
            >
              <Link
                to={routes.announcement(a.slug)}
                className="group flex h-full flex-col rounded-3 border border-line-1 bg-bg-1 p-5 transition-[transform,border-color,box-shadow] duration-[var(--t-base)] hover:-translate-y-1 hover:border-line-2 hover:shadow-pop"
              >
                <div className="flex items-center gap-2 text-xs text-fg-3">
                  <Megaphone size={14} className="text-brand" />
                  <time dateTime={a.date}>{formatTime(a.date, "date", locale, "UTC")}</time>
                  <Tag className="ml-auto">{t(`pcContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>
                </div>
                <h3 className="mt-3 line-clamp-2 text-md font-semibold text-fg-1 transition-colors group-hover:text-brand">{a.title}</h3>
                <p className="mt-2 line-clamp-3 text-sm leading-relaxed text-fg-3">{a.summary}</p>
                <span className="mt-auto flex items-center gap-1 pt-4 text-sm text-brand">
                  {t("pcContent.announcements.readMore")}
                  <ArrowRight size={14} className="transition-transform group-hover:translate-x-0.5" />
                </span>
              </Link>
            </motion.article>
          ))
        )}
      </div>
    </section>
  );
});

const Cta = memo(function Cta() {
  const { t } = useTranslation();
  const signedIn = useSession(selectSignedIn);
  const credits = useWelcomeCredits();
  return (
    <section className="mx-auto max-w-[1440px] px-6 py-20">
      <div className="relative overflow-hidden rounded-3 border border-line-2 bg-bg-1 px-10 py-12">
        <div aria-hidden className="pointer-events-none absolute -right-24 -top-24 size-80 animate-float rounded-full bg-brand-soft blur-3xl" />
        <div className="relative flex items-center justify-between gap-8">
          <div className="min-w-0">
            <h2 className="text-xl font-semibold text-fg-1">{t("pcMarkets.home.ctaTitle")}</h2>
            <p className="mt-2 text-fg-2">{credits ? t("pcMarkets.home.ctaDescCredits", { credits }) : t("pcMarkets.home.ctaDesc")}</p>
          </div>
          <div className="flex shrink-0 gap-3">
            <Button asChild size="lg">
              <Link to={signedIn ? routes.trade(DEFAULT_SYMBOL) : routes.register}>{signedIn ? t("pc.trade") : t("pc.start")}</Link>
            </Button>
            <Button asChild size="lg" variant="secondary">
              <Link to={routes.markets}>{t("nav.markets")}</Link>
            </Button>
          </div>
        </div>
      </div>
    </section>
  );
});

function SectionTitle({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex items-end justify-between gap-4">
      <div>
        <h2 className="text-lg font-semibold text-fg-1">{title}</h2>
        {hint && <p className="mt-1 text-sm text-fg-3">{hint}</p>}
      </div>
      {action}
    </div>
  );
}

function MoreLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className="flex shrink-0 items-center gap-1 text-sm text-fg-3 transition-colors hover:text-brand">
      {children}
      <ArrowRight size={14} />
    </Link>
  );
}
