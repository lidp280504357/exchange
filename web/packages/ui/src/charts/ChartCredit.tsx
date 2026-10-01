import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

/**
 * ChartCredit is the attribution the chart library's licence asks for: the
 * notice of its NOTICE file and a link to TradingView, shown once on each
 * site (the PC footer, the mobile help page) instead of a logo on every
 * chart (CandleChart turns that off).
 */
export function ChartCredit({ className }: { className?: string }) {
  const { t } = useTranslation();
  return (
    <p className={cn("text-xs text-fg-3", className)}>
      {t("ui.chart.credit")}
      <a href="https://www.tradingview.com/" target="_blank" rel="noopener noreferrer" className="underline-offset-2 hover:text-fg-2 hover:underline">
        TradingView Lightweight Charts™
      </a>{" "}
      · Copyright © 2025 TradingView, Inc.
    </p>
  );
}
