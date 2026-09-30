import {
  DEFAULT_SYMBOL,
  dec,
  errorText,
  formatAmount,
  formatPercent,
  formatTime,
  mayHaveSession,
  qk,
  routes,
  selectRestoring,
  selectSignedIn,
  useSession,
  useSettings,
  type TickerData,
} from "@exchange/core";
import { useBalances } from "@exchange/core/assets/hooks";
import { convertValue, referencePrice, valuePortfolio } from "@exchange/core/assets/valuation";
import { useArticles } from "@exchange/core/content/index";
import { marqueeRows, rankOverview, useMarketRows, useMarketTickers, type MarketRow, type TickerOf } from "@exchange/core/markets/index";
import {
  Badge,
  Button,
  ChangeBadge,
  CoinIcon,
  CountUp,
  EmptyState,
  ErrorState,
  HIDDEN_AMOUNT,
  PriceText,
  Skeleton,
  SkeletonLines,
  Tag,
  cn,
  listItem,
  toneOf,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowDownToLine,
  ArrowLeftRight,
  ArrowUpFromLine,
  CandlestickChart,
  ChevronRight,
  Eye,
  EyeOff,
  Layers,
  Megaphone,
  Network,
  Pin,
  ShieldCheck,
  UserPlus,
  Waves,
} from "lucide-react";
import { motion } from "motion/react";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { PillBar } from "../../components/PillBar";
import { PullToRefresh } from "../../components/PullToRefresh";
import { boardPath, maxLeverage, type Board } from "./logic";
import { DaySpark, MarketName, SectionHead, marketLabel, tradePath } from "./parts";
import { daySparkRoot } from "./spark";

// The mobile home tab (design §7.2): a sign-up card for visitors, or the
// total value (the eye hides it) with the four shortcuts; the latest
// announcements to swipe through; the top coins as cards with their
// 24-hour line; the gainers and losers; why Astras. Prices come from the
// one tickers subscription; pulling the page down reloads what the server
// sent.

type Section = { rows: MarketRow[]; loading: boolean; error: unknown; onRetry: () => void; tickerOf: TickerOf };

const TOP_COINS = 8;
const BOARD_ROWS = 5;
const BANNER_SLIDES = 4;
const TONE = { up: "text-up", down: "text-down", neutral: "text-fg-2" } as const;

export default function Home() {
  const { t } = useTranslation();
  const { rows, loading, error, refetch } = useMarketRows();
  const tickers = useMarketTickers();
  const tickerOf: TickerOf = useCallback((s) => tickers.get(s), [tickers]);
  const qc = useQueryClient();
  const refresh = useCallback(
    () =>
      Promise.all([
        qc.refetchQueries({ queryKey: qk.pairs }),
        qc.refetchQueries({ queryKey: qk.contracts }),
        qc.refetchQueries({ queryKey: qk.tickers }),
        qc.refetchQueries({ queryKey: qk.balances }),
        qc.invalidateQueries({ queryKey: daySparkRoot }),
      ]),
    [qc],
  );
  const leverage = useMemo(() => maxLeverage(rows), [rows]);
  return (
    <PullToRefresh onRefresh={refresh}>
      <div className="flex flex-col gap-6 px-4 pb-8 pt-3">
        <h1 className="sr-only">{t("nav.home")}</h1>
        <Top tickers={tickers} />
        <NewsBanner />
        <TopCoins rows={rows} loading={loading} error={error} onRetry={refetch} tickerOf={tickerOf} />
        <Boards rows={rows} loading={loading} error={error} onRetry={refetch} tickerOf={tickerOf} />
        <Why leverage={leverage} />
      </div>
    </PullToRefresh>
  );
}

/** Top: the sign-up card for visitors, the assets card once signed in (a skeleton while a session may come back). */
function Top({ tickers }: { tickers: ReadonlyMap<string, TickerData> }) {
  const signedIn = useSession(selectSignedIn);
  const restoring = useSession(selectRestoring);
  if (signedIn) return <AssetCard tickers={tickers} />;
  if (restoring && mayHaveSession()) return <AssetCardSkeleton />;
  return <Welcome />;
}

