import { dec, errorText, formatPercent, routes, useSettings, useSettleAssets } from "@exchange/core";
import { useBalances, useFuturesAccount, useLiveTickers, useMarginHoldings } from "@exchange/core/assets/hooks";
import { useOpenProducts } from "@exchange/core/platform/products";
import {
  accountShare,
  convertValue,
  distribution,
  isSmall,
  referencePrice,
  valuePortfolio,
  type AccountView,
  type AssetRow,
} from "@exchange/core/assets/valuation";
import { useMarginEntry } from "@exchange/core/margin/hooks";
import {
  AmountText,
  Button,
  CoinIcon,
  CountUp,
  DataTable,
  EmptyState,
  ErrorState,
  HIDDEN_AMOUNT,
  IconButton,
  Input,
  Segmented,
  Skeleton,
  Switch,
  Tooltip,
  cn,
  listItem,
  sortDecimal,
  type ColumnDef,
  type DataColumnMeta,
} from "@exchange/ui";
import { ArrowDownToLine, ArrowLeftRight, ArrowUpFromLine, ChartCandlestick, Eye, EyeOff, Info, Landmark, ScrollText, Search, Wallet } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { WindDownNotice } from "../../features/products/WindDownNotice";
import { TOP_NAV_HEIGHT } from "../../layout/TopNav";
import { AllocationRing, type AllocationSegment } from "./parts/Allocation";
import { AssetsLayout, Card } from "./parts/AssetsLayout";
import { shownDecimals, useAssetMeta, useTradeLinks, withQuery, type AssetMeta } from "./parts/meta";

/**
 * The assets overview (design §6.2): the total value at reference prices
 * with the spot and futures subtotals (and the margin accounts' net, to
 * whom they are shown: B102), the allocation ring and the assets table.
 * Balances follow the balance pushes; prices follow the tickers.
 */
export default function Overview() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const balances = useBalances();
  const margin = useMarginHoldings();
  const showMargin = useMarginEntry();
  const meta = useAssetMeta();
  const { tickers, pending: pricesPending } = useLiveTickers();
  const [view, setView] = useState<AccountView>("ALL");
  const tableRef = useRef<HTMLDivElement>(null);

  const portfolio = useMemo(
    () => valuePortfolio(balances.data?.balances ?? [], (asset) => referencePrice(asset, tickers), margin),
    [balances.data, tickers, margin],
  );
  const btc = referencePrice("BTC", tickers);

  const showAccount = (v: AccountView) => {
    setView(v);
    tableRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <AssetsLayout
      title={t("pcAssets.overview.title")}
      subtitle={t("pcAssets.overview.subtitle")}
      actions={
        <>
          <Button asChild icon={<ArrowDownToLine size={16} />}>
            <Link to={routes.deposit}>{t("nav.deposit")}</Link>
          </Button>
          <Button asChild variant="secondary" icon={<ArrowUpFromLine size={16} />}>
            <Link to={routes.withdraw}>{t("nav.withdraw")}</Link>
          </Button>
          <Button asChild variant="secondary" icon={<ArrowLeftRight size={16} />}>
            <Link to={routes.transfer}>{t("nav.transfer")}</Link>
          </Button>
        </>
      }
    >
      <WindDownNotice />
      <div className="grid grid-cols-12 gap-4">
        <motion.div variants={listItem} initial="initial" animate="animate" custom={0} className="col-span-12 xl:col-span-6">
          <TotalCard
            loading={balances.isPending}
            error={balances.isError ? balances.error : null}
            onRetry={() => void balances.refetch()}
            total={portfolio.total}
            spot={portfolio.spot}
            futures={portfolio.futures}
            margin={showMargin ? portfolio.margin : null}
            onMargin={() => navigate(routes.margin)}
            btcPrice={btc}
            unpriced={pricesPending ? [] : portfolio.unpriced}
            onAccount={showAccount}
          />
        </motion.div>
        <motion.div variants={listItem} initial="initial" animate="animate" custom={1} className="col-span-12 xl:col-span-6">
          <AllocationCard
            rows={portfolio.rows.ALL}
            loading={balances.isPending}
            error={balances.isError ? balances.error : null}
            onRetry={() => void balances.refetch()}
            meta={meta}
          />
        </motion.div>
        <motion.div ref={tableRef} variants={listItem} initial="initial" animate="animate" custom={2} className="col-span-12 scroll-mt-20">
          <AssetsTable
            view={view}
            onView={setView}
            rows={portfolio.rows[view]}
            loading={balances.isPending}
            error={balances.error}
            onRetry={() => void balances.refetch()}
            meta={meta}
          />
        </motion.div>
      </div>
    </AssetsLayout>
  );
}

