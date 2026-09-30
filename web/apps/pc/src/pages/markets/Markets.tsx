import { formatCompact, formatPercent, formatPrice, routes, selectSignedIn, useSession, useSettings, type TickerData } from "@exchange/core";
import {
  categoryCount,
  categoryTags,
  filterRows,
  parseCategory,
  parseSort,
  rankHighlights,
  sortRows,
  staleSymbols,
  tagLabel,
  useMarketRows,
  useMarketTickers,
  type MarketCategory,
  type MarketRow,
  type MarketSort,
  type SortKey,
  type TickerOf,
} from "@exchange/core/markets/index";
import {
  Button,
  ChangeBadge,
  DataTable,
  EmptyState,
  Input,
  PriceText,
  Skeleton,
  cn,
  createColumnHelper,
  listItem,
  toneOf,
  useNow,
  type ColumnDef,
  type SortingState,
} from "@exchange/ui";
import { ChevronRight, Flame, Hash, Info, Layers, LayoutGrid, Search, Sparkles, Star, TrendingDown, TrendingUp, TriangleAlert, Wallet } from "lucide-react";
import { motion } from "motion/react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router";
import { FavoriteStar } from "../../features/markets/FavoriteStar";
import { isTyping, useMediaQuery, usePageTitle } from "./hooks";
import { MarketName, SparkCell, tradePath, useFavoriteToggle } from "./parts";

// /markets (design §6.2): a category rail (all, favourites, spot, futures,
// new, sectors), a search box ("/" focuses it), four small boards and the
// table of every market with live prices from the one tickers channel,
// sortable headers, 7-day lines loaded as rows scroll into view, and
// virtual scrolling from 50 rows. Category, search and sort live in the
// URL, so links and the back button keep them.

type Item = { row: MarketRow; t: TickerData | undefined; fav: boolean };

/** Rows from which the table scrolls virtually. */
const VIRTUAL_FROM = 50;

const SORT_COLUMN: Partial<Record<SortKey, string>> = { symbol: "coin", last: "last", change: "change", high: "highLow", turnover: "turnover" };
const COLUMN_SORT: Record<string, SortKey> = { coin: "symbol", last: "last", change: "change", highLow: "high", turnover: "turnover" };

