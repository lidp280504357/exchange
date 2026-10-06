import { dec, errorText, formatAmount, formatPercent, routes, useSettings } from "@exchange/core";
import { useBalances, useFuturesAccount, useLiveTickers, useMarginHoldings } from "@exchange/core/assets/hooks";
import { useMarginEntry } from "@exchange/core/margin/hooks";
import { convertValue, referencePrice, valuePortfolio, type AccountView, type AssetRow, type Portfolio } from "@exchange/core/assets/valuation";
import {
  AmountText,
  Button,
  CoinIcon,
  CountUp,
  EmptyState,
  ErrorState,
  HIDDEN_AMOUNT,
  Input,
  KeyValue,
  Segmented,
  Sheet,
  Skeleton,
  Switch,
  cn,
  listItem,
} from "@exchange/ui";
import { ArrowDownToLine, ArrowLeftRight, ArrowUpFromLine, ChartCandlestick, ChevronRight, Eye, EyeOff, Info, Landmark, ScrollText, Search, Wallet } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { usePageHeader } from "../../layout/header";
import { Appear, PRESS, RETRY, Section, useKept } from "./parts/bits";
import { filterAssets } from "./parts/logic";
import { shownDecimals, useAssetMeta, useTradeLinks, withQuery, type AssetMeta, type TradeLinks } from "./parts/meta";
import { PullToRefresh } from "../../components/PullToRefresh";

/**
 * The assets tab (design §7.2): the total value at reference prices with
 * the eye toggle, the spot and futures subtotals (and the margin accounts'
 * net, which leads to them, to whom they are shown: B102), the four quick
 * actions, and the assets as cards (search, "hide small", pull to
 * refresh); a card opens a sheet with the coin's balances and its actions.
 * Balances follow the balance pushes; prices follow the tickers channel.
 */
export default function Overview() {
  const { t } = useTranslation();
  const title = t("nav.assets");
  usePageHeader({ title }, [title]);
  const navigate = useNavigate();
  const balances = useBalances();
  const margin = useMarginHoldings();
  const showMargin = useMarginEntry();
  const meta = useAssetMeta();
  const live = useLiveTickers();
  const reduced = useReducedMotion();
  const [view, setView] = useState<AccountView>("ALL");
  const [picked, setPicked] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const futures = useFuturesAccount(view === "FUTURES");

  const portfolio = useMemo(
    () => valuePortfolio(balances.data?.balances ?? [], (asset) => referencePrice(asset, live.tickers), margin),
    [balances.data, live.tickers, margin],
  );
  const btc = referencePrice("BTC", live.tickers);

  const refresh = () => Promise.all([balances.refetch(), live.refetch(), view === "FUTURES" ? futures.refetch() : null]);

  const showAccount = (v: AccountView) => {
    setView(v);
    listRef.current?.scrollIntoView({ behavior: reduced ? "auto" : "smooth", block: "start" });
  };

  return (
    <PullToRefresh onRefresh={refresh}>
      <div className="flex flex-col gap-3 px-4 pb-6 pt-2">
        <motion.div variants={listItem} initial="initial" animate="animate" custom={0}>
          <TotalCard
            loading={balances.isPending}
            error={balances.isError ? balances.error : null}
            onRetry={() => void balances.refetch()}
            portfolio={portfolio}
            btcPrice={btc}
            unpriced={live.pending ? [] : portfolio.unpriced}
            onAccount={showAccount}
            onMargin={showMargin ? () => navigate(routes.margin) : null}
          />
        </motion.div>
        <motion.div variants={listItem} initial="initial" animate="animate" custom={1}>
          <QuickActions />
        </motion.div>
        <motion.div ref={listRef} variants={listItem} initial="initial" animate="animate" custom={2} className="scroll-mt-14">
          <AssetList
            view={view}
            onView={setView}
            rows={portfolio.rows[view]}
            loading={balances.isPending}
            error={balances.error}
            onRetry={() => void balances.refetch()}
            meta={meta}
            onPick={setPicked}
            futures={view === "FUTURES" ? <FuturesSummary query={futures} /> : null}
          />
        </motion.div>
      </div>
      <AssetSheet asset={picked} view={view} portfolio={portfolio} meta={meta} onClose={() => setPicked(null)} />
    </PullToRefresh>
  );
}

