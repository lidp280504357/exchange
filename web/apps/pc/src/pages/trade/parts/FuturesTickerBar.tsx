import { dec, enumLabel, errorText, formatCompact, formatPercent, formatPrice, useMarkPrice, useSettings, useTicker, useTickerSeed, type Contract } from "@exchange/core";
import { useFavorites } from "@exchange/core/markets/favorites";
import { Badge, FundingCountdown, PriceText, cn, toast } from "@exchange/ui";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { FavoriteStar } from "../../../features/markets/FavoriteStar";
import { PairPicker } from "./PairPicker";
import { Stat } from "./TickerBar";
import { useDirection } from "./useDirection";

/**
 * FuturesTickerBar: the contract switcher, the last price, the mark and
 * index prices, the funding rate with its countdown (the only element
 * redrawn every second) and the 24-hour statistics.
 */
export function FuturesTickerBar({ contract, onPick }: { contract: Contract; onPick: (symbol: string) => void }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const fav = useFavorites();
  const decimals = dec.decimalsOf(contract.tick_size);
  useTickerSeed(contract.symbol);
  const tk = useTicker(contract.symbol);
  const mark = useMarkPrice(contract.symbol).data;
  const dir = useDirection(tk?.last);
  const down = tk?.change?.startsWith("-");

  useEffect(() => {
    const price = tk?.last ? formatPrice(tk.last, decimals) : "";
    document.title = `${price ? `${price} | ` : ""}${contract.base_asset}${contract.quote_asset} ${t("pcTrade.perpetual")} | Astras`;
  }, [tk?.last, contract, decimals, t]);
  useEffect(() => () => void (document.title = "Astras"), []);

  return (
    <div className="flex h-14 shrink-0 items-center gap-6 overflow-x-auto bg-bg-1 px-3 [scrollbar-width:none]">
      <PairPicker
        kind="futures"
        current={contract.symbol}
        onPick={onPick}
        label={{ base: contract.base_asset, quote: contract.quote_asset, suffix: t("pcTrade.perpetual") }}
      />
      <FavoriteStar
        active={fav.has(contract.symbol)}
        onToggle={() => void fav.toggle(contract.symbol).catch((e: unknown) => toast.error(errorText(e)))}
        label={fav.has(contract.symbol) ? t("common.unfavorite") : t("common.favorite")}
        className="-ml-4"
      />
      {contract.status !== "TRADING" && <Badge tone="warn">{enumLabel(contract.status)}</Badge>}
      <PriceText value={tk?.last} decimals={decimals} tone={dir ?? "neutral"} className="text-lg font-semibold" />
      <Stat label={t("pcTrade.markPrice")}>{formatPrice(mark?.mark_price, decimals)}</Stat>
      <Stat label={t("pcTrade.indexPrice")}>{formatPrice(mark?.index_price, decimals)}</Stat>
      <FundingCountdown nextFundingTime={mark?.next_funding_time} rate={mark?.funding_rate} layout="stacked" />
      <Stat label={t("market.change")}>
        <span className={cn(tk?.change ? (down ? "text-down" : "text-up") : "text-fg-2")}>{formatPercent(tk?.change)}</span>
      </Stat>
      <Stat label={t("market.high")}>{formatPrice(tk?.high, decimals)}</Stat>
      <Stat label={t("market.low")}>{formatPrice(tk?.low, decimals)}</Stat>
      <Stat label={`${t("market.turnover")}(${contract.quote_asset})`}>{formatCompact(tk?.quote_volume, locale)}</Stat>
    </div>
  );
}
