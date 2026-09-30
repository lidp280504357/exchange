import {
  bookSteps, channels, dec, errorText, formatPercent, formatPrice, routes, useCandles, useContract, useMarkPrice, useOrderBook, usePositions,
  useSyncing, useTerminalPrefs, useTicker, useTickerSeed, useTrades, useTradesSeed, type CandleInterval, type Contract,
} from "@exchange/core";
import { useFavorites } from "@exchange/core/markets/favorites";
import { Button, CandleChart, EmptyState, ErrorState, FundingCountdown, OrderBook, PriceText, Sheet, Skeleton, TradeTape, cn, toast } from "@exchange/ui";
import { ChevronDown, Info, Star, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { FuturesOrderForm } from "./parts/FuturesOrderForm";
import { FuturesOrders } from "./parts/FuturesOrders";
import { FuturesPositions } from "./parts/FuturesPositions";
import { PairSheet } from "./parts/PairSheet";
import { SwipeTabs } from "./parts/SwipeTabs";

const INTERVALS: CandleInterval[] = ["1m", "15m", "1h", "4h", "1d"];
const LEVELS = 12;

/**
 * The futures terminal in portrait (design §7.2): the spot terminal's
 * layout with the mark price and the funding countdown, a positions tab,
 * and an order sheet with margin mode, leverage, open and close; a banner
 * while the contract is reduce-only.
 */
export default function FuturesTerminal() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { symbol = "" } = useParams();
  const { contract, isPending, error, refetch } = useContract(symbol);
  const visit = useTerminalPrefs((s) => s.visit);
  const [tab, setTab] = useState("chart");
  const [pairOpen, setPairOpen] = useState(false);
  const [sheet, setSheet] = useState(false);
  const [fill, setFill] = useState<{ price?: string; quantity?: string } | null>(null);
  const sym = contract?.symbol ?? "";
  useTickerSeed(sym);
  const tk = useTicker(sym);
  const mark = useMarkPrice(sym).data;
  const positions = usePositions("");
  const fav = useFavorites();

  useEffect(() => {
    if (contract) visit(contract.symbol);
  }, [contract, visit]);
  useEffect(() => {
    if (contract && symbol !== contract.symbol) navigate(routes.futures(contract.symbol), { replace: true });
  }, [contract, symbol, navigate]);

  const down = tk?.change?.startsWith("-");
  usePageHeader(
    {
      title: contract ? (
        <button type="button" onClick={() => setPairOpen(true)} className="flex min-h-11 items-center gap-1.5">
          <span className="text-md font-semibold text-fg-1">
            {contract.base_asset}
            {contract.quote_asset}
          </span>
          <span className="rounded-1 bg-brand-soft px-1.5 py-0.5 text-xs text-brand">{t("m.perpetual")}</span>
          <ChevronDown size={16} className="text-fg-3" />
          <span className={cn("ml-1 text-xs tabular-nums", tk?.change ? (down ? "text-down" : "text-up") : "text-fg-3")}>{formatPercent(tk?.change)}</span>
        </button>
      ) : undefined,
      right: contract ? (
        <div className="flex">
          <Link to={routes.coin(contract.base_asset)} aria-label={t("mTrade.coinInfo")} className="grid size-11 place-items-center text-fg-3">
            <Info size={20} />
          </Link>
          <button
            type="button"
            aria-pressed={fav.has(contract.symbol)}
            aria-label={fav.has(contract.symbol) ? t("common.unfavorite") : t("common.favorite")}
            onClick={() => void fav.toggle(contract.symbol).catch((e: unknown) => toast.error(errorText(e)))}
            className={cn("grid size-11 place-items-center", fav.has(contract.symbol) ? "text-brand" : "text-fg-3")}
          >
            <Star size={20} className={cn(fav.has(contract.symbol) && "fill-current")} />
          </button>
        </div>
      ) : undefined,
    },
    [contract?.symbol, tk?.change, fav.has(sym)],
  );

  const onPick = useCallback((price: string, quantity?: string) => {
    setFill({ price, quantity });
    setSheet(true);
  }, []);

  if (isPending) return <Skeleton className="m-4 h-[60vh] rounded-3" />;
  if (error) return <ErrorState message={errorText(error)} onRetry={() => void refetch()} />;
  if (!contract) {
    return (
      <EmptyState
        title={t("mTrade.unknownPair", { symbol })}
        action={
          <Button asChild size="sm" className="hit-area">
            <Link to={routes.markets}>{t("nav.markets")}</Link>
          </Button>
        }
      />
    );
  }

  const pd = dec.decimalsOf(contract.tick_size);
  const qd = dec.decimalsOf(contract.lot_size);
  const mine = (positions.data?.positions ?? []).length;
  return (
    <div className="pb-20">
      {mark?.degraded && (
        <div role="status" className="flex items-center gap-2 bg-warn px-4 py-2 text-xs text-brand-fg">
          <TriangleAlert size={14} />
          {t("mTrade.degraded")}
        </div>
      )}
      <div className="flex items-end justify-between gap-3 px-4 pb-2 pt-1">
        <PriceText value={tk?.last} decimals={pd} change={tk?.change} className="text-2xl font-semibold" />
        <div className="flex flex-col items-end gap-0.5 text-xs">
          <span className="text-fg-3">
            {t("mTrade.markPrice")} <span className="tabular-nums text-fg-1">{formatPrice(mark?.mark_price, pd)}</span>
          </span>
          <FundingCountdown nextFundingTime={mark?.next_funding_time} rate={mark?.funding_rate} layout="inline" />
        </div>
      </div>
      <SwipeTabs
        value={tab}
        onValueChange={setTab}
        tabs={[
          { value: "chart", label: t("mTrade.chart"), content: <ChartTab symbol={contract.symbol} decimals={pd} /> },
          {
            value: "book",
            label: t("mTrade.book"),
            content: <BookTab contract={contract} priceDecimals={pd} qtyDecimals={qd} markPrice={mark?.mark_price} onPick={onPick} />,
          },
          { value: "trades", label: t("mTrade.trades"), content: <TradesTab contract={contract} priceDecimals={pd} qtyDecimals={qd} /> },
          { value: "positions", label: t("mTrade.positions"), count: mine, content: <FuturesPositions symbol={contract.symbol} /> },
        ]}
      />
      <div className="mt-2 border-t border-line-1">
        <FuturesOrders contract={contract} />
      </div>
      <div className="fixed inset-x-0 bottom-[calc(56px+env(safe-area-inset-bottom))] z-[var(--z-sticky)] grid grid-cols-2 gap-2 border-t border-line-1 bg-bg-0/95 px-4 py-2 backdrop-blur">
        <Button size="lg" variant="buy" onClick={() => (setFill(null), setSheet(true))}>
          {t("mTrade.openLong")}
        </Button>
        <Button size="lg" variant="sell" onClick={() => (setFill(null), setSheet(true))}>
          {t("mTrade.openShort")}
        </Button>
      </div>
      <Sheet open={sheet} onOpenChange={setSheet} title={`${contract.base_asset}${contract.quote_asset} ${t("m.perpetual")}`}>
        <FuturesOrderForm contract={contract} fill={fill} onPlaced={() => setSheet(false)} className="pb-2" />
      </Sheet>
      <PairSheet
        open={pairOpen}
        onOpenChange={setPairOpen}
        current={contract.symbol}
        onPick={(s, futures) => navigate(futures ? routes.futures(s) : routes.trade(s))}
      />
    </div>
  );
}