function TotalCard({
  loading, error, onRetry, portfolio, btcPrice, unpriced, onAccount, onMargin,
}: {
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  portfolio: Portfolio;
  btcPrice: string | null;
  /** Opens the margin accounts; null where they are not shown (no margin tile). */
  onMargin: (() => void) | null;
  unpriced: string[];
  onAccount: (v: AccountView) => void;
}) {
  const { t } = useTranslation();
  const hidden = useSettings((s) => s.hideAmounts);
  const set = useSettings((s) => s.set);
  const total = portfolio.total;
  const inBtc = convertValue(total, btcPrice, 8);
  const share = (part: string) => (dec.sign(total) > 0 ? formatPercent(dec.div(part, total, 4, "half"), 2, false) : null);

  return (
    <section className="relative overflow-hidden rounded-3 bg-bg-1 p-4">
      <div aria-hidden className="pointer-events-none absolute -right-16 -top-16 size-48 rounded-full bg-brand-soft blur-3xl" />
      <div className="relative flex items-center text-sm text-fg-3">
        <span>{t("mAssets.overview.total")}</span>
        <button
          type="button"
          onClick={() => set({ hideAmounts: !hidden })}
          aria-pressed={hidden}
          aria-label={hidden ? t("mAssets.overview.showAmounts") : t("mAssets.overview.hideAmounts")}
          className="-my-2 grid size-tap place-items-center text-fg-3 active:text-fg-1"
        >
          {hidden ? <EyeOff size={16} /> : <Eye size={16} />}
        </button>
      </div>
      {error ? (
        <ErrorState compact message={errorText(error)} onRetry={onRetry} className={RETRY} />
      ) : (
        <>
          <div className="relative flex items-baseline gap-2" data-testid="assets-total">
            {loading ? (
              <Skeleton className="h-9 w-48" />
            ) : (
              <>
                <span className="min-w-0 break-all text-2xl font-semibold tracking-tight text-fg-1">
                  {hidden ? HIDDEN_AMOUNT : <RollIn value={dec.round(total, 2, "down")} decimals={2} />}
                </span>
                <span className="shrink-0 text-sm text-fg-3">USDT</span>
              </>
            )}
          </div>
          <div className="relative mt-1 min-h-5 text-sm text-fg-3">
            {!loading && inBtc && t("mAssets.overview.approxBtc", { value: hidden ? HIDDEN_AMOUNT : formatAmount(inBtc, 8) })}
          </div>
          {unpriced.length > 0 && !loading && (
            <p className="relative mt-1 flex items-start gap-1.5 text-xs text-warn">
              <Info size={12} className="mt-0.5 shrink-0" />
              {t("mAssets.overview.unpriced", { list: unpriced.join(t("mAssets.common.listSeparator")) })}
            </p>
          )}
          <div className="relative mt-4 grid grid-cols-2 gap-2">
            <AccountTile
              icon={<Wallet size={16} />}
              label={t("mAssets.common.account.SPOT")}
              value={portfolio.spot}
              share={share(portfolio.spot)}
              loading={loading}
              onClick={() => onAccount("SPOT")}
            />
            <AccountTile
              icon={<ChartCandlestick size={16} />}
              label={t("mAssets.common.account.FUTURES")}
              value={portfolio.futures}
              share={share(portfolio.futures)}
              loading={loading}
              onClick={() => onAccount("FUTURES")}
            />
            {onMargin && (
              <AccountTile
                icon={<Landmark size={16} />}
                label={t("mAssets.common.account.MARGIN")}
                value={portfolio.margin}
                share={share(portfolio.margin)}
                loading={loading}
                onClick={onMargin}
                className="col-span-2"
                testId="margin-entry"
              />
            )}
          </div>
        </>
      )}
    </section>
  );
}

/** RollIn is a CountUp that rolls up from 0 when it first shows, then to every new value. */
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
      className={cn("flex min-h-tap min-w-0 flex-col gap-1 rounded-2 bg-bg-2 p-3 text-left", PRESS, className)}
    >
      <span className="flex items-center gap-1.5 text-xs text-fg-3">
        <span className="text-brand">{icon}</span>
        <span className="truncate">{label}</span>
      </span>
      {loading ? (
        <Skeleton className="h-4 w-20" />
      ) : (
        <span className="break-all text-md font-medium text-fg-1">
          <AmountText value={dec.round(value, 2, "down")} decimals={2} />
        </span>
      )}
      {share && !loading && <span className="text-xs tabular-nums text-fg-3">{t("mAssets.overview.share", { share })}</span>}
    </button>
  );
}

