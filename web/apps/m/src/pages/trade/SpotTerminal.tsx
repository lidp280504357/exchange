import {
  bookSteps, channels, errorText, formatPercent, routes, useOrderBook, usePair, useSyncing, useTerminalPrefs, useTicker, useTickerSeed, useTrades,
  useTradesSeed, useCandles, type CandleInterval,
} from "@exchange/core";
import { useFavorites } from "@exchange/core/markets/favorites";
import { Button, CandleChart, EmptyState, ErrorState, OrderBook, PriceText, Skeleton, TradeTape, cn, toast, type OrderSide } from "@exchange/ui";
import { ChevronDown, Info, Star } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { PairSheet } from "./parts/PairSheet";
import { SpotOrderSheet } from "./parts/SpotOrderSheet";
import { SpotOrders } from "./parts/SpotOrders";
import { SwipeTabs } from "./parts/SwipeTabs";

const INTERVALS: CandleInterval[] = ["1m", "15m", "1h", "4h", "1d"];
const LEVELS = 12;

/**
 * The spot terminal in portrait (design §7.2): the pair switcher and price
 * in the top bar; chart, book and trades as swipeable tabs; the caller's
 * orders; buy and sell buttons fixed above the tab bar that open the order
 * sheet. A tap on a book price opens the sheet with that price.
 */
