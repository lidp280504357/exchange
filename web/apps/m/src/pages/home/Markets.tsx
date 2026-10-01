import { errorText, qk, selectSignedIn, useSession, type TickerData } from "@exchange/core";
import {
  categoryTags,
  filterRows,
  parseCategory,
  parseSort,
  sortRows,
  staleSymbols,
  tagLabel,
  useMarketRows,
  useMarketTickers,
  type MarketCategory,
  type MarketRow,
  type MarketSort,
  type TickerOf,
} from "@exchange/core/markets/index";
import { Button, ChangeBadge, EmptyState, ErrorState, Input, PriceText, Sheet, Skeleton, SkeletonLines, cn, listItem, useNow } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowDownUp, Check, Search, Star, TriangleAlert, X } from "lucide-react";
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
import { SORT_OPTIONS, categoryPills, sortOptionOf, sortParams } from "./logic";
import { DaySpark, MarketName, tradePath, useFavoriteToggle } from "./parts";
import { daySparkRoot } from "./spark";

// The markets tab (design §7.2): a search box, category pills (all,
// favourites, spot, futures and the sectors) that stay under the top bar,
// a sort sheet, and every market as a row: icon, symbol and name, its
// 24-hour line, the live price and the 24-hour change. A tap opens the
// terminal, the star or a long press toggles a favourite; from 50 rows only
// the rows near the screen are rendered. Category, search and order live
// in the address with the PC site's parameters (cat, q, sort, dir).

type Item = { row: MarketRow; ticker: TickerData | undefined; fav: boolean };

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
  const { rows, loading, error, refetch } = useMarketRows();
  const tickers = useMarketTickers();
  const fav = useFavoriteToggle();
  const qc = useQueryClient();
  const [now] = useState(() => Date.now());
  const [sortOpen, setSortOpen] = useState(false);

  const category = parseCategory(params.get("cat"));
  const sortKey = params.get("sort");
  const sortDir = params.get("dir");
  const sort = useMemo(() => parseSort(sortKey, sortDir), [sortKey, sortDir]);
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
  const pickSort = (s: MarketSort | null) => {
    setParam(sortParams(s));
    setSortOpen(false);
  };

  const favorites = useMemo(() => new Set(fav.symbols), [fav.symbols]);
  const tickerOf: TickerOf = useCallback((s) => tickers.get(s), [tickers]);
  const filtered = useMemo(() => filterRows(rows, { category, query, favorites, now }), [rows, category, query, favorites, now]);
  // New listings read newest first unless an order is chosen.
  const order = useMemo<MarketSort | null>(() => sort ?? (category === "new" ? { key: "listed", desc: true } : null), [sort, category]);
  const sorted = useMemo(() => sortRows(filtered, tickerOf, order), [filtered, tickerOf, order]);
  const items = useMemo<Item[]>(() => sorted.map((row) => ({ row, ticker: tickers.get(row.symbol), fav: favorites.has(row.symbol) })), [sorted, tickers, favorites]);

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
  const sortId = sortOptionOf(sort);
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
        renderRow={(it) => <MarketLine row={it.row} ticker={it.ticker} fav={it.fav} onStar={onStar} onPress={onPress} />}
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
            <MarketLine row={it.row} ticker={it.ticker} fav={it.fav} onStar={onStar} onPress={onPress} />
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
                  <button type="button" aria-label={t("ui.clear")} onClick={() => changeQuery("")} className="grid size-11 place-items-center rounded-2 text-fg-3 active:text-fg-1">
                    <X size={16} />
                  </button>
                ) : undefined
              }
            />
          </div>

          <div className="sticky top-[calc(44px+env(safe-area-inset-top))] z-[var(--z-sticky)] mt-1 flex items-center border-b border-line-1 bg-bg-0/95 backdrop-blur">
            {loading ? (
              <div className="flex h-11 min-w-0 flex-1 items-center gap-2 overflow-hidden px-4">
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
              className={cn("grid h-11 w-12 shrink-0 place-items-center border-l border-line-1 transition-colors", sort ? "text-brand" : "text-fg-2 active:text-fg-1")}
            >
              <ArrowDownUp size={18} />
            </button>
          </div>

          <StaleNotice rows={rows} tickerOf={tickerOf} />

          <div className="flex h-9 items-center justify-between pl-11 pr-4 text-xs text-fg-3">
            <span className="truncate">
              {sortId && sortId !== "default" ? <span className="text-brand">{t(`mMarkets.markets.sorts.${sortId}`)}</span> : t("mMarkets.markets.name")}
              {!loading && ` · ${t("mMarkets.pairs", { count: items.length })}`}
            </span>
            <span className="shrink-0">{t("mMarkets.markets.priceChange")}</span>
          </div>

          {content}
        </div>
      </PullToRefresh>
      <SortSheet open={sortOpen} onOpenChange={setSortOpen} current={sortId} onPick={pickSort} />
    </>
  );
}

type LineProps = {
  row: MarketRow;
  ticker: TickerData | undefined;
  fav: boolean;
  onStar: (symbol: string) => void;
  onPress: (symbol: string) => void;
};

/** MarketLine is one market: the star, then a link to its terminal that also takes a long press. */
const MarketLine = memo(function MarketLine({ row, ticker, fav, onStar, onPress }: LineProps) {
  const { t } = useTranslation();
  const longPress = useLongPress(() => onPress(row.symbol));
  return (
    <div className="flex h-16 items-stretch">
      <FavoriteStar
        active={fav}
        onToggle={() => onStar(row.symbol)}
        label={fav ? t("mMarkets.favoriteRemove") : t("mMarkets.favoriteAdd")}
        className="h-16 w-11"
      />
      <Link
        to={tradePath(row)}
        {...longPress}
        className="flex min-w-0 flex-1 select-none items-center gap-2 pr-4 transition-colors duration-[var(--t-fast)] active:bg-bg-1 [-webkit-touch-callout:none]"
      >
        <div className="min-w-0 flex-1">
          <MarketName row={row} size={28} />
        </div>
        <DaySpark symbol={row.symbol} width={48} height={24} />
        <div className="flex w-[88px] shrink-0 flex-col items-end gap-1">
          <PriceText value={ticker?.last} decimals={row.priceDecimals} className="max-w-full truncate text-base font-medium" />
          <ChangeBadge value={ticker?.change} />
        </div>
      </Link>
    </div>
  );
});

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
function SortSheet({ open, onOpenChange, current, onPick }: { open: boolean; onOpenChange: (open: boolean) => void; current: string | null; onPick: (s: MarketSort | null) => void }) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={t("mMarkets.markets.sortTitle")}>
      <div role="radiogroup" aria-label={t("mMarkets.markets.sortTitle")} className="flex flex-col">
        {SORT_OPTIONS.map((o) => {
          const on = current === o.id;
          return (
            <button
              key={o.id}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => onPick(o.sort)}
              className={cn(
                "flex h-12 items-center justify-between gap-3 rounded-2 px-3 text-left text-base transition-colors active:bg-bg-2",
                on ? "font-medium text-brand" : "text-fg-1",
              )}
            >
              {t(`mMarkets.markets.sorts.${o.id}`)}
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
