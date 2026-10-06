import { dec, enumLabel, errorText, formatCompact, formatPercent, qk, routes, selectSignedIn, useSession, useSettings, type TickerData } from "@exchange/core";
import {
  futuresKeys,
  parseFuturesSort,
  parseMarginGroup,
  sortFuturesRows,
  useFuturesMarketRows,
  useOverviewOf,
  type FuturesOverviewItem,
  type FuturesSort,
  type MarginGroup,
} from "@exchange/core/futures/index";
import {
  categoryTags,
  filterRows,
  parseCategory,
  parseSort,
  sortRows,
  staleSymbols,
  tagLabel,
  useMarketTickers,
  type MarketCategory,
  type MarketRow,
  type MarketSort,
  type TickerOf,
} from "@exchange/core/markets/index";
import { Badge, Button, ChangeBadge, CoinIcon, EmptyState, ErrorState, Input, PriceText, Segmented, Sheet, Skeleton, SkeletonLines, Tag, cn, listItem, useNow } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowDownUp, Check, ChevronRight, Search, Star, TriangleAlert, X } from "lucide-react";
import { motion } from "motion/react";
import { memo, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import { FavoriteStar } from "../../components/FavoriteStar";
import { PillBar } from "../../components/PillBar";
import { PullToRefresh } from "../../components/PullToRefresh";
import { useLongPress } from "../../components/useLongPress";
import { WindowList } from "../../components/WindowList";
import { usePageHeader } from "../../layout/header";
import { SORT_OPTIONS, categoryPills } from "./logic";
import { DaySpark, MarketName, statusTone, tradePath, useFavoriteToggle } from "./parts";
import { daySparkRoot } from "./spark";

// The markets tab (design §7.2): a search box, category pills (all,
// favourites, spot, futures and the sectors) that stay under the top bar,
// a sort sheet, and every market as a row: icon, symbol and name, its
// 24-hour line, the live price and the 24-hour change. A tap opens the
// terminal, the star or a long press toggles a favourite; from 50 rows only
// the rows near the screen are rendered. Category, search and order live
// in the address with the PC site's parameters (cat, q, sort, dir).

/** A row; ov: a contract's open interest and funding in the futures category (batch F3). */
type Item = { row: MarketRow; ticker: TickerData | undefined; fav: boolean; ov?: FuturesOverviewItem };

/** An order the sort sheet offers. */
type SortChoice = { id: string; sort: FuturesSort | null };

/** The futures category's orders besides the list's (core parseFuturesSort). */
const FUTURES_SORTS: readonly SortChoice[] = [
  { id: "oiDesc", sort: { key: "oi", desc: true } },
  { id: "fundingDesc", sort: { key: "funding", desc: true } },
  { id: "fundingAsc", sort: { key: "funding", desc: false } },
];

const RATE_TONE = { 1: "text-up", 0: "text-fg-2", [-1]: "text-down" } as const;

/** Rows from which the list renders only what is near the screen. */
const VIRTUAL_FROM = 50;
const ROW_HEIGHT = 64;
/** Rows of the first screen that fade in one after another. */
const INTRO_ROWS = 12;

export default function Markets() {
  const { t } = useTranslation();
  usePageHeader({ title: t("nav.markets") }, [t]);
  const signedIn = useSession(selectSignedIn);
  const [params, setParams] = useSearchParams();
  // Contracts of both margin types, once the terminal opens the coin-margined ones (design 2026-10-06 §3.4).
  const { rows, loading, error, refetch, groupOf } = useFuturesMarketRows();
  const tickers = useMarketTickers();
  const fav = useFavoriteToggle();
  const qc = useQueryClient();
  const [now] = useState(() => Date.now());
  const [sortOpen, setSortOpen] = useState(false);

  const category = parseCategory(params.get("cat"));
  const sortKey = params.get("sort");
  const sortDir = params.get("dir");
  // The futures category: its two groups (USDⓈ-M, COIN-M), its open interest and funding, and their orders.
  const futures = category === "futures";
  const sort = useMemo<FuturesSort | null>(() => (futures ? parseFuturesSort(sortKey, sortDir) : parseSort(sortKey, sortDir)), [futures, sortKey, sortDir]);
  const group = parseMarginGroup(params.get("margin"));
  const overviewOf = useOverviewOf(futures);
  const [query, setQuery] = useState(() => params.get("q") ?? "");

  const setParam = useCallback(
    (patch: Record<string, string | null>) =>
      setParams(
        (p) => {
          const next = new URLSearchParams(p);
          for (const [k, v] of Object.entries(patch)) {
            if (v) next.set(k, v);
            else next.delete(k);
          }
          return next;
        },
        { replace: true },
      ),
    [setParams],
  );
  const changeQuery = (v: string) => {
    setQuery(v);
    setParam({ q: v.trim() || null });
  };
  const changeCategory = (c: string) => setParam({ cat: c === "all" ? null : c });
  const pickSort = (s: FuturesSort | null) => {
    setParam(s ? { sort: s.key, dir: s.desc ? "desc" : "asc" } : { sort: null, dir: null });
    setSortOpen(false);
  };

  const favorites = useMemo(() => new Set(fav.symbols), [fav.symbols]);
  const tickerOf: TickerOf = useCallback((s) => tickers.get(s), [tickers]);
  const hasCoin = useMemo(() => rows.some((r) => r.kind === "perp" && groupOf(r.symbol) === "coin"), [rows, groupOf]);
  const filtered = useMemo(() => {
    const list = filterRows(rows, { category, query, favorites, now });
    return futures && hasCoin ? list.filter((r) => groupOf(r.symbol) === group) : list;
  }, [rows, category, query, favorites, now, futures, hasCoin, group, groupOf]);
  // New listings read newest first unless an order is chosen.
  const order = useMemo<FuturesSort | null>(() => sort ?? (category === "new" ? { key: "listed", desc: true } : null), [sort, category]);
  const sorted = useMemo(
    () => (futures ? sortFuturesRows(filtered, tickerOf, overviewOf, order) : sortRows(filtered, tickerOf, order as MarketSort | null)),
    [futures, filtered, tickerOf, overviewOf, order],
  );
  const items = useMemo<Item[]>(
    () => sorted.map((row) => ({ row, ticker: tickers.get(row.symbol), fav: favorites.has(row.symbol), ov: futures ? overviewOf(row.symbol) : undefined })),
    [sorted, tickers, favorites, futures, overviewOf],
  );

  const tags = useMemo(() => categoryTags(rows).map((x) => x.tag), [rows]);
  const pills = useMemo(() => {
    const label = (c: MarketCategory): string => {
      switch (c) {
        case "all":
          return t("common.all");
        case "favorites":
          return t("market.favorites");
        case "spot":
          return t("market.spot");
        case "futures":
          return t("market.futures");
        case "new":
          return t("market.newListings");
        default: {
          const tag = c.slice(4);
          return t(`mMarkets.tags.${tag}`, { defaultValue: tagLabel(tag) });
        }
      }
    };
    return categoryPills(category, tags).map((c) => ({ value: c, label: label(c), icon: c === "favorites" ? <Star size={13} /> : undefined }));
  }, [category, tags, t]);

  const refresh = useCallback(
    () =>
      Promise.all([
        qc.refetchQueries({ queryKey: qk.pairs }),
        qc.refetchQueries({ queryKey: qk.contracts }),
        qc.refetchQueries({ queryKey: qk.tickers }),
        qc.refetchQueries({ queryKey: futuresKeys.overview }),
        qc.refetchQueries({ queryKey: qk.favorites }),
        qc.invalidateQueries({ queryKey: daySparkRoot }),
      ]),
    [qc],
  );

  const { toggle } = fav;
  const onStar = useCallback((symbol: string) => toggle(symbol), [toggle]);
  // A long press shows no star animation under the finger: a short buzz and a toast say it worked.
  const onPress = useCallback(
    (symbol: string) => {
      navigator.vibrate?.(10);
      toggle(symbol, true);
    },
    [toggle],
  );

  // The first screen's rows fade in one after another; rows that show later (scrolling, filters) do not.
  const [intro, setIntro] = useState(true);
  useEffect(() => {
    if (loading) return;
    const id = setTimeout(() => setIntro(false), 600);
    return () => clearTimeout(id);
  }, [loading]);

  const searching = query.trim() !== "";
  const sortOptions: readonly SortChoice[] = futures ? [...SORT_OPTIONS, ...FUTURES_SORTS] : SORT_OPTIONS;
  const sortId = !sort ? "default" : (sortOptions.find((o) => o.sort && o.sort.key === sort.key && o.sort.desc === sort.desc)?.id ?? null);
  const sortLabel = (id: string) => (FUTURES_SORTS.some((o) => o.id === id) ? t(`mFutures.list.sorts.${id}`) : t(`mMarkets.markets.sorts.${id}`));
  const browseAll = (
    <Button size="lg" variant="secondary" onClick={() => changeCategory("all")}>
      {t("mMarkets.markets.browseAll")}
    </Button>
  );
  let content: ReactNode;
  if (loading) content = <RowsSkeleton />;
  else if (error && rows.length === 0) content = <ErrorState message={errorText(error)} onRetry={refetch} />;
  else if (items.length === 0)
    content = searching ? (
      <EmptyState
        title={t("market.noResults")}
        description={t("mMarkets.markets.emptySearchHint")}
        action={
          <Button size="lg" variant="secondary" onClick={() => changeQuery("")}>
            {t("mMarkets.markets.clearSearch")}
          </Button>
        }
      />
    ) : category === "favorites" ? (
      <EmptyState
        title={t("mMarkets.markets.emptyFavorites")}
        description={signedIn ? t("mMarkets.markets.emptyFavoritesHint") : t("mMarkets.markets.emptyFavoritesVisitor")}
        action={browseAll}
      />
    ) : (
      <EmptyState title={t("mMarkets.markets.emptyCategory")} action={browseAll} />
    );
  else if (items.length >= VIRTUAL_FROM)
    content = (
      <WindowList
        aria-label={t("nav.markets")}
        items={items}
        rowHeight={ROW_HEIGHT}
        getKey={(it) => it.row.symbol}
        renderRow={(it) => <MarketLine row={it.row} ticker={it.ticker} fav={it.fav} ov={it.ov} futures={futures} onStar={onStar} onPress={onPress} />}
      />
    );
  else
    content = (
      <ul aria-label={t("nav.markets")}>
        {items.map((it, i) => (
          <motion.li
            key={it.row.symbol}
            variants={listItem}
            initial={intro && i < INTRO_ROWS ? "initial" : false}
            animate="animate"
            custom={i}
            style={{ height: ROW_HEIGHT }}
          >
            <MarketLine row={it.row} ticker={it.ticker} fav={it.fav} ov={it.ov} futures={futures} onStar={onStar} onPress={onPress} />
          </motion.li>
        ))}
      </ul>
    );

  // The sort sheet sits outside the pull-to-refresh area: React events
  // bubble out of its portal, and dragging it down must not pull the page.
  return (
    <>
      <PullToRefresh onRefresh={refresh}>
        <div className="pb-6">
          <h1 className="sr-only">{t("nav.markets")}</h1>
          <div className="px-4 pt-2">
            <Input
              size="lg"
              value={query}
              onValueChange={changeQuery}
              onKeyDown={(e) => {
                if (e.key === "Enter") e.currentTarget.blur();
              }}
              inputMode="search"
              enterKeyHint="search"
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              placeholder={t("mMarkets.markets.search")}
              aria-label={t("mMarkets.markets.search")}
              prefix={<Search size={18} className="text-fg-3" />}
              suffix={
                query ? (
                  <button type="button" aria-label={t("ui.clear")} onClick={() => changeQuery("")} className="grid size-tap place-items-center rounded-2 text-fg-3 active:text-fg-1">
                    <X size={16} />
                  </button>
                ) : undefined
              }
            />
          </div>

          <div className="sticky top-[calc(44px+env(safe-area-inset-top))] z-[var(--z-sticky)] mt-1 flex items-center border-b border-line-1 bg-bg-0/95 backdrop-blur">
            {loading ? (
              <div className="flex h-tap min-w-0 flex-1 items-center gap-2 overflow-hidden px-4">
                {[0, 1, 2, 3].map((i) => (
                  <Skeleton key={i} className="h-8 w-14 shrink-0 rounded-full" />
                ))}
              </div>
            ) : (
              <PillBar aria-label={t("mMarkets.markets.categories")} items={pills} value={category} onValueChange={changeCategory} className="min-w-0 flex-1 px-2" />
            )}
            <button
              type="button"
              aria-haspopup="dialog"
              aria-label={t("mMarkets.markets.sort")}
              onClick={() => setSortOpen(true)}
              className={cn("grid h-tap w-12 shrink-0 place-items-center border-l border-line-1 transition-colors", sort ? "text-brand" : "text-fg-2 active:text-fg-1")}
            >
              <ArrowDownUp size={18} />
            </button>
          </div>

          <StaleNotice rows={rows} tickerOf={tickerOf} />

          {futures && (
            <div className="flex items-center gap-3 px-4 pt-2">
              {hasCoin && (
                <Segmented
                  size="md"
                  className="min-w-0 flex-1"
                  block
                  aria-label={t("market.futures")}
                  value={group}
                  onValueChange={(v) => setParam({ margin: (v as MarginGroup) === "coin" ? "coin" : null })}
                  items={[
                    { value: "usdt", label: t("mFutures.overview.groups.usdt") },
                    { value: "coin", label: t("mFutures.overview.groups.coin") },
                  ]}
                />
              )}
              <Link to={routes.futuresData} className="ml-auto flex min-h-tap shrink-0 items-center gap-0.5 text-sm text-fg-3 active:text-brand">
                {t("mFutures.allData")}
                <ChevronRight size={16} />
              </Link>
            </div>
          )}

          <div className="flex h-9 items-center justify-between pl-11 pr-4 text-xs text-fg-3">
            <span className="truncate">
              {sortId && sortId !== "default" ? <span className="text-brand">{sortLabel(sortId)}</span> : t("mMarkets.markets.name")}
              {!loading && ` · ${t("mMarkets.pairs", { count: items.length })}`}
            </span>
            <span className="shrink-0">{t("mMarkets.markets.priceChange")}</span>
          </div>

          {content}
        </div>
      </PullToRefresh>
      <SortSheet open={sortOpen} onOpenChange={setSortOpen} options={sortOptions} label={sortLabel} current={sortId} onPick={pickSort} />
    </>
  );
}

type LineProps = {
  row: MarketRow;
  ticker: TickerData | undefined;
  fav: boolean;
  /** The futures category: the line under the name has the funding rate and open interest (ov) instead of the coin's name. */
  futures?: boolean;
  ov?: FuturesOverviewItem;
  onStar: (symbol: string) => void;
  onPress: (symbol: string) => void;
};

/** MarketLine is one market: the star, then a link to its terminal that also takes a long press. */
const MarketLine = memo(function MarketLine({ row, ticker, fav, futures, ov, onStar, onPress }: LineProps) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const longPress = useLongPress(() => onPress(row.symbol));
  return (
    <div className="flex h-16 items-stretch">
      <FavoriteStar
        active={fav}
        onToggle={() => onStar(row.symbol)}
        label={fav ? t("mMarkets.favoriteRemove") : t("mMarkets.favoriteAdd")}
        className="h-16 w-tap"
      />
      <Link
        to={tradePath(row)}
        {...longPress}
        className="flex min-w-0 flex-1 select-none items-center gap-2 pr-4 transition-colors duration-[var(--t-fast)] active:bg-bg-1 [-webkit-touch-callout:none]"
      >
        <div className="min-w-0 flex-1">{futures ? <FuturesName row={row} ov={ov} locale={locale} /> : <MarketName row={row} size={28} />}</div>
        {!futures && <DaySpark symbol={row.symbol} width={48} height={24} />}
        <div className="flex w-[88px] shrink-0 flex-col items-end gap-1">
          <PriceText value={ticker?.last} decimals={row.priceDecimals} className="max-w-full truncate text-base font-medium" />
          <ChangeBadge value={ticker?.change} />
        </div>
      </Link>
    </div>
  );
});

