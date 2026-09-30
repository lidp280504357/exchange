import { useCandles, useOrderBook, useTerminalPrefs, type CandleInterval } from "@exchange/core";
// The index's CandleChart loads the chart library in its own chunk, so the
// book, the form and the ticker render before it.
import { CandleChart, DepthChart, cn, type Indicator } from "@exchange/ui";
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";

const INTERVALS: CandleInterval[] = ["1m", "5m", "15m", "1h", "4h", "1d", "1w"];

/**
 * ChartPanel: the candle chart (intervals, MA/EMA/VOL, full screen, older
 * candles paged in as the view scrolls left) and the collapsible depth
 * chart; the interval, indicators and depth chart are remembered.
 */
export function ChartPanel({
  symbol, priceDecimals, qtyDecimals, base, className,
}: {
  symbol: string;
  priceDecimals: number;
  qtyDecimals: number;
  base: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const stored = useTerminalPrefs((s) => s.interval);
  const interval = (INTERVALS as string[]).includes(stored) ? (stored as CandleInterval) : "15m";
  const indicators = useTerminalPrefs((s) => s.indicators) as Indicator[];
  const showDepth = useTerminalPrefs((s) => s.showDepthChart);
  const set = useTerminalPrefs((s) => s.set);
  const { candles, hasMore, loading, loadMore } = useCandles(symbol, interval);

  return (
    <div className={cn("flex min-h-0 flex-col gap-px bg-line-1", className)}>
      <CandleChart
        className="min-h-0 flex-1"
        height="fill"
        symbol={symbol}
        candles={candles}
        interval={interval}
        priceDecimals={priceDecimals}
        loading={loading}
        hasMore={hasMore}
        onLoadMore={loadMore}
        intervals={INTERVALS}
        onIntervalChange={(i) => set({ interval: i })}
        indicators={indicators}
        onIndicatorsChange={(list) => set({ indicators: list })}
      />
      <div className="shrink-0 bg-bg-1">
        <button
          type="button"
          aria-expanded={showDepth}
          onClick={() => set({ showDepthChart: !showDepth })}
          className="flex h-8 w-full items-center gap-1 px-3 text-xs text-fg-3 transition-colors hover:text-fg-1"
        >
          <ChevronDown size={14} className={cn("transition-transform duration-[var(--t-fast)]", !showDepth && "-rotate-90")} />
          {t("pcTrade.depthChart")}
        </button>
        {showDepth && <DepthPlot symbol={symbol} priceDecimals={priceDecimals} qtyDecimals={qtyDecimals} base={base} />}
      </div>
    </div>
  );
}

// DepthPlot follows 100 levels a side only while the depth chart is open.
function DepthPlot({ symbol, priceDecimals, qtyDecimals, base }: { symbol: string; priceDecimals: number; qtyDecimals: number; base: string }) {
  const view = useOrderBook(symbol, 100);
  return <DepthChart view={view} priceDecimals={priceDecimals} qtyDecimals={qtyDecimals} base={base} height={180} className="px-2 pb-2" />;
}