function Welcome() {
  const { t } = useTranslation();
  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={0}
      className="relative overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-5"
    >
      <div aria-hidden className="pointer-events-none absolute -right-12 -top-16 size-44 animate-float rounded-full bg-brand-soft blur-2xl" />
      <div aria-hidden className="pointer-events-none absolute -bottom-24 -left-12 size-44 animate-float rounded-full bg-glow blur-2xl [animation-delay:-7s]" />
      <div className="relative">
        <Badge tone="brand" dot>
          {t("mMarkets.home.badge")}
        </Badge>
        <h2 className="mt-3 text-lg font-semibold leading-snug text-fg-1">{t("mMarkets.home.welcomeTitle")}</h2>
        <p className="mt-1.5 text-sm leading-relaxed text-fg-2">{t("mMarkets.home.welcomeDesc")}</p>
        <div className="mt-5 grid grid-cols-2 gap-3">
          <Button asChild size="lg" icon={<UserPlus size={18} />}>
            <Link to={routes.register}>{t("mMarkets.home.register")}</Link>
          </Button>
          <Button asChild size="lg" variant="secondary">
            <Link to={routes.login}>{t("mMarkets.home.login")}</Link>
          </Button>
        </div>
      </div>
    </motion.section>
  );
}

/** RollIn is a CountUp that rolls up from 0 when it first shows, then to every new value. */
function RollIn({ value, decimals }: { value: string; decimals: number }) {
  const [shown, setShown] = useState("0");
  useEffect(() => setShown(value), [value]);
  return <CountUp value={shown} decimals={decimals} />;
}

/**
 * AssetCard: the total value of both accounts at reference prices (live
 * with the balance pushes and the tickers), the eye that hides amounts
 * everywhere, and the shortcuts. There is no API for today's PnL yet.
 */
function AssetCard({ tickers }: { tickers: ReadonlyMap<string, TickerData> }) {
  const { t } = useTranslation();
  const balances = useBalances();
  const hidden = useSettings((s) => s.hideAmounts);
  const set = useSettings((s) => s.set);
  const list = balances.data?.balances;
  const portfolio = useMemo(() => valuePortfolio(list ?? [], (asset) => referencePrice(asset, tickers)), [list, tickers]);
  const total = dec.round(portfolio.total, 2, "down");
  const inBtc = convertValue(portfolio.total, referencePrice("BTC", tickers), 8);
  const empty = list !== undefined && list.every((b) => dec.sign(b.total) <= 0);

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={0}
      className="relative overflow-hidden rounded-3 border border-line-1 bg-bg-1"
    >
      <div aria-hidden className="pointer-events-none absolute -right-16 -top-20 size-48 rounded-full bg-brand-soft blur-3xl" />
      <div className="relative px-4">
        <div className="flex items-center justify-between">
          <div className="flex min-w-0 items-center text-sm text-fg-3">
            <span className="truncate">{t("mMarkets.home.totalValue")} (USDT)</span>
            <button
              type="button"
              aria-pressed={hidden}
              aria-label={hidden ? t("mMarkets.home.showAmounts") : t("mMarkets.home.hideAmounts")}
              onClick={() => set({ hideAmounts: !hidden })}
              className="grid size-11 shrink-0 place-items-center rounded-full text-fg-3 transition-colors active:bg-bg-2 active:text-fg-1"
            >
              {hidden ? <EyeOff size={16} /> : <Eye size={16} />}
            </button>
          </div>
          <Link to={routes.assets} className="-mr-2 flex h-11 shrink-0 items-center gap-0.5 px-2 text-sm text-fg-3 transition-colors active:text-fg-1">
            {t("mMarkets.home.myAssets")}
            <ChevronRight size={16} />
          </Link>
        </div>
        {balances.isPending ? (
          <div className="pb-4">
            <Skeleton className="h-9 w-48" />
            <Skeleton className="mt-2 h-4 w-28" />
          </div>
        ) : balances.isError ? (
          <ErrorState compact className="py-3" message={errorText(balances.error)} onRetry={() => void balances.refetch()} />
        ) : (
          <div className="pb-4">
            <div className="flex items-baseline gap-1.5">
              <span className="text-2xl font-semibold tracking-tight text-fg-1">{hidden ? HIDDEN_AMOUNT : <RollIn value={total} decimals={2} />}</span>
              <span className="text-sm text-fg-3">USDT</span>
            </div>
            {empty ? (
              <Link to={routes.deposit} className="-mb-2.5 inline-flex min-h-11 items-center gap-0.5 text-sm text-brand">
                {t("mMarkets.home.noBalance")}
                <ChevronRight size={14} />
              </Link>
            ) : (
              <div className="mt-0.5 h-6 text-sm leading-6 text-fg-3 tabular-nums">
                {inBtc && t("mMarkets.home.approxBtc", { value: hidden ? HIDDEN_AMOUNT : formatAmount(inBtc, 8) })}
              </div>
            )}
          </div>
        )}
      </div>
      <Shortcuts />
    </motion.section>
  );
}

