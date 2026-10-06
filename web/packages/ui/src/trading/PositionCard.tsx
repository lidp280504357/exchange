import { enumLabel, formatAmount, formatDecimal, formatPercent, formatPrice } from "@exchange/core";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "../components/Badge";
import { Button } from "../components/Button";
import { toneOf } from "../components/PriceText";
import { cn } from "../lib/cn";

/** A futures position as the derivatives API returns it (decimal strings). */
export type Position = {
  symbol: string;
  side: "LONG" | "SHORT";
  quantity: string;
  entryPrice: string;
  markPrice: string;
  liquidationPrice: string | null;
  margin: string;
  leverage: string | number;
  unrealizedPnl: string;
  /** Return on margin as a fraction ("0.1234" = +12.34%). */
  roe: string;
  marginMode?: "CROSS" | "ISOLATED";
};

export type PositionCardProps = {
  position: Position;
  priceDecimals: number;
  qtyDecimals: number;
  /** The margin asset's decimals (default 2 for USDT). */
  quoteDecimals?: number;
  base?: string;
  quote?: string;
  onClose?: () => void;
  onTpSl?: () => void;
  onAdjustMargin?: () => void;
  /** Extra content under the fields (open TP/SL orders). */
  children?: ReactNode;
  className?: string;
};

/**
 * PositionCard shows one futures position: side and leverage, size, entry,
 * mark and liquidation prices, margin, and the unrealized PnL with ROE
 * coloured by sign; close, TP/SL and margin actions go to the page.
 */
export function PositionCard({
  position: p, priceDecimals, qtyDecimals, quoteDecimals = 2, base, quote = "USDT", onClose, onTpSl, onAdjustMargin, children, className,
}: PositionCardProps) {
  const { t } = useTranslation();
  const long = p.side === "LONG";
  const pnlTone = toneOf(p.unrealizedPnl);
  const pnlClass = pnlTone === "up" ? "text-up" : pnlTone === "down" ? "text-down" : "text-fg-1";
  const field = (label: ReactNode, value: ReactNode, extra?: string) => (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-xs text-fg-3">{label}</span>
      <span className={cn("truncate text-sm tabular-nums text-fg-1", extra)}>{value}</span>
    </div>
  );
  return (
    <article className={cn("flex flex-col gap-3 rounded-3 border border-line-1 bg-bg-1 p-4", className)}>
      <header className="flex items-center gap-2">
        <span className={cn("h-4 w-1 rounded-full", long ? "bg-up" : "bg-down")} aria-hidden />
        <span className="font-semibold">{p.symbol}</span>
        <Badge tone={long ? "up" : "down"}>{enumLabel(p.side)}</Badge>
        {p.marginMode && <Badge tone="neutral">{enumLabel(p.marginMode)}</Badge>}
        <Badge tone="brand">{String(p.leverage)}x</Badge>
      </header>
      <div className="flex items-end justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <span className="text-xs text-fg-3">
            {t("ui.position.pnl")} ({quote})
          </span>
          <span className={cn("text-lg font-semibold tabular-nums", pnlClass)}>{formatDecimal(p.unrealizedPnl, { decimals: quoteDecimals, rounding: "half", sign: true })}</span>
        </div>
        <div className="flex flex-col items-end gap-0.5">
          <span className="text-xs text-fg-3">{t("ui.position.roe")}</span>
          <span className={cn("text-md font-semibold tabular-nums", pnlClass)}>{formatPercent(p.roe)}</span>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-x-3 gap-y-2.5">
        {field(`${t("ui.position.size")}${base ? ` (${base})` : ""}`, formatAmount(p.quantity, qtyDecimals))}
        {field(t("ui.position.entry"), formatPrice(p.entryPrice, priceDecimals))}
        {field(t("ui.position.mark"), formatPrice(p.markPrice, priceDecimals))}
        {field(t("ui.position.liq"), formatPrice(p.liquidationPrice, priceDecimals), "text-warn")}
        {field(`${t("ui.position.margin")} (${quote})`, formatAmount(p.margin, quoteDecimals))}
      </div>
      {children}
      {(onClose || onTpSl || onAdjustMargin) && (
        // At the card's bottom: cards side by side in a grid row are as tall as the tallest, their actions on one line (review B134).
        <footer className="mt-auto grid grid-cols-3 gap-2">
          {onAdjustMargin && (
            <Button type="button" size="sm" variant="secondary" onClick={onAdjustMargin}>
              {t("ui.position.adjustMargin")}
            </Button>
          )}
          {onTpSl && (
            <Button type="button" size="sm" variant="secondary" onClick={onTpSl}>
              {t("ui.position.tpsl")}
            </Button>
          )}
          {onClose && (
            <Button type="button" size="sm" variant="secondary" onClick={onClose} className="col-start-3">
              {t("ui.position.close")}
            </Button>
          )}
        </footer>
      )}
    </article>
  );
}