/**
 * FuturesName is a contract in the futures category: its icon, symbol,
 * perpetual tag and status, and under them its funding rate and open
 * interest instead of the coin's name.
 */
function FuturesName({ row, ov, locale }: { row: MarketRow; ov: FuturesOverviewItem | undefined; locale: string }) {
  const { t } = useTranslation();
  const code = `${row.base}${row.quote}`;
  const rate = ov?.funding_rate;
  const sign = rate && dec.isDecimal(rate) ? dec.sign(rate) : 0;
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <CoinIcon symbol={row.base} size={28} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1 overflow-hidden whitespace-nowrap">
          <span className="min-w-0 truncate font-medium text-fg-1" title={code}>
            {code}
          </span>
          <Tag tone="brand">{t("mMarkets.perp")}</Tag>
          {row.status !== "TRADING" && <Badge tone={statusTone(row.status)}>{enumLabel(row.status)}</Badge>}
        </div>
        <div className="truncate text-xs text-fg-3">
          {t("mFutures.list.rateShort")} <span className={cn("tabular-nums", RATE_TONE[sign])}>{formatPercent(rate, 4)}</span>
          {ov?.open_interest_value && (
            <>
              {" · "}
              {t("mFutures.list.oiShort")} <span className="tabular-nums text-fg-2">{formatCompact(ov.open_interest_value, locale)}</span>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function RowsSkeleton() {
  return (
    <ul aria-hidden>
      {Array.from({ length: 8 }, (_, i) => (
        <li key={i} className="flex h-16 items-center gap-2 pl-11 pr-4">
          <Skeleton round className="size-7 shrink-0" />
          <SkeletonLines lines={2} className="min-w-0 flex-1 pl-1" />
          <Skeleton className="h-6 w-12 shrink-0" />
          <div className="flex w-[88px] shrink-0 flex-col items-end gap-1.5">
            <Skeleton className="h-4 w-16" />
            <Skeleton className="h-5 w-16" />
          </div>
        </li>
      ))}
    </ul>
  );
}

/** SortSheet offers the orders of the list in a bottom sheet (design §7.1: choices go in drawers). */
function SortSheet({
  open, onOpenChange, options, label, current, onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  options: readonly SortChoice[];
  label: (id: string) => string;
  current: string | null;
  onPick: (s: FuturesSort | null) => void;
}) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={t("mMarkets.markets.sortTitle")}>
      <div role="radiogroup" aria-label={t("mMarkets.markets.sortTitle")} className="flex flex-col">
        {options.map((o) => {
          const on = current === o.id;
          return (
            <button
              key={o.id}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => onPick(o.sort)}
              className={cn(
                "flex h-tap items-center justify-between gap-3 rounded-2 px-3 text-left text-base transition-colors active:bg-bg-2",
                on ? "font-medium text-brand" : "text-fg-1",
              )}
            >
              {label(o.id)}
              {on && <Check size={18} className="shrink-0" />}
            </button>
          );
        })}
      </div>
    </Sheet>
  );
}

/**
 * StaleNotice warns when the reference prices stop moving for 30 seconds
 * (the feed stalled); it re-checks every 5 seconds on its own.
 */
function StaleNotice({ rows, tickerOf }: { rows: MarketRow[]; tickerOf: TickerOf }) {
  const { t } = useTranslation();
  const now = useNow(5_000);
  if (staleSymbols(rows, tickerOf, now).length === 0) return null;
  return (
    <div role="status" className="mx-4 mt-2 flex items-center gap-2 rounded-2 border border-warn/40 bg-warn/10 px-3 py-2 text-xs text-warn">
      <TriangleAlert size={14} className="shrink-0" />
      {t("common.feedStale")}
    </div>
  );
}