function TotalCard({
  loading, error, onRetry, total, spot, futures, margin, onMargin, btcPrice, unpriced, onAccount,
}: {
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  total: string;
  spot: string;
  futures: string;
  /** The margin accounts' net; null where they are not shown. */
  margin: string | null;
  onMargin: () => void;
  btcPrice: string | null;
  unpriced: string[];
  onAccount: (v: AccountView) => void;
}) {
  const { t } = useTranslation();
  const hidden = useSettings((s) => s.hideAmounts);
  const set = useSettings((s) => s.set);
  const shown = dec.round(total, 2, "down");
  const inBtc = convertValue(total, btcPrice, 8);
  const parts = [spot, futures, margin ?? "0"];
  const share = (part: string) => {
    const r = accountShare(part, parts);
    return r === null ? null : formatPercent(r, 2, false);
  };

  return (
    <section className="relative h-full overflow-hidden rounded-3 border border-line-1 bg-bg-1 p-5">
      <div aria-hidden className="pointer-events-none absolute -right-24 -top-24 size-64 rounded-full bg-brand-soft blur-3xl" />
      <div className="relative flex items-center gap-1.5 text-sm text-fg-3">
        <span>{t("pcAssets.overview.total")}</span>
        <Tooltip content={t("pcAssets.overview.totalHint")}>
          <button type="button" aria-label={t("pcAssets.overview.totalHint")} className="text-fg-3 hover:text-fg-1">
            <Info size={14} />
          </button>
        </Tooltip>
        <IconButton
          size="xs"
          icon={hidden ? <EyeOff /> : <Eye />}
          label={hidden ? t("pcAssets.overview.showAmounts") : t("pcAssets.overview.hideAmounts")}
          onClick={() => set({ hideAmounts: !hidden })}
        />
      </div>
      {error ? (
        <ErrorState compact message={errorText(error)} onRetry={onRetry} />
      ) : (
        <>
          <div className="relative mt-2 flex items-baseline gap-2" data-testid="assets-total">
            {loading ? (
              <Skeleton className="h-9 w-56" />
            ) : (
              <>
                <span className="text-2xl font-semibold tracking-tight text-fg-1">
                  {hidden ? HIDDEN_AMOUNT : <RollIn value={shown} decimals={2} />}
                </span>
                <span className="text-md text-fg-3">USDT</span>
              </>
            )}
          </div>
          <div className="relative mt-1 h-5 text-sm text-fg-3">
            {!loading && inBtc && t("pcAssets.overview.approxBtc", { value: hidden ? HIDDEN_AMOUNT : dec.round(inBtc, 8, "down") })}
          </div>
          {unpriced.length > 0 && !loading && (
            <p className="relative mt-1 flex items-center gap-1.5 text-xs text-warn">
              <Info size={12} />
              {t("pcAssets.overview.unpriced", { list: unpriced.join(t("pcAssets.common.listSeparator")) })}
            </p>
          )}
          <div className="relative mt-5 grid grid-cols-2 gap-3">
            <AccountTile
              icon={<Wallet size={18} />}
              label={t("pcAssets.common.account.SPOT")}
              value={spot}
              share={share(spot)}
              loading={loading}
              onClick={() => onAccount("SPOT")}
            />
            <AccountTile
              icon={<ChartCandlestick size={18} />}
              label={t("pcAssets.common.account.FUTURES")}
              value={futures}
              share={share(futures)}
              loading={loading}
              onClick={() => onAccount("FUTURES")}
            />
            {margin !== null && (
              <AccountTile
                icon={<Landmark size={18} />}
                label={t("pcAssets.common.account.MARGIN")}
                value={margin}
                share={share(margin)}
                loading={loading}
                onClick={onMargin}
                className="col-span-2"
                testId="assets-margin"
              />
            )}
          </div>
        </>
      )}
    </section>
  );
}

/**
 * RollIn is a CountUp that also rolls up from 0 when it first shows, then
 * to every new value (a transfer, a price move).
 */
function RollIn({ value, decimals }: { value: string; decimals: number }) {
  const [shown, setShown] = useState("0");
  useEffect(() => setShown(value), [value]);
  return <CountUp value={shown} decimals={decimals} />;
}