function Shortcuts() {
  const { t } = useTranslation();
  const items = [
    { key: "deposit", to: routes.deposit, icon: <ArrowDownToLine size={18} />, label: t("nav.deposit") },
    { key: "withdraw", to: routes.withdraw, icon: <ArrowUpFromLine size={18} />, label: t("nav.withdraw") },
    { key: "transfer", to: routes.transfer, icon: <ArrowLeftRight size={18} />, label: t("nav.transfer") },
    { key: "trade", to: routes.trade(DEFAULT_SYMBOL), icon: <CandlestickChart size={18} />, label: t("nav.trade") },
  ];
  return (
    <nav aria-label={t("mMarkets.home.shortcuts")} className="relative grid grid-cols-4 border-t border-line-1">
      {items.map((it) => (
        <Link
          key={it.key}
          to={it.to}
          className="flex h-[72px] flex-col items-center justify-center gap-1.5 text-xs text-fg-2 transition-[transform,background-color] duration-[var(--t-fast)] active:scale-[0.97] active:bg-bg-2"
        >
          <span className="grid size-9 place-items-center rounded-full bg-brand-soft text-brand">{it.icon}</span>
          {it.label}
        </Link>
      ))}
    </nav>
  );
}

function AssetCardSkeleton() {
  return (
    <div className="rounded-3 border border-line-1 bg-bg-1">
      <div className="px-4 pb-4 pt-3">
        <Skeleton className="h-4 w-32" />
        <Skeleton className="mt-4 h-9 w-48" />
        <Skeleton className="mt-2 h-4 w-28" />
      </div>
      <div className="grid h-[72px] grid-cols-4 items-center justify-items-center border-t border-line-1">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} round className="size-9" />
        ))}
      </div>
    </div>
  );
}

/** NewsBanner: the pinned and newest announcements, one card per slide, swiped sideways (native snap scrolling). */
const NewsBanner = memo(function NewsBanner() {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const q = useArticles("announcements");
  const list = useMemo(() => (q.data ?? []).slice(0, BANNER_SLIDES), [q.data]);
  const scroller = useRef<HTMLDivElement>(null);
  const [index, setIndex] = useState(0);
  const onScroll = () => {
    const el = scroller.current;
    if (!el || el.clientWidth === 0) return;
    setIndex(Math.round(el.scrollLeft / el.clientWidth));
  };

  let body: ReactNode;
  if (q.isPending) body = <Skeleton className="h-[88px] w-full rounded-3" />;
  else if (q.isError)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />
      </div>
    );
  else if (list.length === 0)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <EmptyState
          compact
          title={t("mMarkets.home.newsEmpty")}
          action={
            <Button asChild size="lg" variant="secondary">
              <Link to={routes.help}>{t("nav.help")}</Link>
            </Button>
          }
        />
      </div>
    );
  else
    body = (
      <>
        <div
          ref={scroller}
          onScroll={onScroll}
          className="flex snap-x snap-mandatory overflow-x-auto overscroll-x-contain rounded-3 border border-line-1 bg-bg-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        >
          {list.map((a) => (
            <Link
              key={a.slug}
              to={routes.announcement(a.slug)}
              className="flex h-[88px] w-full shrink-0 snap-start items-center gap-3 px-4 transition-colors active:bg-bg-2"
            >
              <span className="grid size-10 shrink-0 place-items-center rounded-full bg-brand-soft text-brand">
                {a.pinned ? <Pin size={18} /> : <Megaphone size={18} />}
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 items-center gap-1.5 text-xs text-fg-3">
                  {a.pinned && (
                    <Badge tone="brand">
                      {t("mContent.announcements.pinned")}
                    </Badge>
                  )}
                  <Tag>{t(`mContent.categories.${a.category}`, { defaultValue: a.category })}</Tag>
                  <time dateTime={a.date} className="truncate tabular-nums">
                    {formatTime(a.date, "date", locale, "UTC")}
                  </time>
                </span>
                <span className="mt-1 block truncate text-base font-medium text-fg-1">{a.title}</span>
                <span className="mt-0.5 block truncate text-xs text-fg-3">{a.summary}</span>
              </span>
            </Link>
          ))}
        </div>
        {list.length > 1 && (
          <div aria-hidden className="mt-2 flex justify-center gap-1.5">
            {list.map((a, i) => (
              <span key={a.slug} className={cn("size-1.5 rounded-full transition-colors duration-[var(--t-base)]", i === index ? "bg-brand" : "bg-line-2")} />
            ))}
          </div>
        )}
      </>
    );

  return (
    <section aria-label={t("mMarkets.home.news")}>
      <SectionHead title={t("mMarkets.home.news")} to={routes.announcements} more={t("mMarkets.viewAll")} />
      {body}
    </section>
  );
});

