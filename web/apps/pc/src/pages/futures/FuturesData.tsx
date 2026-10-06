import { dec, formatCompact, formatPercent, formatPrice, routes, useSettings } from "@exchange/core";
import {
  filterOverview,
  hasFuturesData,
  overviewTotals,
  parseMarginGroup,
  parseOverviewSort,
  sortOverview,
  useOpenContracts,
  useOverviewRows,
  type MarginGroup,
  type OverviewRow,
  type OverviewSortKey,
} from "@exchange/core/futures/index";
import {
  Button,
  ChangeBadge,
  CoinIcon,
  DataTable,
  EmptyState,
  Input,
  Segmented,
  Select,
  Stat,
  Tag,
  countdown,
  createColumnHelper,
  cn,
  prefersReducedMotion,
  subscribeClock,
  type ColumnDef,
  type SortingState,
} from "@exchange/ui";
import { Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useSearchParams } from "react-router";
import { FuturesDataBoard } from "../../features/futures/FuturesDataBoard";
import { qtyUnit } from "../../features/futures/labels";
import { TOP_NAV_HEIGHT } from "../../layout/TopNav";
import { useMediaQuery, usePageTitle } from "../markets/hooks";

// /futures/data (design 2026-10-06 §3.3, batch F2): every contract's mark
// and index prices, funding rate with its countdown, open interest and
// day, by margin type (USDⓈ-M, COIN-M), searchable and sortable, with the
// group's totals above; under it the futures data board of the contract
// picked in the table. Group, search, order and the contract live in the
// address (margin, q, sort, dir, symbol).

const COLUMN_SORT: Record<string, OverviewSortKey> = { contract: "symbol", mark: "mark", change: "change", volume: "volume", oi: "oi", funding: "funding" };
const SORT_COLUMN: Record<OverviewSortKey, string> = { symbol: "contract", mark: "mark", change: "change", volume: "volume", oi: "oi", funding: "funding" };

/** From this many rows the table scrolls in a box of its own, so the board under it stays near. */
const BOX_FROM = 16;