function AccountTile({
  icon, label, value, share, loading, onClick, className, testId,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  share: string | null;
  loading: boolean;
  onClick: () => void;
  className?: string;
  testId?: string;
}) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      className={cn(
        "group flex items-center gap-3 rounded-2 border border-line-1 bg-bg-2 p-3 text-left transition-[transform,border-color] duration-[var(--t-base)] hover:-translate-y-0.5 hover:border-line-2",
        className,
      )}
    >
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-bg-3 text-fg-2 group-hover:text-brand">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-xs text-fg-3">{label}</span>
        {loading ? (
          <Skeleton className="mt-1 h-4 w-24" />
        ) : (
          <span className="mt-0.5 block truncate text-base font-medium text-fg-1">
            <AmountText value={dec.round(value, 2, "down")} decimals={2} asset="USDT" />
          </span>
        )}
      </span>
      {share && !loading && <span className="shrink-0 text-xs tabular-nums text-fg-3">{t("pcAssets.overview.share", { share })}</span>}
    </button>
  );
}

function AllocationCard({
  rows, loading, error, onRetry, meta,
}: { rows: AssetRow[]; loading: boolean; error: unknown; onRetry: () => void; meta: AssetMeta }) {
  const { t } = useTranslation();
  const segments = useMemo<AllocationSegment[]>(() => {
    const slices = distribution(rows, 5);
    // Colours follow the coin, not its rank by value: the coins are put in
    // market-cap order (then by code) and take the palette slots in turn.
    const coins = slices
      .filter((s) => s.asset !== null)
      .sort((a, b) => meta.rank(a.asset!) - meta.rank(b.asset!) || (a.asset! < b.asset! ? -1 : 1));
    const out: AllocationSegment[] = coins.map((s, i) => ({ key: s.asset!, label: s.asset!, asset: s.asset, value: s.value, share: s.share, tone: i }));
    const other = slices.find((s) => s.asset === null);
    if (other) out.push({ key: "__others", label: t("pcAssets.overview.others"), asset: null, value: other.value, share: other.share, tone: -1 });
    return out;
  }, [rows, meta, t]);
  const count = rows.filter((r) => r.value !== null && dec.sign(r.value) > 0).length;

  return (
    <Card
      title={t("pcAssets.overview.distribution")}
      extra={
        <Tooltip content={t("pcAssets.overview.distributionHint")}>
          <span tabIndex={0} className="cursor-help text-xs text-fg-3">
            {t("pcAssets.overview.assetsCount", { count })}
          </span>
        </Tooltip>
      }
      className="h-full"
    >
      {loading ? (
        <div className="flex items-center gap-6">
          <Skeleton round className="size-[152px]" />
          <div className="flex flex-1 flex-col gap-3">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-4 w-full" />
            ))}
          </div>
        </div>
      ) : error ? (
        <ErrorState compact message={errorText(error)} onRetry={onRetry} />
      ) : segments.length === 0 ? (
        <EmptyState
          compact
          title={t("pcAssets.overview.empty")}
          description={t("pcAssets.overview.distributionEmpty")}
          action={
            <Button asChild size="sm">
              <Link to={routes.deposit}>{t("nav.deposit")}</Link>
            </Button>
          }
        />
      ) : (
        <AllocationRing segments={segments} title={t("pcAssets.overview.distribution")} />
      )}
    </Card>
  );
}