/** TopCoins: the first eight coins by rank as cards that scroll sideways, each with its 24-hour line. */
function TopCoins({ rows, loading, error, onRetry, tickerOf }: Section) {
  const { t } = useTranslation();
  const list = useMemo(() => marqueeRows(rows, TOP_COINS), [rows]);
  let body: ReactNode;
  if (loading)
    body = (
      <div className="-mx-4 flex gap-3 overflow-hidden px-4">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex h-[150px] w-[148px] shrink-0 flex-col gap-3 rounded-3 border border-line-1 bg-bg-1 p-3">
            <Skeleton className="h-5 w-20" />
            <Skeleton className="h-6 w-24" />
            <Skeleton className="mt-auto h-8 w-full" />
          </div>
        ))}
      </div>
    );
  else if (error && rows.length === 0)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <ErrorState compact message={errorText(error)} onRetry={onRetry} />
      </div>
    );
  else if (list.length === 0)
    body = (
      <div className="rounded-3 border border-line-1 bg-bg-1">
        <EmptyState
          compact
          action={
            <Button asChild size="lg" variant="secondary">
              <Link to={routes.markets}>{t("nav.markets")}</Link>
            </Button>
          }
        />
      </div>
    );
  else
    body = (
      <ul className="-mx-4 flex snap-x snap-mandatory scroll-px-4 gap-3 overflow-x-auto overscroll-x-contain px-4 pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {list.map((r, i) => (
          <motion.li key={r.symbol} variants={listItem} initial="initial" animate="animate" custom={i} className="shrink-0 snap-start">
            <CoinCard row={r} ticker={tickerOf(r.symbol)} />
          </motion.li>
        ))}
      </ul>
    );
  return (
    <section aria-label={t("mMarkets.home.hot")}>
      <SectionHead title={t("mMarkets.home.hot")} to={routes.markets} more={t("mMarkets.viewAll")} />
      {body}
    </section>
  );
}

function CoinCard({ row, ticker }: { row: MarketRow; ticker: TickerData | undefined }) {
  const { t } = useTranslation();
  return (
    <Link
      to={tradePath(row)}
      className="flex h-[150px] w-[148px] flex-col rounded-3 border border-line-1 bg-bg-1 p-3 transition-transform duration-[var(--t-fast)] active:scale-[0.98]"
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <CoinIcon symbol={row.base} size={20} />
        <span className="min-w-0 truncate text-sm font-medium text-fg-1">{row.kind === "perp" ? row.base : marketLabel(row)}</span>
        {row.kind === "perp" && (
          <Tag tone="brand" className="shrink-0">
            {t("mMarkets.perp")}
          </Tag>
        )}
      </span>
      <PriceText value={ticker?.last} decimals={row.priceDecimals} className="mt-3 block truncate text-md font-semibold" />
      <span className={cn("mt-0.5 text-xs font-medium tabular-nums", TONE[toneOf(ticker?.change)])}>{formatPercent(ticker?.change)}</span>
      <DaySpark symbol={row.symbol} width={124} height={32} fluid className="mt-auto" />
    </Link>
  );
}