export default function FuturesData() {
  const { t } = useTranslation();
  usePageTitle(t("pcFutures.overview.title"));
  const navigate = useNavigate();
  const locale = useSettings((s) => s.locale);
  const wide = useMediaQuery("(min-width: 1280px)");
  const [params, setParams] = useSearchParams();
  const group = parseMarginGroup(params.get("margin"));
  const sortKey = params.get("sort");
  const sortDir = params.get("dir");
  const sort = useMemo(() => parseOverviewSort(sortKey, sortDir), [sortKey, sortDir]);
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  const { rows, loading, error, refetch } = useOverviewRows();
  const { contracts } = useOpenContracts();
  const board = useRef<HTMLElement>(null);

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

  const hasCoin = rows.some((r) => r.group === "coin");
  const inGroup = useMemo(() => filterOverview(rows, group), [rows, group]);
  const shown = useMemo(() => sortOverview(filterOverview(rows, group, query), sort), [rows, group, query, sort]);
  const totals = useMemo(() => overviewTotals(inGroup), [inGroup]);

  // The board's contract: the one in the address, else the first with data in the table's order.
  const withData = useMemo(() => contracts.filter(hasFuturesData), [contracts]);
  const symbol = params.get("symbol");
  const picked = contracts.find((c) => c.symbol === symbol) ?? withData.find((c) => c.symbol === shown[0]?.symbol) ?? withData[0];
  const pick = (s: string, scroll: boolean) => {
    setParam({ symbol: s });
    if (scroll) board.current?.scrollIntoView({ behavior: prefersReducedMotion() ? "auto" : "smooth", block: "start" });
  };

  const sorting: SortingState = [{ id: SORT_COLUMN[sort.key], desc: sort.desc }];
  const onSortingChange = (next: SortingState) => {
    const s = next[0];
    const key = s ? COLUMN_SORT[s.id] : undefined;
    setParam(key ? { sort: key, dir: s!.desc ? "desc" : "asc" } : { sort: null, dir: null });
  };

  const columns = useMemo(() => {
    const col = createColumnHelper<OverviewRow>();
    const list: ColumnDef<OverviewRow, any>[] = [
      col.accessor((r) => r.symbol, {
        id: "contract",
        header: t("pcFutures.overview.columns.contract"),
        sortDescFirst: false,
        cell: ({ row }) => <ContractName row={row.original} />,
      }),
      col.accessor((r) => r.mark_price, {
        id: "mark",
        header: t("pcFutures.overview.columns.mark"),
        sortDescFirst: true,
        cell: ({ row }) => <span className="tabular-nums text-fg-1">{formatPrice(row.original.mark_price, row.original.priceDecimals)}</span>,
        meta: { align: "right", width: wide ? 132 : 120 },
      }),
      ...(wide
        ? [
            col.accessor((r) => r.index_price, {
              id: "index",
              header: t("pcFutures.overview.columns.index"),
              enableSorting: false,
              cell: ({ row }) => <span className="tabular-nums text-fg-2">{formatPrice(row.original.index_price, row.original.priceDecimals)}</span>,
              meta: { align: "right", width: 132 },
            }),
          ]
        : []),
      col.accessor((r) => r.change, {
        id: "change",
        header: t("pcFutures.overview.columns.change"),
        sortDescFirst: true,
        cell: ({ row }) => <ChangeBadge value={row.original.change} />,
        meta: { align: "right", width: 104 },
      }),
      col.accessor((r) => r.quote_volume, {
        id: "volume",
        header: t("pcFutures.overview.columns.volume"),
        sortDescFirst: true,
        cell: ({ row }) => <span className="tabular-nums text-fg-2">{formatCompact(row.original.quote_volume, locale)}</span>,
        meta: { align: "right", width: 116 },
      }),
      col.accessor((r) => r.open_interest_value, {
        id: "oi",
        header: t("pcFutures.overview.columns.oi"),
        sortDescFirst: true,
        cell: ({ row }) => <OpenInterest row={row.original} locale={locale} />,
        meta: { align: "right", width: 136 },
      }),
      col.accessor((r) => r.funding_rate, {
        id: "funding",
        header: t("pcFutures.overview.columns.funding"),
        sortDescFirst: true,
        cell: ({ row }) => <FundingCell row={row.original} />,
        meta: { align: "right", width: 132 },
      }),
      col.display({
        id: "actions",
        header: () => <span className="sr-only">{t("pcFutures.overview.columns.actions")}</span>,
        cell: ({ row }) => (
          <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
            {row.original.futures_data && (
              <Button size="sm" variant="ghost" onClick={() => pick(row.original.symbol, true)}>
                {t("pcFutures.overview.data")}
              </Button>
            )}
            <Button asChild size="sm" variant="secondary">
              <Link to={routes.futures(row.original.symbol)}>{t("pcFutures.overview.trade")}</Link>
            </Button>
          </div>
        ),
        meta: { align: "right", width: 132 },
      }),
    ];
    return list;
    // pick only changes the address.
  }, [t, locale, wide]);

  const changeGroup = (g: MarginGroup) => setParam({ margin: g === "coin" ? "coin" : null });
  const changeQuery = (v: string) => {
    setQuery(v);
    setParam({ q: v.trim() || null });
  };
  const boxed = shown.length >= BOX_FROM;

  return (
    <div className="mx-auto max-w-[1440px] px-6 py-8">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold text-fg-1">{t("pcFutures.overview.title")}</h1>
          <p className="mt-1 max-w-3xl text-sm text-fg-3">{t("pcFutures.overview.subtitle")}</p>
        </div>
        <span className="text-xs text-fg-3">{t("pcFutures.overview.updated")}</span>
      </header>

      <div className="mt-6 grid grid-cols-3 gap-4">
        <Stat
          className="rounded-3 border border-line-1 bg-bg-1 p-4"
          label={t("pcFutures.overview.totals.oiValue")}
          value={formatCompact(totals.openInterestValue, locale)}
          loading={loading}
        />
        <Stat
          className="rounded-3 border border-line-1 bg-bg-1 p-4"
          label={t("pcFutures.overview.totals.volume")}
          value={formatCompact(totals.volume, locale)}
          loading={loading}
        />
        <Stat
          className="rounded-3 border border-line-1 bg-bg-1 p-4"
          label={t("pcFutures.overview.totals.funding")}
          value={
            <span>
              <span className="text-up">{totals.positive}</span>
              <span className="mx-1 text-fg-3">/</span>
              <span className="text-down">{totals.negative}</span>
            </span>
          }
          loading={loading}
        />
      </div>

      {/* clip, not hidden: the table's header sticks to the page under the top bar. */}
      <section className="mt-6 min-w-0 overflow-clip rounded-3 border border-line-1 bg-bg-1">
        <div className="flex flex-wrap items-center gap-3 border-b border-line-1 px-4 py-3">
          {hasCoin && (
            <Segmented
              size="sm"
              aria-label={t("pcFutures.overview.title")}
              value={group}
              onValueChange={(v) => changeGroup(v as MarginGroup)}
              items={[
                { value: "usdt", label: t("pcFutures.overview.groups.usdt") },
                { value: "coin", label: t("pcFutures.overview.groups.coin") },
              ]}
            />
          )}
          <Input
            value={query}
            onValueChange={changeQuery}
            clearable
            onClear={() => changeQuery("")}
            placeholder={t("pcFutures.overview.search")}
            aria-label={t("pcFutures.overview.search")}
            prefix={<Search size={16} className="text-fg-3" />}
            size="md"
            containerClassName="max-w-xs"
          />
          <span className="ml-auto text-xs tabular-nums text-fg-3">{!loading && t("pcFutures.overview.count", { count: shown.length })}</span>
        </div>
        <DataTable
          aria-label={t("pcFutures.overview.title")}
          columns={columns}
          data={shown}
          getRowId={(r) => r.symbol}
          loading={loading}
          loadingRows={8}
          error={error}
          onRetry={refetch}
          empty={
            <EmptyState
              compact
              title={t("pcFutures.overview.noMatch")}
              action={
                query ? (
                  <Button size="sm" variant="secondary" onClick={() => changeQuery("")}>
                    {t("pcFutures.overview.clear")}
                  </Button>
                ) : undefined
              }
            />
          }
          sorting={sorting}
          onSortingChange={onSortingChange}
          manualSorting
          onRowClick={(r) => (r.futures_data ? pick(r.symbol, true) : navigate(routes.futures(r.symbol)))}
          isRowActive={(r) => r.symbol === picked?.symbol}
          virtual={boxed}
          height={boxed ? "min(640px, calc(100dvh - 220px))" : undefined}
          stickyTop={boxed ? 0 : TOP_NAV_HEIGHT}
        />
      </section>

      {picked && (
        <section ref={board} aria-label={t("pcFutures.overview.board", { symbol: `${picked.base_asset}${picked.quote_asset}` })} className="mt-8 scroll-mt-20">
          <div className="mb-3 flex flex-wrap items-center gap-3">
            <h2 className="text-md font-semibold text-fg-1">{t("pcFutures.overview.board", { symbol: `${picked.base_asset}${picked.quote_asset}` })}</h2>
            <Select
              size="sm"
              aria-label={t("pcFutures.overview.pick")}
              value={picked.symbol}
              onValueChange={(s) => pick(s, false)}
              options={withData.map((c) => ({ value: c.symbol, label: `${c.base_asset}${c.quote_asset} ${t("pcFutures.overview.perpetual")}` }))}
              className="w-52"
            />
            <Link to={routes.futures(picked.symbol)} className="ml-auto text-sm text-brand hover:underline">
              {t("pcFutures.overview.trade")}
            </Link>
          </div>
          <FuturesDataBoard key={picked.symbol} contract={picked} layout="page" />
        </section>
      )}
    </div>
  );
}

