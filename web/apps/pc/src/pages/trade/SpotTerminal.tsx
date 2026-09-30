import { errorText, routes, useAssets, usePair, useTerminalPrefs } from "@exchange/core";
import { Button, EmptyState, ErrorState, Skeleton, type OrderSide } from "@exchange/ui";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";
import { BookPanel } from "./parts/BookPanel";
import { ChartPanel } from "./parts/ChartPanel";
import { OrdersPanel, type OrdersTab } from "./parts/OrdersPanel";
import { SpotOrderPanel } from "./parts/SpotOrderPanel";
import { SpotTickerBar } from "./parts/TickerBar";
import { useTerminalKeys } from "./parts/useTerminalKeys";
import { PanelResizer } from "./parts/PanelResizer";

/**
 * The spot terminal (design §6.2): ticker bar; order book | chart | order
 * form; the caller's orders underneath. The book and the trades are the
 * reference market's; the caller's own orders and fills only show in the
 * bottom panel. Switching pairs keeps the connection and the layout.
 */
export default function SpotTerminal() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { symbol = "" } = useParams();
  const { pair, isPending, error, refetch } = usePair(symbol);
  useAssets();
  const visit = useTerminalPrefs((s) => s.visit);
  const panelHeight = useTerminalPrefs((s) => s.panelHeight);
  const [side, setSide] = useState<OrderSide>("BUY");
  const [fill, setFill] = useState<{ price?: string; quantity?: string } | null>(null);
  const [tab, setTab] = useState<OrdersTab>("open");
  const pickerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (pair) visit(pair.symbol);
  }, [pair, visit]);
  // Canonical upper-case URL (a shared link may be lower case).
  useEffect(() => {
    if (pair && symbol !== pair.symbol) navigate(routes.trade(pair.symbol), { replace: true });
  }, [pair, symbol, navigate]);

  const onPick = useCallback((price: string, quantity?: string) => setFill({ price, quantity }), []);
  useTerminalKeys({
    onBuy: () => setSide("BUY"),
    onSell: () => setSide("SELL"),
    onSearch: () => pickerRef.current?.querySelector<HTMLButtonElement>("button")?.click(),
  });

  if (isPending) return <TerminalSkeleton />;
  if (error) return <ErrorState className="flex-1" message={errorText(error)} onRetry={() => void refetch()} />;
  if (!pair) {
    return (
      <EmptyState
        className="flex-1"
        title={t("pcTrade.unknownPair", { symbol })}
        action={
          <Button asChild size="sm">
            <Link to={routes.markets}>{t("nav.markets")}</Link>
          </Button>
        }
      />
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-px bg-line-1">
      <div ref={pickerRef}>
        <SpotTickerBar pair={pair} onPick={(s) => navigate(routes.trade(s))} />
      </div>
      <div className="grid min-h-0 flex-1 grid-cols-[minmax(260px,22%)_minmax(0,1fr)_minmax(290px,24%)] gap-px">
        <BookPanel
          symbol={pair.symbol}
          base={pair.base_asset}
          quote={pair.quote_asset}
          tickSize={pair.tick_size}
          priceDecimals={pair.price_decimals}
          qtyDecimals={pair.qty_decimals}
          onPick={onPick}
        />
        <ChartPanel symbol={pair.symbol} priceDecimals={pair.price_decimals} qtyDecimals={pair.qty_decimals} base={pair.base_asset} />
        <SpotOrderPanel
          className="overflow-y-auto bg-bg-1"
          pair={pair}
          side={side}
          onSideChange={setSide}
          fill={fill}
          onPlaced={() => setTab("open")}
        />
      </div>
      <PanelResizer />
      <OrdersPanel symbol={pair.symbol} tab={tab} onTabChange={setTab} height={panelHeight} className="shrink-0" />
    </div>
  );
}

export function TerminalSkeleton() {
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-px bg-line-1">
      <div className="flex h-14 items-center gap-6 bg-bg-1 px-4">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-6 w-28" />
        <Skeleton className="h-6 w-64" />
      </div>
      <div className="grid flex-1 grid-cols-[minmax(260px,22%)_minmax(0,1fr)_minmax(290px,24%)] gap-px">
        <div className="bg-bg-1 p-3">
          <Skeleton className="h-full w-full" />
        </div>
        <div className="bg-bg-1 p-3">
          <Skeleton className="h-full w-full" />
        </div>
        <div className="bg-bg-1 p-3">
          <Skeleton className="h-full w-full" />
        </div>
      </div>
      <div className="h-[300px] bg-bg-1" />
    </div>
  );
}
