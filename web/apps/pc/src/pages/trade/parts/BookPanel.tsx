import { bookSteps, channels, useOrderBook, useSyncing, useTerminalPrefs, useTicker, useTrades, useTradesSeed } from "@exchange/core";
import { OrderBook, Tabs, TabsPanel, TradeTape, cn } from "@exchange/ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useDirection } from "./useDirection";
import { useElementHeight } from "./useElementHeight";

const ROW = 20;
// Tabs, the book's toolbar, its column header and the middle row.
const CHROME = 36 + 36 + 24 + 36;

export type BookPanelProps = {
  symbol: string;
  base: string;
  quote: string;
  tickSize: string;
  priceDecimals: number;
  qtyDecimals: number;
  /** Futures: the mark price under the last price. */
  markPrice?: string | null;
  /** A click on a price (Shift: with the cumulative quantity) fills the order form. */
  onPick: (price: string, quantity?: string) => void;
  className?: string;
};

/**
 * BookPanel: the order book and the latest trades in tabs (design §6.2),
 * sized to the panel's height (up to 20 levels a side), with the view and
 * the step kept per pair.
 */
export function BookPanel({ symbol, base, quote, tickSize, priceDecimals, qtyDecimals, markPrice, onPick, className }: BookPanelProps) {
  const { t } = useTranslation();
  const [tab, setTab] = useState("book");
  const [ref, height] = useElementHeight<HTMLDivElement>();
  const mode = useTerminalPrefs((s) => s.bookMode);
  const step = useTerminalPrefs((s) => s.bookStep[symbol] ?? "");
  const prefs = useTerminalPrefs.getState;
  const levels = Math.max(5, Math.min(20, Math.floor((height - CHROME) / 2 / ROW) || 12));
  const depth = mode === "both" ? levels : levels * 2;
  const view = useOrderBook(symbol, depth, step);
  const syncing = useSyncing(channels.depth(symbol));
  useTradesSeed(symbol);
  const trades = useTrades(symbol);
  const tk = useTicker(symbol);
  const dir = useDirection(tk?.last);
  const steps = bookSteps(tickSize);
  const loading = view.asks.length === 0 && view.bids.length === 0 && !tk;

  return (
    <div ref={ref} className={cn("flex min-h-0 flex-col overflow-hidden bg-bg-1", className)}>
      <Tabs
        value={tab}
        onValueChange={setTab}
        size="sm"
        className="flex min-h-0 flex-1 flex-col"
        listClassName="px-2"
        items={[
          { value: "book", label: t("pcTrade.book") },
          { value: "trades", label: t("pcTrade.trades") },
        ]}
      >
        <TabsPanel value="book" className="flex-1">
          <OrderBook
            view={view}
            priceDecimals={priceDecimals}
            qtyDecimals={qtyDecimals}
            levels={levels}
            mode={mode}
            onModeChange={(m) => prefs().set({ bookMode: m })}
            lastPrice={tk?.last}
            lastDirection={dir}
            markPrice={markPrice}
            steps={steps}
            step={step || steps[0]}
            onStepChange={(s) => prefs().setStep(symbol, s === steps[0] ? "" : s)}
            onPriceClick={onPick}
            syncing={syncing}
            loading={loading}
            base={base}
            quote={quote}
            rowHeight={ROW}
          />
        </TabsPanel>
        <TabsPanel value="trades" className="flex-1">
          <TradeTape
            trades={trades}
            priceDecimals={priceDecimals}
            qtyDecimals={qtyDecimals}
            max={Math.max(10, Math.floor((height - 36 - 24) / ROW))}
            base={base}
            quote={quote}
            onPriceClick={(p) => onPick(p)}
            loading={trades.length === 0 && !tk}
            rowHeight={ROW}
          />
        </TabsPanel>
      </Tabs>
    </div>
  );
}