/** Boards: the five biggest gainers or losers among the trading USDT pairs, live. */
function Boards({ rows, loading, error, onRetry, tickerOf }: Section) {
  const { t } = useTranslation();
  const [board, setBoard] = useState<Board>("gainers");
  const lists = useMemo(() => rankOverview(rows, tickerOf, BOARD_ROWS), [rows, tickerOf]);
  const list = lists[board];

  let body: ReactNode;
  if (loading)
    body = (
      <ul>
        {Array.from({ length: BOARD_ROWS }, (_, i) => (
          <li key={i} className="flex h-14 items-center gap-3 px-4">
            <Skeleton round className="size-7" />
            <SkeletonLines lines={2} className="flex-1" />
            <Skeleton className="h-6 w-[76px]" />
          </li>
        ))}
      </ul>
    );
  else if (error && rows.length === 0) body = <ErrorState compact message={errorText(error)} onRetry={onRetry} />;
  else if (list.length === 0)
    body = (
      <EmptyState
        compact
        title={t("mMarkets.home.boardsEmpty")}
        description={t("mMarkets.home.boardsEmptyHint")}
        action={
          <Button asChild size="lg" variant="secondary">
            <Link to={routes.markets}>{t("nav.markets")}</Link>
          </Button>
        }
      />
    );
  else
    body = (
      <>
        <ol key={board} className="animate-fade-in divide-y divide-line-1">
          {list.map((r, i) => (
            <BoardRow key={r.symbol} row={r} index={i} ticker={tickerOf(r.symbol)} />
          ))}
        </ol>
        {list.length < BOARD_ROWS && <p className="border-t border-line-1 px-4 py-3 text-center text-xs text-fg-3">{t("mMarkets.home.moreSoon")}</p>}
      </>
    );

  return (
    <section aria-label={t("mMarkets.home.boards")}>
      <div className="flex items-center justify-between gap-2">
        <PillBar
          aria-label={t("mMarkets.home.boards")}
          items={[
            { value: "gainers", label: t("market.gainers") },
            { value: "losers", label: t("market.losers") },
          ]}
          value={board}
          onValueChange={(v) => setBoard(v as Board)}
          className="-ml-1"
        />
        <Link to={boardPath(board)} className="-mr-2 flex h-11 shrink-0 items-center gap-0.5 px-2 text-sm text-fg-3 transition-colors active:text-fg-1">
          {t("mMarkets.viewAll")}
          <ChevronRight size={16} />
        </Link>
      </div>
      <div className="mt-1 overflow-hidden rounded-3 border border-line-1 bg-bg-1">{body}</div>
    </section>
  );
}

function BoardRow({ row, index, ticker }: { row: MarketRow; index: number; ticker: TickerData | undefined }) {
  return (
    <li>
      <Link to={tradePath(row)} className="flex h-14 items-center gap-3 px-4 transition-colors active:bg-bg-2">
        <span className={cn("w-4 shrink-0 text-center text-sm font-semibold tabular-nums", index < 3 ? "text-brand" : "text-fg-3")}>{index + 1}</span>
        <div className="min-w-0 flex-1">
          <MarketName row={row} size={28} />
        </div>
        <PriceText value={ticker?.last} decimals={row.priceDecimals} className="shrink-0 text-base font-medium" />
        <ChangeBadge value={ticker?.change} size="md" className="w-[76px] shrink-0" />
      </Link>
    </li>
  );
}

const WHY: { id: string; icon: ReactNode }[] = [
  { id: "wallet", icon: <Network size={20} /> },
  { id: "futures", icon: <Layers size={20} /> },
  { id: "liquidity", icon: <Waves size={20} /> },
  { id: "security", icon: <ShieldCheck size={20} /> },
];

// Below the first screen and without prices: memo keeps the per-second ticker renders away.
const Why = memo(function Why({ leverage }: { leverage: number }) {
  const { t } = useTranslation();
  return (
    <section aria-label={t("mMarkets.home.whyTitle")}>
      <SectionHead title={t("mMarkets.home.whyTitle")} />
      <div className="grid grid-cols-2 gap-3">
        {WHY.map((w) => (
          <article key={w.id} className="min-w-0 rounded-3 border border-line-1 bg-bg-1 p-4">
            <span className="grid size-9 place-items-center rounded-2 bg-brand-soft text-brand">{w.icon}</span>
            <h3 className="mt-3 text-sm font-semibold text-fg-1">{t(`mMarkets.home.why.${w.id}.title`)}</h3>
            <p className="mt-1 text-xs leading-relaxed text-fg-3">{t(`mMarkets.home.why.${w.id}.desc`, { leverage })}</p>
          </article>
        ))}
      </div>
    </section>
  );
});