function ChartTab({ symbol, decimals }: { symbol: string; decimals: number }) {
  const stored = useTerminalPrefs((s) => s.interval);
  const set = useTerminalPrefs((s) => s.set);
  const interval = (INTERVALS as string[]).includes(stored) ? (stored as CandleInterval) : "15m";
  const { candles, hasMore, loading, loadMore } = useCandles(symbol, interval);
  return (
    <CandleChart
      symbol={symbol}
      candles={candles}
      interval={interval}
      priceDecimals={decimals}
      loading={loading}
      hasMore={hasMore}
      onLoadMore={loadMore}
      intervals={INTERVALS}
      onIntervalChange={(i) => set({ interval: i })}
      height="42vh"
    />
  );
}

function BookTab({
  contract, priceDecimals, qtyDecimals, markPrice, onPick,
}: {
  contract: Contract;
  priceDecimals: number;
  qtyDecimals: number;
  markPrice?: string | null;
  onPick: (price: string, quantity?: string) => void;
}) {
  const step = useTerminalPrefs((s) => s.bookStep[contract.symbol] ?? "");
  const setStep = useTerminalPrefs((s) => s.setStep);
  const view = useOrderBook(contract.symbol, LEVELS, step);
  const syncing = useSyncing(channels.depth(contract.symbol));
  const tk = useTicker(contract.symbol);
  const steps = bookSteps(contract.tick_size);
  return (
    <OrderBook
      view={view}
      priceDecimals={priceDecimals}
      qtyDecimals={qtyDecimals}
      levels={LEVELS}
      lastPrice={tk?.last}
      markPrice={markPrice}
      steps={steps}
      step={step || steps[0]}
      onStepChange={(s) => setStep(contract.symbol, s === steps[0] ? "" : s)}
      onPriceClick={onPick}
      syncing={syncing}
      loading={view.asks.length === 0 && view.bids.length === 0 && !tk}
      base={contract.base_asset}
      quote={contract.quote_asset}
      rowHeight={24}
    />
  );
}

function TradesTab({ contract, priceDecimals, qtyDecimals }: { contract: Contract; priceDecimals: number; qtyDecimals: number }) {
  useTradesSeed(contract.symbol);
  const trades = useTrades(contract.symbol);
  const tk = useTicker(contract.symbol);
  return (
    <TradeTape
      trades={trades}
      priceDecimals={priceDecimals}
      qtyDecimals={qtyDecimals}
      max={24}
      base={contract.base_asset}
      quote={contract.quote_asset}
      loading={trades.length === 0 && !tk}
      rowHeight={24}
    />
  );
}