function AssetsTable({
  view, onView, rows, loading, error, onRetry, meta,
}: {
  view: AccountView;
  onView: (v: AccountView) => void;
  rows: AssetRow[];
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  meta: AssetMeta;
}) {
  const { t } = useTranslation();
  const hideSmall = useSettings((s) => s.hideSmallBalances);
  const set = useSettings((s) => s.set);
  const [query, setQuery] = useState("");
  const links = useTradeLinks();

  const q = query.trim().toLowerCase();
  const shown = rows.filter(
    (r) => (!hideSmall || !isSmall(r)) && (!q || r.asset.toLowerCase().includes(q) || meta.name(r.asset).toLowerCase().includes(q)),
  );
  const filtered = shown.length === 0 && rows.length > 0;

  const columns = useMemo<ColumnDef<AssetRow, any>[]>(
    () => [
      {
        id: "asset",
        accessorKey: "asset",
        header: t("pcAssets.common.coin"),
        cell: ({ row }) => (
          <span className="flex items-center gap-3">
            <CoinIcon symbol={row.original.asset} size={28} />
            <span className="min-w-0">
              <span className="block font-medium text-fg-1">{row.original.asset}</span>
              <span className="block truncate text-xs text-fg-3">{meta.name(row.original.asset)}</span>
            </span>
          </span>
        ),
        meta: { width: 200 } satisfies DataColumnMeta,
      },
      {
        id: "available",
        accessorKey: "available",
        header: t("pcAssets.overview.available"),
        sortingFn: sortDecimal,
        cell: ({ row }) => <AmountText value={row.original.available} decimals={shownDecimals(meta.decimals(row.original.asset))} />,
        meta: { align: "right" } satisfies DataColumnMeta,
      },
      {
        id: "frozen",
        accessorKey: "frozen",
        header: t("pcAssets.overview.frozen"),
        sortingFn: sortDecimal,
        cell: ({ row }) => (
          <AmountText value={row.original.frozen} decimals={shownDecimals(meta.decimals(row.original.asset))} className="text-fg-2" />
        ),
        meta: { align: "right" } satisfies DataColumnMeta,
      },
      {
        id: "value",
        accessorKey: "value",
        header: t("pcAssets.common.valuation"),
        sortingFn: sortDecimal,
        cell: ({ row }) =>
          row.original.value === null ? (
            <Tooltip content={t("pcAssets.common.noPrice")}>
              <span tabIndex={0} className="cursor-help text-fg-3">
                —
              </span>
            </Tooltip>
          ) : (
            <AmountText value={row.original.value} decimals={2} />
          ),
        meta: { align: "right" } satisfies DataColumnMeta,
      },
      {
        id: "actions",
        header: t("common.action"),
        enableSorting: false,
        cell: ({ row }) => <RowActions asset={row.original.asset} view={view} meta={meta} links={links} />,
        meta: { align: "right", width: 232 } satisfies DataColumnMeta,
      },
    ],
    [t, meta, links, view],
  );

  return (
    <Card
      title={t("pcAssets.overview.table")}
      extra={
        <Link to={routes.history} className="flex items-center gap-1 text-sm text-fg-2 hover:text-brand">
          <ScrollText size={14} />
          {t("pcAssets.overview.history")}
        </Link>
      }
      bodyClassName="p-0"
    >
      <div className="flex flex-wrap items-center gap-3 px-5 py-3">
        <Segmented
          value={view}
          onValueChange={(v) => onView(v as AccountView)}
          aria-label={t("pcAssets.overview.table")}
          items={[
            { value: "ALL", label: t("pcAssets.overview.viewAll") },
            { value: "SPOT", label: t("pcAssets.overview.viewSpot") },
            { value: "FUTURES", label: t("pcAssets.overview.viewFutures") },
          ]}
        />
        <div className="ml-auto flex items-center gap-4">
          <Tooltip content={t("pcAssets.overview.hideSmallHint")}>
            <span className="shrink-0">
              <Switch size="sm" checked={hideSmall} onCheckedChange={(v) => set({ hideSmallBalances: v })} label={t("pcAssets.overview.hideSmall")} />
            </span>
          </Tooltip>
          <Input
            size="sm"
            value={query}
            onValueChange={setQuery}
            clearable
            onClear={() => setQuery("")}
            prefix={<Search size={14} />}
            placeholder={t("pcAssets.overview.search")}
            aria-label={t("pcAssets.overview.search")}
            containerClassName="w-52"
          />
        </div>
      </div>
      {view === "FUTURES" && <FuturesSummaries held={rows.filter((r) => dec.sign(r.total) > 0).map((r) => r.asset)} meta={meta} />}
      <DataTable
        aria-label={t("pcAssets.overview.table")}
        columns={columns}
        data={shown}
        getRowId={(r) => r.asset}
        loading={loading}
        loadingRows={4}
        error={rows.length === 0 ? error : undefined}
        onRetry={onRetry}
        stickyTop={TOP_NAV_HEIGHT}
        empty={
          filtered ? (
            <EmptyState
              compact
              title={t("pcAssets.overview.noMatch")}
              action={
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => {
                    setQuery("");
                    set({ hideSmallBalances: false });
                  }}
                >
                  {t("pcAssets.overview.clearFilters")}
                </Button>
              }
            />
          ) : (
            <EmptyState
              compact
              title={t("pcAssets.overview.empty")}
              description={t("pcAssets.overview.emptyHint")}
              action={
                <Button asChild size="sm">
                  <Link to={routes.deposit}>{t("nav.deposit")}</Link>
                </Button>
              }
            />
          )
        }
      />
    </Card>
  );
}

