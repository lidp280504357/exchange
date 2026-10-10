import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/cn";

export type CrossLiquidatingNoticeProps = {
  /** The settlement asset whose cross positions are being liquidated: USDT, or a coin-margined contract's coin. */
  asset: string;
  /** What the place it sits in cannot do meanwhile: place orders, add isolated margin, transfer out. */
  stops: "orders" | "margin" | "transfer";
  className?: string;
};

/**
 * CrossLiquidatingNotice says an account's cross positions are being
 * liquidated (C68, F24): from the take-over until what the liquidation
 * leaves has gone to the insurance fund as the clearance fee, seconds as a
 * rule. Meanwhile the server refuses cross and opening orders, isolated
 * margin added and transfers out of the asset's futures account
 * (DERIV_POSITION_LIQUIDATING); the notice says which of them stops where
 * it sits.
 */
export function CrossLiquidatingNotice({ asset, stops, className }: CrossLiquidatingNoticeProps) {
  const { t } = useTranslation();
  return (
    <div role="status" data-testid="cross-liquidating" className={cn("flex items-start gap-2 rounded-2 border border-danger/40 bg-danger/10 px-3 py-2 text-xs", className)}>
      <TriangleAlert size={14} aria-hidden className="mt-0.5 shrink-0 text-danger" />
      <p className="min-w-0 leading-relaxed text-fg-1">
        <span className="font-medium">{t("ui.crossLiquidating.title")}</span> · {t(`ui.crossLiquidating.${stops}`, { asset })}
      </p>
    </div>
  );
}
