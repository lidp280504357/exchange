import { dec, errorText, isInverse, routes, useContract, useMarkPrice, useTerminalPrefs } from "@exchange/core";
import { Button, EmptyState, ErrorState } from "@exchange/ui";
import { TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";
import { BookPanel } from "./parts/BookPanel";
import { ChartPanel } from "./parts/ChartPanel";
import { FuturesOrderPanel } from "./parts/FuturesOrderPanel";
import { FuturesPanel, type FuturesTab } from "./parts/FuturesPanel";
import { FuturesTickerBar } from "./parts/FuturesTickerBar";
import { PanelResizer } from "./parts/PanelResizer";
import { TerminalSkeleton } from "./SpotTerminal";

/**
 * The futures terminal (design §6.2): the spot terminal's layout with the
 * mark and index prices, the funding countdown, margin mode and leverage,
 * open/close, positions with take-profit/stop-loss, and a yellow banner
 * while the contract is reduce-only (degraded).
 */
export default function FuturesTerminal() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { symbol = "" } = useParams();
  const { contract, isPending, error, refetch } = useContract(symbol);
  const mark = useMarkPrice(contract?.symbol ?? "").data;
  const visit = useTerminalPrefs((s) => s.visit);
  const panelHeight = useTerminalPrefs((s) => s.panelHeight);
  const [fill, setFill] = useState<{ price?: string; quantity?: string } | null>(null);
  const [tab, setTab] = useState<FuturesTab>("positions");

  useEffect(() => {
    if (contract) visit(contract.symbol);
  }, [contract, visit]);
  useEffect(() => {
    if (contract && symbol !== contract.symbol) navigate(routes.futures(contract.symbol), { replace: true });
  }, [contract, symbol, navigate]);
  const onPick = useCallback((price: string, quantity?: string) => setFill({ price, quantity }), []);

  if (isPending) return <TerminalSkeleton />;
  if (error) return <ErrorState className="flex-1" message={errorText(error)} onRetry={() => void refetch()} />;
  if (!contract) {
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

  const priceDecimals = dec.decimalsOf(contract.tick_size);
  const qtyDecimals = dec.decimalsOf(contract.lot_size);
  // A coin-margined contract's book and trades count whole contracts.
  const qtyUnit = isInverse(contract) ? t("pcTrade.contractsUnit") : contract.base_asset;
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-px bg-line-1">
      {mark?.degraded && (
        <div role="status" className="flex items-center justify-center gap-2 bg-warn px-4 py-1.5 text-sm text-brand-fg">
          <TriangleAlert size={16} />
          {t("pcTrade.degraded")}
        </div>
      )}
      <FuturesTickerBar contract={contract} onPick={(s) => navigate(routes.futures(s))} />
      <div className="grid min-h-0 flex-1 grid-cols-[minmax(260px,22%)_minmax(0,1fr)_minmax(290px,24%)] gap-px">
        <BookPanel
          symbol={contract.symbol}
          base={qtyUnit}
          quote={contract.quote_asset}
          tickSize={contract.tick_size}
          priceDecimals={priceDecimals}
          qtyDecimals={qtyDecimals}
          markPrice={mark?.mark_price}
          onPick={onPick}
        />
        <ChartPanel symbol={contract.symbol} priceDecimals={priceDecimals} qtyDecimals={qtyDecimals} base={qtyUnit} />
        <FuturesOrderPanel contract={contract} fill={fill} className="overflow-y-auto" />
      </div>
      <PanelResizer />
      <FuturesPanel contract={contract} tab={tab} onTabChange={setTab} height={panelHeight} className="shrink-0" />
    </div>
  );
}