function ContractName({ row }: { row: OverviewRow }) {
  const { t } = useTranslation();
  return (
    <div className="flex min-w-0 items-center gap-3">
      <CoinIcon symbol={row.base} size={24} />
      <span className="truncate font-medium text-fg-1">{`${row.base}${row.quote}`}</span>
      <Tag tone="brand">{t("pcFutures.overview.perpetual")}</Tag>
    </div>
  );
}

function OpenInterest({ row, locale }: { row: OverviewRow; locale: string }) {
  const { t } = useTranslation();
  if (!row.open_interest_value) return <span className="text-fg-3">—</span>;
  return (
    <div className="flex flex-col items-end leading-tight">
      <span className="tabular-nums text-fg-1">{formatCompact(row.open_interest_value, locale)}</span>
      <span className="text-xs tabular-nums text-fg-3">
        {formatCompact(row.open_interest, locale)} {qtyUnit({ margin_type: row.group === "coin" ? "COIN" : "USDT", base_asset: row.base }, t("pcFutures.contracts"))}
      </span>
    </div>
  );
}

const RATE_TONE = { 1: "text-up", 0: "text-fg-2", [-1]: "text-down" } as const;

function FundingCell({ row }: { row: OverviewRow }) {
  const sign = row.funding_rate && dec.isDecimal(row.funding_rate) ? dec.sign(row.funding_rate) : 0;
  return (
    <div className="flex flex-col items-end leading-tight">
      <span className={cn("tabular-nums", RATE_TONE[sign])}>{formatPercent(row.funding_rate, 4)}</span>
      <Countdown at={row.next_funding_time ? Date.parse(row.next_funding_time) : Number.NaN} />
    </div>
  );
}

/**
 * Countdown is the time left to the next funding, written into its text by
 * the shared one-second clock: the rows do not render every second.
 */
function Countdown({ at }: { at: number }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (Number.isNaN(at)) return;
    return subscribeClock(1000, () => {
      if (ref.current) ref.current.textContent = countdown(at, Date.now());
    });
  }, [at]);
  return (
    <span ref={ref} className="text-xs tabular-nums text-fg-3">
      {Number.isNaN(at) ? "--:--:--" : countdown(at, Date.now())}
    </span>
  );
}
