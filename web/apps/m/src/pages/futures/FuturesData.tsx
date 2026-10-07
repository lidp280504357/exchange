import { dec, formatCompact, formatPercent, formatPrice, routes, useSettings } from "@exchange/core";
import {
  filterOverview,
  overviewTotals,
  parseMarginGroup,
  parseOverviewSort,
  sortOverview,
  useOpenContracts,
  useOverviewRows,
  type MarginGroup,
  type OverviewRow,
  type OverviewSort,
} from "@exchange/core/futures/index";
import { Button, ChangeBadge, CoinIcon, EmptyState, ErrorState, Input, Segmented, Select, Sheet, Skeleton, Tag, cn } from "@exchange/ui";
import { Search, X } from "lucide-react";
import { memo, useCallback, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router";
import { WindowList } from "../../components/WindowList";
import { FuturesDataBoard } from "../../features/futures/FuturesDataBoard";
import { usePageHeader } from "../../layout/header";

// /futures/data on the phone (design 2026-10-06 §3.3, batch F2): every
// contract by margin type (USDⓈ-M, COIN-M), searchable, in an order from a
// list: its mark price and day, its funding rate and open interest, the
// group's totals above. A tap opens the contract's futures data in a
// sheet, with the way to its terminal. Group, search and order live in
// the address with the PC site's parameters (margin, q, sort, dir).

const ROW_HEIGHT = 64;
/** Rows from which only the rows near the screen are rendered. */
const VIRTUAL_FROM = 50;

const SORTS: { id: string; sort: OverviewSort }[] = [
  { id: "oiDesc", sort: { key: "oi", desc: true } },
  { id: "fundingDesc", sort: { key: "funding", desc: true } },
  { id: "fundingAsc", sort: { key: "funding", desc: false } },
  { id: "changeDesc", sort: { key: "change", desc: true } },
  { id: "volumeDesc", sort: { key: "volume", desc: true } },
];

const TONE = { 1: "text-up", 0: "text-fg-2", [-1]: "text-down" } as const;

export default function FuturesData() {
  const { t } = useTranslation();
  usePageHeader({ title: t("mFutures.overview.title"), back: routes.markets }, [t]);
  const navigate = useNavigate();
  const locale = useSettings((s) => s.locale);
  const [params, setParams] = useSearchParams();
  const asked = parseMarginGroup(params.get("margin"));
  const sortKey = params.get("sort");
  const sortDir = params.get("dir");
  const sort = useMemo(() => parseOverviewSort(sortKey, sortDir), [sortKey, sortDir]);
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  const [open, setOpen] = useState<string | null>(null);
  const { rows, loading, error, refetch } = useOverviewRows();
  const { contracts } = useOpenContracts();

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

  const hasCoin = rows.some((r) => r.group === "coin");
  // With one contract line closed the other one is the whole overview, without the switch (design 2026-10-07, product line switches §1 #2).
  const both = hasCoin && rows.some((r) => r.group === "usdt");
  const group: MarginGroup = both || rows.length === 0 ? asked : hasCoin ? "coin" : "usdt";
  const inGroup = useMemo(() => filterOverview(rows, group), [rows, group]);
  const shown = useMemo(() => sortOverview(filterOverview(rows, group, query), sort), [rows, group, query, sort]);
  const totals = useMemo(() => overviewTotals(inGroup), [inGroup]);
  const sortId = SORTS.find((o) => o.sort.key === sort.key && o.sort.desc === sort.desc)?.id ?? "oiDesc";
  const onOpen = useCallback((s: string) => setOpen(s), []);
  const contract = contracts.find((c) => c.symbol === open);

  let list: ReactNode;
  if (loading) list = <RowsSkeleton />;
  else if (error && rows.length === 0) list = <ErrorState message={t("state.errorHint")} onRetry={refetch} />;
  else if (shown.length === 0)
    list = (
      <EmptyState
        title={t("mFutures.overview.noMatch")}
        action={
          query ? (
            <Button size="lg" variant="secondary" onClick={() => changeQuery("")}>
              {t("mFutures.overview.clear")}
            </Button>
          ) : undefined
        }
      />
    );
  else if (shown.length >= VIRTUAL_FROM)
    list = (
      <WindowList
        aria-label={t("mFutures.overview.title")}
        items={shown}
        rowHeight={ROW_HEIGHT}
        getKey={(r) => r.symbol}
        renderRow={(r) => <Line row={r} locale={locale} onOpen={onOpen} />}
      />
    );
  else
    list = (
      <ul aria-label={t("mFutures.overview.title")}>
        {shown.map((r) => (
          <li key={r.symbol} style={{ height: ROW_HEIGHT }}>
            <Line row={r} locale={locale} onOpen={onOpen} />
          </li>
        ))}
      </ul>
    );

  return (
    <div className="pb-6">
      <h1 className="sr-only">{t("mFutures.overview.title")}</h1>
      <div className="flex flex-col gap-2 px-4 pt-2">
        {both && (
          <Segmented
            size="md"
            block
            aria-label={t("mFutures.overview.title")}
            value={group}
            onValueChange={(v) => setParam({ margin: (v as MarginGroup) === "coin" ? "coin" : null })}
            items={[
              { value: "usdt", label: t("mFutures.overview.groups.usdt") },
              { value: "coin", label: t("mFutures.overview.groups.coin") },
            ]}
          />
        )}
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
          placeholder={t("mFutures.overview.search")}
          aria-label={t("mFutures.overview.search")}
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

      <dl className="mt-3 grid grid-cols-3 gap-2 px-4">
        <Total label={t("mFutures.overview.totals.oiValue")} value={<Usd value={formatCompact(totals.openInterestValue, locale)} />} loading={loading} />
        <Total label={t("mFutures.overview.totals.volume")} value={<Usd value={formatCompact(totals.volume, locale)} />} loading={loading} />
        <Total
          label={t("mFutures.overview.totals.funding")}
          value={
            <>
              <span className="text-up">{totals.positive}</span>
              <span className="mx-0.5 text-fg-3">/</span>
              <span className="text-down">{totals.negative}</span>
            </>
          }
          loading={loading}
        />
      </dl>

      <div className="mt-3 flex items-center justify-between gap-3 border-b border-line-1 px-4 pb-2">
        <span className="text-xs text-fg-3">{!loading && t("mFutures.overview.count", { count: shown.length })}</span>
        <Select
          size="sm"
          variant="ghost"
          aria-label={t("mFutures.overview.sort")}
          value={sortId}
          onValueChange={(id) => {
            const o = SORTS.find((x) => x.id === id)!;
            setParam({ sort: o.sort.key, dir: o.sort.desc ? "desc" : "asc" });
          }}
          options={SORTS.map((o) => ({ value: o.id, label: t(`mFutures.overview.sorts.${o.id}`) }))}
        />
      </div>

      {list}

      <Sheet
        open={open !== null}
        onOpenChange={(v) => !v && setOpen(null)}
        title={contract ? t("mFutures.overview.board", { symbol: `${contract.base_asset}${contract.quote_asset}` }) : t("mFutures.overview.title")}
        closeButton
        footer={
          contract && (
            <Button size="lg" className="w-full" onClick={() => navigate(routes.futures(contract.symbol))}>
              {t("mFutures.overview.goTrade")}
            </Button>
          )
        }
      >
        {contract && <FuturesDataBoard contract={contract} overviewLink={false} />}
      </Sheet>
    </div>
  );
}

function Total({ label, value, loading }: { label: string; value: ReactNode; loading: boolean }) {
  return (
    <div className="min-w-0 rounded-2 bg-bg-1 px-3 py-2">
      <dt className="truncate text-xs text-fg-3" title={label}>
        {label}
      </dt>
      <dd className="mt-1 truncate text-base font-semibold text-fg-1">{loading ? <Skeleton className="h-5 w-14" /> : value}</dd>
    </div>
  );
}

function Usd({ value }: { value: string }) {
  return (
    <>
      {value}
      <span className="ml-1 text-xs font-normal text-fg-3">USD</span>
    </>
  );
}

/** Line is one contract: a button that opens its futures data. */
const Line = memo(function Line({ row, locale, onOpen }: { row: OverviewRow; locale: string; onOpen: (symbol: string) => void }) {
  const { t } = useTranslation();
  const sign = row.funding_rate && dec.isDecimal(row.funding_rate) ? dec.sign(row.funding_rate) : 0;
  return (
    <button type="button" onClick={() => onOpen(row.symbol)} className="flex h-16 w-full items-center gap-2.5 px-4 text-left transition-colors active:bg-bg-1">
      <CoinIcon symbol={row.base} size={28} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1">
          <span className="truncate font-medium text-fg-1">{`${row.base}${row.quote}`}</span>
          <Tag tone="brand">{t("mFutures.overview.perpetual")}</Tag>
        </div>
        <div className="mt-0.5 truncate text-xs text-fg-3">
          {t("mFutures.list.rateShort")} <span className={cn("tabular-nums", TONE[sign])}>{formatPercent(row.funding_rate, 4)}</span>
          {row.open_interest_value && (
            <>
              {" · "}
              {t("mFutures.list.oiShort")} <span className="tabular-nums text-fg-2">{formatCompact(row.open_interest_value, locale)}</span>
            </>
          )}
        </div>
      </div>
      <div className="flex w-[96px] shrink-0 flex-col items-end gap-1">
        <span className="max-w-full truncate text-base font-medium tabular-nums text-fg-1">{formatPrice(row.mark_price, row.priceDecimals)}</span>
        <ChangeBadge value={row.change} />
      </div>
    </button>
  );
});

function RowsSkeleton() {
  return (
    <ul aria-hidden>
      {Array.from({ length: 6 }, (_, i) => (
        <li key={i} className="flex h-16 items-center gap-2.5 px-4">
          <Skeleton round className="size-7 shrink-0" />
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-3 w-40" />
          </div>
          <div className="flex w-[96px] shrink-0 flex-col items-end gap-1.5">
            <Skeleton className="h-4 w-16" />
            <Skeleton className="h-5 w-16" />
          </div>
        </li>
      ))}
    </ul>
  );
}