export default function Markets() {
  const { t } = useTranslation();
  usePageTitle(t("nav.markets"));
  const navigate = useNavigate();
  const locale = useSettings((s) => s.locale);
  const signedIn = useSession(selectSignedIn);
  const wide = useMediaQuery("(min-width: 1280px)");
  const [params, setParams] = useSearchParams();
  const { rows, loading, error, refetch } = useMarketRows();
  const tickers = useMarketTickers();
  const fav = useFavoriteToggle();
  const [now] = useState(() => Date.now());

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
  const changeCategory = (c: MarketCategory) => setParam({ cat: c === "all" ? null : c });
  // A board title shows the whole table in that board's order.
  const pickBoard = (id: BoardId) =>
    setParam(
      id === "newest"
        ? { cat: "new", sort: null, dir: null }
        : { cat: null, sort: id === "hot" ? "turnover" : "change", dir: id === "losers" ? "asc" : "desc" },
    );

  // "/" focuses the search box from anywhere on the page.
  const searchRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey || isTyping(e.target)) return;
      e.preventDefault();
      searchRef.current?.focus();
      searchRef.current?.select();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const favorites = useMemo(() => new Set(fav.symbols), [fav.symbols]);
  const tickerOf: TickerOf = useCallback((s) => tickers.get(s), [tickers]);
  const filtered = useMemo(() => filterRows(rows, { category, query, favorites, now }), [rows, category, query, favorites, now]);
  // New listings read newest first unless a column is chosen.
  const order = useMemo<MarketSort | null>(() => sort ?? (category === "new" ? { key: "listed", desc: true } : null), [sort, category]);
  const sorted = useMemo(() => sortRows(filtered, tickerOf, order), [filtered, tickerOf, order]);
  const data = useMemo<Item[]>(() => sorted.map((row) => ({ row, t: tickers.get(row.symbol), fav: favorites.has(row.symbol) })), [sorted, tickers, favorites]);

  const sorting: SortingState = sort && SORT_COLUMN[sort.key] ? [{ id: SORT_COLUMN[sort.key]!, desc: sort.desc }] : [];
  const onSortingChange = (next: SortingState) => {
    const s = next[0];
    const key = s ? COLUMN_SORT[s.id] : undefined;
    setParam(key ? { sort: key, dir: s!.desc ? "desc" : "asc" } : { sort: null, dir: null });
  };

  const toggle = fav.toggle;
  const columns = useMemo(() => {
    const col = createColumnHelper<Item>();
    const list: ColumnDef<Item, any>[] = [
      col.display({
        id: "star",
        header: () => <span className="sr-only">{t("common.favorite")}</span>,
        cell: ({ row }) => (
          <FavoriteStar
            active={row.original.fav}
            onToggle={() => toggle(row.original.row.symbol)}
            label={row.original.fav ? t("pcMarkets.favoriteRemove") : t("pcMarkets.favoriteAdd")}
          />
        ),
        meta: { width: 44, className: "pr-0" },
      }),
      col.accessor((it) => it.row.symbol, {
        id: "coin",
        header: t("pcMarkets.markets.coin"),
        sortDescFirst: false,
        cell: ({ row }) => <MarketName row={row.original.row} />,
      }),
      col.accessor((it) => it.t?.last ?? null, {
        id: "last",
        header: t("market.last"),
        sortDescFirst: true,
        cell: ({ row }) => <PriceText value={row.original.t?.last} decimals={row.original.row.priceDecimals} className="font-medium" />,
        meta: { align: "right", width: wide ? 150 : 124 },
      }),
      col.accessor((it) => it.t?.change ?? null, {
        id: "change",
        header: t("market.change"),
        sortDescFirst: true,
        cell: ({ row }) => <ChangeBadge value={row.original.t?.change} />,
        meta: { align: "right", width: wide ? 116 : 104 },
      }),
    ];
    if (wide) {
      list.push(
        col.accessor((it) => it.t?.high ?? null, {
          id: "highLow",
          header: t("pcMarkets.markets.highLow"),
          sortDescFirst: true,
          cell: ({ row }) => (
            <div className="flex flex-col items-end leading-tight">
              <span className="text-fg-1">{formatPrice(row.original.t?.high, row.original.row.priceDecimals)}</span>
              <span className="text-xs text-fg-3">{formatPrice(row.original.t?.low, row.original.row.priceDecimals)}</span>
            </div>
          ),
          meta: { align: "right", width: 150 },
        }),
      );
    }
    list.push(
      col.accessor((it) => it.t?.quote_volume ?? null, {
        id: "turnover",
        header: t("market.turnover"),
        sortDescFirst: true,
        cell: ({ row }) => (
          <span className="text-fg-2">
            {formatCompact(row.original.t?.quote_volume, locale)}
            <span className="ml-1 text-xs text-fg-3">{row.original.row.quote}</span>
          </span>
        ),
        meta: { align: "right", width: wide ? 136 : 112 },
      }),
      col.display({
        id: "trend",
        header: t("pcMarkets.markets.trend"),
        cell: ({ row }) => <SparkCell symbol={row.original.row.symbol} width={wide ? 104 : 88} height={30} className="ml-auto" />,
        meta: { align: "right", width: wide ? 128 : 108 },
      }),
      col.display({
        id: "action",
        header: () => <span className="sr-only">{t("common.action")}</span>,
        cell: ({ row }) => {
          const r = row.original.row;
          return (
            <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
              {wide && (
                <Link
                  to={routes.coin(r.base)}
                  aria-label={`${t("pcMarkets.details")} ${r.base}`}
                  title={t("pcMarkets.details")}
                  className="grid size-8 place-items-center rounded-1 text-fg-3 transition-colors hover:bg-bg-3 hover:text-fg-1"
                >
                  <Info size={16} />
                </Link>
              )}
              <Button asChild size="sm" variant="secondary">
                <Link to={tradePath(r)}>{t("pcMarkets.trade")}</Link>
              </Button>
            </div>
          );
        },
        meta: { align: "right", width: wide ? 116 : 84 },
      }),
    );
    return list;
  }, [t, wide, locale, toggle]);

  const tags = useMemo(() => categoryTags(rows), [rows]);
  const virtual = data.length >= VIRTUAL_FROM;
  const searching = query.trim() !== "";

  const empty = searching ? (
    <EmptyState
      compact
      title={t("market.noResults")}
      description={t("pcMarkets.markets.emptySearchHint")}
      action={
        <Button size="sm" variant="secondary" onClick={() => changeQuery("")}>
          {t("pcMarkets.markets.clearSearch")}
        </Button>
      }
    />
  ) : category === "favorites" ? (
    <EmptyState
      compact
      title={t("pcMarkets.markets.emptyFavorites")}
      description={signedIn ? t("pcMarkets.markets.emptyFavoritesHint") : t("pcMarkets.markets.emptyFavoritesVisitor")}
      action={
        <Button size="sm" variant="secondary" onClick={() => changeCategory("all")}>
          {t("pcMarkets.markets.browseAll")}
        </Button>
      }
    />
  ) : (
    <EmptyState
      compact
      title={t("pcMarkets.markets.emptyCategory")}
      action={
        <Button size="sm" variant="secondary" onClick={() => changeCategory("all")}>
          {t("pcMarkets.markets.browseAll")}
        </Button>
      }
    />
  );

  return (
    <div className="mx-auto max-w-[1440px] px-6 py-8">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-fg-1">{t("nav.markets")}</h1>
          <p className="mt-1 text-sm text-fg-3">{t("pcMarkets.markets.subtitle")}</p>
        </div>
        {!loading && rows.length > 0 && <span className="text-sm text-fg-3">{t("pcMarkets.pairs", { count: rows.length })}</span>}
      </header>

      <Highlights rows={rows} tickerOf={tickerOf} now={now} loading={loading} onPick={pickBoard} />
      <StaleNotice rows={rows} tickerOf={tickerOf} />

      <div className="mt-6 grid grid-cols-[168px_minmax(0,1fr)] gap-5 xl:grid-cols-[200px_minmax(0,1fr)] xl:gap-6">
        <CategoryRail
          rows={rows}
          loading={loading}
          category={category}
          onChange={changeCategory}
          favorites={favorites}
          now={now}
          tags={tags.map((x) => x.tag)}
        />

        <section className="min-w-0 overflow-hidden rounded-3 border border-line-1 bg-bg-1">
          <div className="flex items-center gap-3 border-b border-line-1 px-4 py-3">
            <Input
              ref={searchRef}
              value={query}
              onValueChange={changeQuery}
              clearable
              onClear={() => changeQuery("")}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  if (query) changeQuery("");
                  else e.currentTarget.blur();
                }
              }}
              placeholder={t("pcMarkets.markets.search")}
              aria-label={t("pcMarkets.markets.search")}
              prefix={<Search size={16} className="text-fg-3" />}
              suffix={
                <kbd title={t("pcMarkets.markets.searchShortcut")} className="rounded-1 border border-line-2 px-1.5 text-xs text-fg-3">
                  /
                </kbd>
              }
              size="md"
              containerClassName="max-w-sm"
            />
            <span className="ml-auto text-xs text-fg-3 tabular-nums">{!loading && t("pcMarkets.pairs", { count: data.length })}</span>
          </div>
          <DataTable
            aria-label={t("nav.markets")}
            columns={columns}
            data={data}
            getRowId={(it) => it.row.symbol}
            loading={loading}
            loadingRows={6}
            error={error}
            onRetry={refetch}
            empty={empty}
            sorting={sorting}
            onSortingChange={onSortingChange}
            manualSorting
            onRowClick={(it) => navigate(tradePath(it.row))}
            virtual={virtual}
            height={virtual ? "min(760px, calc(100dvh - 180px))" : undefined}
            stickyHeader={virtual}
          />
        </section>
      </div>
    </div>
  );
}

