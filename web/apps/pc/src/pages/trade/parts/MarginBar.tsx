import { formatPrice } from "@exchange/core";
import type { MarginActionKind } from "@exchange/core/margin/form";
import type { SideEffect, TradeAccount, useMarginTrade } from "@exchange/core/margin/trade";
import { Badge, MarginLevel, Segmented, Select } from "@exchange/ui";
import { useTranslation } from "react-i18next";

/**
 * MarginBar sits above the spot order form (margin design §7): the
 * account the order trades from (spot, cross, or the pair's isolated
 * account, as the pair allows), and in a margin account its leverage, its
 * margin level, the liquidation price of an isolated account, the
 * transfer, borrow and repay dialogs and the order's side effect.
 */
export function MarginBar({
  account, onAccount, effect, onEffect, trade, priceDecimals, onAct,
}: {
  account: TradeAccount;
  onAccount: (a: TradeAccount) => void;
  effect: SideEffect;
  onEffect: (e: SideEffect) => void;
  trade: ReturnType<typeof useMarginTrade>;
  priceDecimals: number;
  /** Opens the transfer, borrow or repay dialog of the account. */
  onAct: (kind: MarginActionKind) => void;
}) {
  const { t } = useTranslation();
  const items = [
    { value: "SPOT", label: t("pcTrade.margin.spot") },
    ...(trade.support.cross ? [{ value: "MARGIN_CROSS", label: t("pcTrade.margin.cross") }] : []),
    ...(trade.support.isolated ? [{ value: "MARGIN_ISOLATED", label: t("pcTrade.margin.isolated") }] : []),
  ];
  const owner = trade.owner;
  const terms = trade.terms;
  const link = (kind: MarginActionKind, label: string) => (
    <button type="button" onClick={() => onAct(kind)} className="text-xs font-medium text-brand hover:brightness-110">
      {label}
    </button>
  );
  return (
    <div className="flex flex-col gap-2.5 border-b border-line-1 px-4 pb-3 pt-3" data-testid="margin-bar">
      <Segmented
        block
        size="sm"
        value={account}
        onValueChange={(v) => onAccount(v as TradeAccount)}
        aria-label={t("pcTrade.margin.account")}
        items={items}
      />
      {account !== "SPOT" && terms && (
        <>
          <div className="flex items-center gap-2">
            <Badge tone="brand" size="sm">
              {owner?.leverage ?? terms.leverage}x
            </Badge>
            <MarginLevel
              level={owner?.margin_level ?? null}
              warn={owner?.warn_level ?? terms.warn_level}
              liquidation={owner?.liquidation_level ?? terms.liquidation_level}
              size="sm"
              compact
              className="flex-1"
            />
          </div>
          <div className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-3">
              {link("transfer", t("pcTrade.margin.transfer"))}
              {link("borrow", t("pcTrade.margin.borrow"))}
              {link("repay", t("pcTrade.margin.repay"))}
            </span>
            {account === "MARGIN_ISOLATED" && owner?.liquidation_price && (
              <span className="text-xs tabular-nums text-fg-3">{t("pcTrade.margin.liquidation", { price: formatPrice(owner.liquidation_price, priceDecimals) })}</span>
            )}
          </div>
          <Select
            size="sm"
            value={effect}
            onValueChange={(v) => onEffect(v as SideEffect)}
            aria-label={t("pcTrade.margin.effect")}
            options={(["NONE", "AUTO_BORROW", "AUTO_REPAY"] as const).map((e) => ({
              value: e,
              label: t(`pcTrade.margin.effects.${e}`),
              title: t(`pcTrade.margin.effectHints.${e}`),
            }))}
          />
        </>
      )}
    </div>
  );
}