function RowActions({ asset, view, meta, links }: { asset: string; view: AccountView; meta: AssetMeta; links: ReturnType<typeof useTradeLinks> }) {
  const { t } = useTranslation();
  const futures = view === "FUTURES";
  const trade = futures ? links.futures(asset) : links.spot(asset);
  const from = futures ? "FUTURES" : "SPOT";
  return (
    <span className="inline-flex items-center justify-end gap-0.5">
      {!futures && (
        <>
          <ActionLink to={meta.canDeposit(asset) ? withQuery(routes.deposit, { asset }) : null} label={t("nav.deposit")} />
          <ActionLink to={meta.canWithdraw(asset) ? withQuery(routes.withdraw, { asset }) : null} label={t("nav.withdraw")} />
        </>
      )}
      <ActionLink to={withQuery(routes.transfer, { asset, from })} label={t("nav.transfer")} />
      <ActionLink to={trade} label={t("nav.trade")} hint={t("pcAssets.overview.noPair")} />
    </span>
  );
}

function ActionLink({ to, label, hint }: { to: string | null; label: string; hint?: string }) {
  const { t } = useTranslation();
  const base = "rounded-1 px-2 py-1 text-sm transition-colors";
  if (!to) {
    return (
      <Tooltip content={hint ?? t("pcAssets.common.onlyInternal")}>
        <span tabIndex={0} className={cn(base, "cursor-not-allowed text-fg-3 opacity-60")}>
          {label}
        </span>
      </Tooltip>
    );
  }
  return (
    <Link to={to} className={cn(base, "text-brand hover:bg-brand-soft")}>
      {label}
    </Link>
  );
}

// The FUTURES accounts' summaries: USDT's always, a coin's (coin-margined
// contracts, design 2026-10-06 §2.6) once it holds something; USDT's too
// only while it holds something once the USDT-margined line is closed
// (design 2026-10-07, product line switches §1 #2).
function FuturesSummaries({ held, meta }: { held: string[]; meta: AssetMeta }) {
  const settles = useSettleAssets();
  const products = useOpenProducts();
  return (
    <>
      {settles
        .filter((a) => (a === "USDT" && products.usdt_m) || held.includes(a))
        .map((a) => (
          <FuturesSummary key={a} asset={a} decimals={a === "USDT" ? 2 : shownDecimals(meta.decimals(a))} />
        ))}
    </>
  );
}

function FuturesSummary({ asset, decimals }: { asset: string; decimals: number }) {
  const { t } = useTranslation();
  const account = useFuturesAccount(true, asset);
  const links = useTradeLinks();
  const a = account.data;
  return (
    <div
      data-testid={asset === "USDT" ? "futures-summary" : `futures-summary-${asset}`}
      className="mx-5 mb-3 flex flex-wrap items-center gap-x-8 gap-y-2 rounded-2 bg-bg-2 px-4 py-3 text-sm"
    >
      {account.isError ? (
        <span className="flex items-center gap-2 text-fg-3">
          {t("pcAssets.overview.futuresUnavailable")} · {errorText(account.error)}
          <Button size="sm" variant="ghost" onClick={() => void account.refetch()}>
            {t("common.retry")}
          </Button>
        </span>
      ) : (
        <>
          <Figure label={t("pcAssets.overview.marginBalance")} loading={account.isPending}>
            <AmountText value={a?.margin_balance} decimals={decimals} asset={a?.asset} />
          </Figure>
          <Figure label={t("pcAssets.overview.unrealized")} loading={account.isPending}>
            <AmountText value={a?.unrealized_pnl} decimals={decimals} sign tone="auto" asset={a?.asset} />
          </Figure>
          <Figure label={t("pcAssets.overview.transferable")} loading={account.isPending}>
            <AmountText value={a?.transferable} decimals={decimals} asset={a?.asset} />
          </Figure>
        </>
      )}
      <Link to={links.futures(asset)} className="ml-auto flex items-center gap-1 text-brand hover:brightness-110">
        <ChartCandlestick size={14} />
        {t("pcAssets.overview.openFutures")}
      </Link>
    </div>
  );
}

function Figure({ label, loading, children }: { label: string; loading: boolean; children: ReactNode }) {
  return (
    <span className="flex flex-col">
      <span className="text-xs text-fg-3">{label}</span>
      {loading ? <Skeleton className="mt-1 h-4 w-20" /> : <span className="font-medium text-fg-1">{children}</span>}
    </span>
  );
}