type RailProps = {
  rows: MarketRow[];
  loading: boolean;
  category: MarketCategory;
  onChange: (c: MarketCategory) => void;
  favorites: ReadonlySet<string>;
  now: number;
  tags: string[];
};

/** CategoryRail: the fixed categories, then the sector tags, each with its count. */
function CategoryRail({ rows, loading, category, onChange, favorites, now, tags }: RailProps) {
  const { t } = useTranslation();
  const fixed: { id: MarketCategory; label: string; icon: ReactNode }[] = [
    { id: "all", label: t("common.all"), icon: <LayoutGrid size={16} /> },
    { id: "favorites", label: t("market.favorites"), icon: <Star size={16} /> },
    { id: "spot", label: t("market.spot"), icon: <Wallet size={16} /> },
    { id: "futures", label: t("market.futures"), icon: <Layers size={16} /> },
    { id: "new", label: t("market.newListings"), icon: <Sparkles size={16} /> },
  ];
  const item = (id: MarketCategory, label: string, icon: ReactNode) => {
    const active = category === id;
    return (
      <button
        key={id}
        type="button"
        aria-pressed={active}
        onClick={() => onChange(id)}
        className={cn(
          "relative flex h-9 w-full items-center gap-2.5 rounded-2 px-3 text-left text-sm transition-colors duration-[var(--t-fast)]",
          active ? "text-fg-1" : "text-fg-2 hover:bg-bg-1 hover:text-fg-1",
        )}
      >
        {active && (
          <motion.span
            layoutId="markets-rail-active"
            aria-hidden
            className="absolute inset-0 -z-0 rounded-2 bg-bg-2"
            transition={{ type: "spring", stiffness: 520, damping: 42 }}
          />
        )}
        {active && <span aria-hidden className="absolute left-0 top-2 bottom-2 z-10 w-0.5 rounded-full bg-brand" />}
        <span className={cn("relative z-10 shrink-0", active ? "text-brand" : "text-fg-3")}>{icon}</span>
        <span className="relative z-10 min-w-0 flex-1 truncate">{label}</span>
        <span className="relative z-10 text-xs text-fg-3 tabular-nums">{loading ? "" : categoryCount(rows, id, favorites, now)}</span>
      </button>
    );
  };
  return (
    <nav aria-label={t("pcMarkets.markets.categories")} className="sticky top-20 flex h-max flex-col gap-0.5 self-start">
      {fixed.map((c) => item(c.id, c.label, c.icon))}
      {loading ? (
        <div className="mt-4 flex flex-col gap-2 px-3">
          <Skeleton className="h-3 w-12" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-4 w-20" />
        </div>
      ) : (
        tags.length > 0 && (
          <>
            <div className="mb-1 mt-5 px-3 text-xs font-medium uppercase tracking-wide text-fg-3">{t("pcMarkets.markets.sectors")}</div>
            {tags.map((tag) => item(`tag:${tag}`, t(`pcMarkets.tags.${tag}`, { defaultValue: tagLabel(tag) }), <Hash size={16} />))}
          </>
        )
      )}
    </nav>
  );
}

