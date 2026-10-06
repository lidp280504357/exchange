import { formatAmount, formatPrice } from "@exchange/core";
import type { MarginActionKind } from "@exchange/core/margin/form";
import type { SideEffect, TradeAccount, useMarginTrade } from "@exchange/core/margin/trade";
import { Badge, MarginLevel, Segmented, Select, Skeleton } from "@exchange/ui";
import { useTranslation } from "react-i18next";

/**
 * MarginBar sits above the spot order form (margin design §7), compact as
 * Binance's (B120): the account the order trades from (spot, cross, or the
 * pair's isolated account, as the pair allows) and, in a margin account,
 * one row of its leverage, the order's side effect and the transfer,
 * borrow and repay dialogs. What it may borrow, an isolated account's
 * liquidation price and its margin level go under the form, beside the
 * available balance (MarginInfo). The panel's 12 px insets hold in every
 * account; the divider under the bar runs to the panel's edges.
 */
export function MarginBar({
  account, onAccount, effect, onEffect, trade, onAct,
}: {
  account: TradeAccount;
  onAccount: (a: TradeAccount) => void;
  effect: SideEffect;
  onEffect: (e: SideEffect) => void;
  trade: ReturnType<typeof useMarginTrade>;
  /** Opens the transfer, borrow or repay dialog of the account. */
  onAct: (kind: MarginActionKind) => void;
}) {
  const { t } = useTranslation();
  const items = [
    { value: "SPOT", label: t("pcTrade.margin.spot") },
    ...(trade.support.cross ? [{ value: "MARGIN_CROSS", label: t("pcTrade.margin.cross") }] : []),
    ...(trade.support.isolated ? [{ value: "MARGIN_ISOLATED", label: t("pcTrade.margin.isolated") }] : []),
  ];
  const terms = trade.terms;
  const link = (kind: MarginActionKind, label: string) => (
    <button type="button" onClick={() => onAct(kind)} className="text-xs font-medium text-brand hover:brightness-110">
      {label}
    </button>
  );
  return (
    <div className="-mx-3 flex flex-col gap-2.5 border-b border-line-1 px-3 pb-3" data-testid="margin-bar">
      <Segmented
        block
        size="sm"
        value={account}
        onValueChange={(v) => onAccount(v as TradeAccount)}
        aria-label={t("pcTrade.margin.account")}
        items={items}
      />
      {account !== "SPOT" && terms && (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
          <Badge tone="brand" size="sm">
            {trade.owner?.leverage ?? terms.leverage}x
          </Badge>
          <Select
            size="sm"
            value={effect}
            onValueChange={(v) => onEffect(v as SideEffect)}
            aria-label={t("pcTrade.margin.effect")}
            className="min-w-24"
            options={(["NONE", "AUTO_BORROW", "AUTO_REPAY"] as const).map((e) => ({
              value: e,
              label: t(`pcTrade.margin.effects.${e}`),
              title: t(`pcTrade.margin.effectHints.${e}`),
            }))}
          />
          <span className="ml-auto flex items-center gap-3">
            {link("transfer", t("pcTrade.margin.transfer"))}
            {link("borrow", t("pcTrade.margin.borrow"))}
            {link("repay", t("pcTrade.margin.repay"))}
          </span>
        </div>
      )}
    </div>
  );
}

/**
 * MarginInfo is a margin account's lines under the order form's available
 * balance (OrderForm's info, B120): what it may borrow of the asset the
 * side spends (design §7 "可借额度"), an isolated account's liquidation
 * price and its margin level.
 */
export function MarginInfo({
  account, trade, priceDecimals, borrowable,
}: {
  account: TradeAccount;
  trade: ReturnType<typeof useMarginTrade>;
  priceDecimals: number;
  borrowable: { asset: string; amount: string | undefined; decimals: number };
}) {
  const { t } = useTranslation();
  const owner = trade.owner;
  const terms = trade.terms;
  if (account === "SPOT" || !terms) return null;
  return (
    <div className="flex flex-col gap-1.5" data-testid="margin-info">
      <div className="flex items-center justify-between gap-2">
        <span className="text-fg-3">{t("pcTrade.margin.borrowable")}</span>
        <span className="tabular-nums text-fg-1" data-testid="margin-borrowable">
          {borrowable.amount === undefined ? "—" : formatAmount(borrowable.amount, borrowable.decimals)} {borrowable.asset}
        </span>
      </div>
      {account === "MARGIN_ISOLATED" && owner?.liquidation_price && (
        <div className="flex items-center justify-between gap-2">
          <span className="text-fg-3">{t("pcTrade.margin.liquidationPrice")}</span>
          <span className="tabular-nums text-fg-1">{formatPrice(owner.liquidation_price, priceDecimals)}</span>
        </div>
      )}
      {trade.pending ? (
        <Skeleton className="h-8" />
      ) : (
        <MarginLevel
          level={owner?.margin_level ?? null}
          warn={owner?.warn_level ?? terms.warn_level}
          liquidation={owner?.liquidation_level ?? terms.liquidation_level}
          size="sm"
          compact
        />
      )}
    </div>
  );
}