const QUICK: { to: string; label: string; icon: ComponentType<{ size?: number }> }[] = [
  { to: routes.deposit, label: "nav.deposit", icon: ArrowDownToLine },
  { to: routes.withdraw, label: "nav.withdraw", icon: ArrowUpFromLine },
  { to: routes.transfer, label: "nav.transfer", icon: ArrowLeftRight },
  { to: routes.history, label: "nav.history", icon: ScrollText },
];

function QuickActions() {
  const { t } = useTranslation();
  return (
    <nav aria-label={t("mAssets.overview.actions")} className="grid grid-cols-4 gap-2">
      {QUICK.map(({ to, label, icon: Icon }) => (
        <Link key={to} to={to} className={cn("flex min-h-16 flex-col items-center justify-center gap-1.5 rounded-3 bg-bg-1 px-1 py-3 text-center", PRESS)}>
          <span className="grid size-10 place-items-center rounded-full bg-brand-soft text-brand">
            <Icon size={18} />
          </span>
          <span className="text-xs text-fg-1">{t(label)}</span>
        </Link>
      ))}
    </nav>
  );
}

function AssetList({
  view, onView, rows, loading, error, onRetry, meta, onPick, futures,
}: {
  view: AccountView;
  onView: (v: AccountView) => void;
  rows: AssetRow[];
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  meta: AssetMeta;
  onPick: (asset: string) => void;
  futures: ReactNode;
}) {
  const { t } = useTranslation();
  const hideSmall = useSettings((s) => s.hideSmallBalances);
  const set = useSettings((s) => s.set);
  const [query, setQuery] = useState("");
  const shown = filterAssets(rows, { query, hideSmall, name: meta.name });
  const filtered = shown.length === 0 && rows.length > 0;

  return (
    <Section title={t("mAssets.overview.list")}>
      <div className="flex flex-col gap-3">
        <Segmented
          size="lg"
          block
          value={view}
          onValueChange={(v) => onView(v as AccountView)}
          aria-label={t("mAssets.overview.list")}
          items={[
            { value: "ALL", label: t("mAssets.overview.viewAll") },
            { value: "SPOT", label: t("mAssets.overview.viewSpot") },
            { value: "FUTURES", label: t("mAssets.overview.viewFutures") },
          ]}
        />
        <Input
          size="lg"
          value={query}
          onValueChange={setQuery}
          clearable
          onClear={() => setQuery("")}
          prefix={<Search size={16} />}
          placeholder={t("mAssets.overview.search")}
          aria-label={t("mAssets.overview.search")}
          enterKeyHint="search"
          autoComplete="off"
        />
        <label className="flex min-h-tap items-center justify-between gap-3 text-sm text-fg-2">
          <span className="flex flex-col">
            {t("mAssets.overview.hideSmall")}
            <span className="text-xs text-fg-3">{t("mAssets.overview.hideSmallHint")}</span>
          </span>
          <Switch checked={hideSmall} onCheckedChange={(v) => set({ hideSmallBalances: v })} aria-label={t("mAssets.overview.hideSmall")} />
        </label>
        {futures}
        {loading ? (
          <div className="flex flex-col gap-2" aria-hidden>
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-[104px] w-full rounded-2" />
            ))}
          </div>
        ) : error && rows.length === 0 ? (
          <ErrorState compact message={errorText(error)} onRetry={onRetry} className={RETRY} />
        ) : filtered ? (
          <EmptyState
            compact
            title={t("mAssets.overview.noMatch")}
            action={
              <Button
                variant="secondary"
                className="h-tap"
                onClick={() => {
                  setQuery("");
                  set({ hideSmallBalances: false });
                }}
              >
                {t("mAssets.overview.clearFilters")}
              </Button>
            }
          />
        ) : shown.length === 0 ? (
          <EmptyState
            compact
            title={t("mAssets.overview.empty")}
            description={t("mAssets.overview.emptyHint")}
            action={
              <Button asChild className="h-tap">
                <Link to={routes.deposit}>{t("nav.deposit")}</Link>
              </Button>
            }
          />
        ) : (
          <ul className="flex flex-col gap-2" aria-label={t("mAssets.overview.list")}>
            {shown.map((r, i) => (
              <Appear key={r.asset} index={i}>
                <AssetCard row={r} meta={meta} onPick={() => onPick(r.asset)} />
              </Appear>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

function AssetCard({ row, meta, onPick }: { row: AssetRow; meta: AssetMeta; onPick: () => void }) {
  const { t } = useTranslation();
  const places = shownDecimals(meta.decimals(row.asset));
  return (
    <button type="button" onClick={onPick} className={cn("block w-full rounded-2 bg-bg-2 p-3 text-left", PRESS)}>
      <span className="flex items-center gap-3">
        <CoinIcon symbol={row.asset} size={32} />
        <span className="min-w-0 flex-1">
          <span className="block font-medium text-fg-1">{row.asset}</span>
          <span className="block truncate text-xs text-fg-3">{meta.name(row.asset)}</span>
        </span>
        <span className="flex min-w-0 flex-col items-end text-right">
          <AmountText value={row.total} decimals={places} className="break-all text-md font-medium text-fg-1" />
          <span className="text-xs text-fg-3">
            {row.value === null ? (
              t("mAssets.overview.noPrice")
            ) : (
              <>
                ≈ <AmountText value={row.value} decimals={2} /> USDT
              </>
            )}
          </span>
        </span>
        <ChevronRight size={16} className="shrink-0 text-fg-3" />
      </span>
      <span className="mt-2.5 grid grid-cols-2 gap-3 border-t border-line-1 pt-2.5 text-xs">
        <span className="flex min-w-0 flex-col">
          <span className="text-fg-3">{t("mAssets.overview.available")}</span>
          <AmountText value={row.available} decimals={places} className="break-all text-sm text-fg-1" />
        </span>
        <span className="flex min-w-0 flex-col items-end text-right">
          <span className="text-fg-3">{t("mAssets.overview.frozen")}</span>
          <AmountText value={row.frozen} decimals={places} className="break-all text-sm text-fg-2" />
        </span>
      </span>
    </button>
  );
}

function FuturesSummary({ query }: { query: ReturnType<typeof useFuturesAccount> }) {
  const { t } = useTranslation();
  const links = useTradeLinks();
  const a = query.data;
  return (
    <div data-testid="futures-summary" className="rounded-2 bg-bg-2 p-3">
      {query.isError ? (
        <div className="flex items-center gap-2 text-sm text-fg-3">
          <span className="min-w-0 flex-1">
            {t("mAssets.overview.futuresUnavailable")} · {errorText(query.error)}
          </span>
          <Button variant="secondary" className="h-tap" onClick={() => void query.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <div className="grid grid-cols-3 gap-2 text-xs">
          <Figure label={t("mAssets.overview.marginBalance")} loading={query.isPending}>
            <AmountText value={a?.margin_balance} decimals={2} />
          </Figure>
          <Figure label={t("mAssets.overview.unrealized")} loading={query.isPending}>
            <AmountText value={a?.unrealized_pnl} decimals={2} sign tone="auto" />
          </Figure>
          <Figure label={t("mAssets.overview.transferable")} loading={query.isPending}>
            <AmountText value={a?.transferable} decimals={2} />
          </Figure>
        </div>
      )}
      <Link to={links.futures("USDT")} className="mt-1 flex min-h-tap items-center justify-center gap-1 text-sm font-medium text-brand">
        <ChartCandlestick size={14} />
        {t("mAssets.overview.openFutures")}
      </Link>
    </div>
  );
}

function Figure({ label, loading, children }: { label: string; loading: boolean; children: ReactNode }) {
  return (
    <span className="flex min-w-0 flex-col gap-0.5">
      <span className="truncate text-fg-3">{label}</span>
      {loading ? <Skeleton className="h-4 w-16" /> : <span className="break-all text-sm font-medium text-fg-1">{children}</span>}
    </span>
  );
}

/** AssetSheet shows a coin's balances in each account and its actions: deposit, withdraw, transfer, trade. */
function AssetSheet({
  asset, view, portfolio, meta, onClose,
}: { asset: string | null; view: AccountView; portfolio: Portfolio; meta: AssetMeta; onClose: () => void }) {
  const kept = useKept(asset);
  const links = useTradeLinks();
  return (
    <Sheet open={asset !== null} onOpenChange={(o) => !o && onClose()} title={kept ? `${kept} · ${meta.name(kept)}` : ""} closeButton>
      {kept && <AssetDetail asset={kept} view={view} portfolio={portfolio} meta={meta} links={links} />}
    </Sheet>
  );
}

function AssetDetail({ asset, view, portfolio, meta, links }: { asset: string; view: AccountView; portfolio: Portfolio; meta: AssetMeta; links: TradeLinks }) {
  const { t } = useTranslation();
  const places = shownDecimals(meta.decimals(asset));
  const all = portfolio.rows.ALL.find((r) => r.asset === asset);
  const spot = portfolio.rows.SPOT.find((r) => r.asset === asset);
  const futures = portfolio.rows.FUTURES.find((r) => r.asset === asset);
  const from =
    view === "FUTURES" || (view === "ALL" && !(spot && dec.sign(spot.available) > 0) && futures && dec.sign(futures.available) > 0)
      ? "FUTURES"
      : "SPOT";
  const trade = view === "FUTURES" ? links.futures(asset) : links.spot(asset);
  const line = (account: "SPOT" | "FUTURES", kind: "available" | "frozen") =>
    t("mAssets.overview.accountLine", { account: t(`mAssets.common.account.${account}`), kind: t(`mAssets.overview.${kind}`) });
  const lines = [
    ...(spot
      ? [
          { key: "spot-available", label: line("SPOT", "available"), value: <AmountText value={spot.available} decimals={places} /> },
          { key: "spot-frozen", label: line("SPOT", "frozen"), value: <AmountText value={spot.frozen} decimals={places} /> },
        ]
      : []),
    ...(futures
      ? [
          { key: "futures-available", label: line("FUTURES", "available"), value: <AmountText value={futures.available} decimals={places} /> },
          { key: "futures-frozen", label: line("FUTURES", "frozen"), value: <AmountText value={futures.frozen} decimals={places} /> },
        ]
      : []),
  ];

  return (
    <div className="flex flex-col gap-4 pt-1">
      <div className="flex items-center gap-3">
        <CoinIcon symbol={asset} size={40} />
        <div className="min-w-0 flex-1">
          <AmountText value={all?.total ?? "0"} decimals={places} asset={asset} className="block break-all text-lg font-semibold text-fg-1" />
          <div className="text-xs text-fg-3">
            {!all || all.value === null ? (
              t("mAssets.overview.noPrice")
            ) : (
              <>
                ≈ <AmountText value={all.value} decimals={2} /> USDT
              </>
            )}
          </div>
        </div>
      </div>
      {lines.length > 0 && <KeyValue items={lines} className="rounded-2 bg-bg-2 p-3" />}
      <div className="grid grid-cols-2 gap-2">
        <ActionTile
          to={meta.canDeposit(asset) ? withQuery(routes.deposit, { asset }) : null}
          icon={ArrowDownToLine}
          label={t("nav.deposit")}
          note={t("mAssets.common.onlyInternal")}
        />
        <ActionTile
          to={meta.canWithdraw(asset) ? withQuery(routes.withdraw, { asset }) : null}
          icon={ArrowUpFromLine}
          label={t("nav.withdraw")}
          note={t("mAssets.common.onlyInternal")}
        />
        <ActionTile to={withQuery(routes.transfer, { asset, from })} icon={ArrowLeftRight} label={t("nav.transfer")} />
        <ActionTile to={trade} icon={ChartCandlestick} label={t("nav.trade")} note={t("mAssets.overview.noPair")} />
      </div>
    </div>
  );
}

function ActionTile({ to, icon: Icon, label, note }: { to: string | null; icon: ComponentType<{ size?: number }>; label: string; note?: string }) {
  const body = (
    <>
      <span className={cn("grid size-9 shrink-0 place-items-center rounded-full", to ? "bg-brand-soft text-brand" : "bg-bg-3 text-fg-3")}>
        <Icon size={18} />
      </span>
      <span className="flex min-w-0 flex-col">
        <span className="text-sm font-medium">{label}</span>
        {!to && note && <span className="truncate text-xs text-fg-3">{note}</span>}
      </span>
    </>
  );
  if (!to) {
    return (
      <span aria-disabled className="flex min-h-14 items-center gap-2.5 rounded-2 bg-bg-2 px-3 text-fg-3 opacity-70">
        {body}
      </span>
    );
  }
  return (
    <Link to={to} className={cn("flex min-h-14 items-center gap-2.5 rounded-2 bg-bg-2 px-3 text-fg-1", PRESS)}>
      {body}
    </Link>
  );
}