type BoardId = "hot" | "gainers" | "losers" | "newest";

type HighlightsProps = { rows: MarketRow[]; tickerOf: TickerOf; now: number; loading: boolean; onPick: (id: BoardId) => void };

/** Highlights: four small boards (hot, gainers, losers, new) above the table. */
function Highlights({ rows, tickerOf, now, loading, onPick }: HighlightsProps) {
  const { t } = useTranslation();
  const h = useMemo(() => rankHighlights(rows, tickerOf, now, 3), [rows, tickerOf, now]);
  const boards: { id: BoardId; title: string; icon: ReactNode; list: MarketRow[] }[] = [
    { id: "hot", title: t("pcMarkets.markets.highlights.hot"), icon: <Flame size={16} className="text-warn" />, list: h.hot },
    { id: "gainers", title: t("pcMarkets.markets.highlights.gainers"), icon: <TrendingUp size={16} className="text-up" />, list: h.gainers },
    { id: "losers", title: t("pcMarkets.markets.highlights.losers"), icon: <TrendingDown size={16} className="text-down" />, list: h.losers },
    { id: "newest", title: t("pcMarkets.markets.highlights.newest"), icon: <Sparkles size={16} className="text-brand" />, list: h.newest },
  ];
  return (
    <div className="mt-6 grid grid-cols-4 gap-4">
      {boards.map((b, i) => (
        <motion.section
          key={b.id}
          variants={listItem}
          initial="initial"
          animate="animate"
          custom={i}
          className="min-w-0 rounded-3 border border-line-1 bg-bg-1 p-4 transition-[transform,border-color] duration-[var(--t-base)] hover:-translate-y-0.5 hover:border-line-2"
        >
          <h2>
            <button
              type="button"
              onClick={() => onPick(b.id)}
              className="group flex w-full items-center gap-2 text-sm font-medium text-fg-1 transition-colors hover:text-brand"
            >
              {b.icon}
              {b.title}
              <ChevronRight size={14} className="ml-auto text-fg-3 transition-transform group-hover:translate-x-0.5 group-hover:text-brand" />
            </button>
          </h2>
          <ul className="mt-3 flex flex-col gap-1">
            {loading
              ? [0, 1, 2].map((k) => (
                  <li key={k} className="flex h-8 items-center justify-between">
                    <Skeleton className="h-4 w-20" />
                    <Skeleton className="h-4 w-14" />
                  </li>
                ))
              : b.list.length === 0
                ? <li className="flex h-8 items-center text-xs text-fg-3">{t("state.emptyTitle")}</li>
                : b.list.map((r) => <HighlightRow key={r.symbol} row={r} ticker={tickerOf(r.symbol)} />)}
          </ul>
        </motion.section>
      ))}
    </div>
  );
}