export default function SpotTerminal() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { symbol = "" } = useParams();
  const { pair, isPending, error, refetch } = usePair(symbol);
  const visit = useTerminalPrefs((s) => s.visit);
  const [tab, setTab] = useState("chart");
  const [pairOpen, setPairOpen] = useState(false);
  const [sheet, setSheet] = useState<OrderSide | null>(null);
  const [fill, setFill] = useState<{ price?: string; quantity?: string } | null>(null);
  useTickerSeed(pair?.symbol ?? "");
  const tk = useTicker(pair?.symbol ?? "");
  const fav = useFavorites();

  useEffect(() => {
    if (pair) visit(pair.symbol);
  }, [pair, visit]);
  useEffect(() => {
    if (pair && symbol !== pair.symbol) navigate(routes.trade(pair.symbol), { replace: true });
  }, [pair, symbol, navigate]);

  const down = tk?.change?.startsWith("-");
  usePageHeader(
    {
      title: pair ? (
        <button type="button" onClick={() => setPairOpen(true)} className="flex min-h-11 items-center gap-1.5">
          <span className="text-md font-semibold text-fg-1">
            {pair.base_asset}
            <span className="text-fg-3">/{pair.quote_asset}</span>
          </span>
          <ChevronDown size={16} className="text-fg-3" />
          <span className={cn("ml-1 text-xs tabular-nums", tk?.change ? (down ? "text-down" : "text-up") : "text-fg-3")}>{formatPercent(tk?.change)}</span>
        </button>
      ) : undefined,
      right: pair ? (
        <div className="flex">
          <Link to={routes.coin(pair.base_asset)} aria-label={t("mTrade.coinInfo")} className="grid size-11 place-items-center text-fg-3">
            <Info size={20} />
          </Link>
          <button
            type="button"
            aria-pressed={fav.has(pair.symbol)}
            aria-label={fav.has(pair.symbol) ? t("common.unfavorite") : t("common.favorite")}
            onClick={() => void fav.toggle(pair.symbol).catch((e: unknown) => toast.error(errorText(e)))}
            className={cn("grid size-11 place-items-center", fav.has(pair.symbol) ? "text-brand" : "text-fg-3")}
          >
            <Star size={20} className={cn(fav.has(pair.symbol) && "fill-current")} />
          </button>
        </div>
      ) : undefined,
    },
    [pair?.symbol, tk?.change, fav.has(pair?.symbol ?? "")],
  );

  const onPick = useCallback((price: string, quantity?: string) => {
    setFill({ price, quantity });
    setSheet("BUY");
  }, []);

  if (isPending) return <Skeleton className="m-4 h-[60vh] rounded-3" />;
  if (error) return <ErrorState message={errorText(error)} onRetry={() => void refetch()} />;
  if (!pair) {
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

  return (
    <div className="pb-20">
      <div className="flex items-end justify-between px-4 pb-2 pt-1">
        <PriceText value={tk?.last} decimals={pair.price_decimals} change={tk?.change} flash={false} arrow className="text-2xl font-semibold" />
        <span className="text-xs text-fg-3">{pair.base_name}</span>
      </div>
      <SwipeTabs
        value={tab}
        onValueChange={setTab}
        tabs={[
          { value: "chart", label: t("mTrade.chart"), content: <ChartTab symbol={pair.symbol} decimals={pair.price_decimals} /> },
          {
            value: "book",
            label: t("mTrade.book"),
            content: (
              <BookTab
                symbol={pair.symbol}
                tickSize={pair.tick_size}
                priceDecimals={pair.price_decimals}
                qtyDecimals={pair.qty_decimals}
                base={pair.base_asset}
                quote={pair.quote_asset}
                onPick={onPick}
              />
            ),
          },
          {
            value: "trades",
            label: t("mTrade.trades"),
            content: <TradesTab symbol={pair.symbol} priceDecimals={pair.price_decimals} qtyDecimals={pair.qty_decimals} base={pair.base_asset} quote={pair.quote_asset} />,
          },
        ]}
      />
      <div className="mt-2 border-t border-line-1">
        <SpotOrders pair={pair} />
      </div>
      <div className="fixed inset-x-0 bottom-[calc(56px+env(safe-area-inset-bottom))] z-[var(--z-sticky)] grid grid-cols-2 gap-2 border-t border-line-1 bg-bg-0/95 px-4 py-2 backdrop-blur">
        <Button size="lg" variant="buy" onClick={() => (setFill(null), setSheet("BUY"))}>
          {t("common.buy")} {pair.base_asset}
        </Button>
        <Button size="lg" variant="sell" onClick={() => (setFill(null), setSheet("SELL"))}>
          {t("common.sell")} {pair.base_asset}
        </Button>
      </div>
      <SpotOrderSheet
        pair={pair}
        side={sheet ?? "BUY"}
        open={sheet !== null}
        onOpenChange={(o) => !o && setSheet(null)}
        fill={fill}
        onPlaced={() => setTab("chart")}
      />
      <PairSheet
        open={pairOpen}
        onOpenChange={setPairOpen}
        current={pair.symbol}
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

function BookTab(props: {
  symbol: string; tickSize: string; priceDecimals: number; qtyDecimals: number; base: string; quote: string;
  onPick: (price: string, quantity?: string) => void;
}) {
  const step = useTerminalPrefs((s) => s.bookStep[props.symbol] ?? "");
  const setStep = useTerminalPrefs((s) => s.setStep);
  const view = useOrderBook(props.symbol, LEVELS, step);
  const syncing = useSyncing(channels.depth(props.symbol));
  const tk = useTicker(props.symbol);
  const steps = bookSteps(props.tickSize);
  return (
    <OrderBook
      view={view}
      priceDecimals={props.priceDecimals}
      qtyDecimals={props.qtyDecimals}
      levels={LEVELS}
      lastPrice={tk?.last}
      steps={steps}
      step={step || steps[0]}
      onStepChange={(s) => setStep(props.symbol, s === steps[0] ? "" : s)}
      onPriceClick={props.onPick}
      syncing={syncing}
      loading={view.asks.length === 0 && view.bids.length === 0 && !tk}
      base={props.base}
      quote={props.quote}
      rowHeight={24}
    />
  );
}

function TradesTab(props: { symbol: string; priceDecimals: number; qtyDecimals: number; base: string; quote: string }) {
  useTradesSeed(props.symbol);
  const trades = useTrades(props.symbol);
  const tk = useTicker(props.symbol);
  return (
    <TradeTape
      trades={trades}
      priceDecimals={props.priceDecimals}
      qtyDecimals={props.qtyDecimals}
      max={24}
      base={props.base}
      quote={props.quote}
      loading={trades.length === 0 && !tk}
      rowHeight={24}
    />
  );
}