function HighlightRow({ row, ticker }: { row: MarketRow; ticker: TickerData | undefined }) {
  return (
    <li>
      <Link
        to={tradePath(row)}
        className="-mx-2 flex h-8 items-center justify-between gap-2 rounded-1 px-2 text-sm transition-colors hover:bg-bg-2"
      >
        <MarketName row={row} size={20} hideName />
        <span className="flex items-center gap-2 tabular-nums">
          <PriceText value={ticker?.last} decimals={row.priceDecimals} className="text-fg-1" />
          <span className={cn("w-16 text-right text-xs", TONE[toneOf(ticker?.change)])}>{formatPercent(ticker?.change)}</span>
        </span>
      </Link>
    </li>
  );
}

const TONE = { up: "text-up", down: "text-down", neutral: "text-fg-2" } as const;

/**
 * StaleNotice warns when the reference prices stop moving for 30 seconds
 * (the feed stalled); it re-checks every 5 seconds on its own.
 */
function StaleNotice({ rows, tickerOf }: { rows: MarketRow[]; tickerOf: TickerOf }) {
  const { t } = useTranslation();
  const now = useNow(5_000);
  const stale = staleSymbols(rows, tickerOf, now);
  if (stale.length === 0) return null;
  return (
    <div role="status" className="mt-4 flex items-center gap-2 rounded-2 border border-warn/40 bg-warn/10 px-4 py-2 text-sm text-warn">
      <TriangleAlert size={16} />
      {t("common.feedStale")}
    </div>
  );
}
