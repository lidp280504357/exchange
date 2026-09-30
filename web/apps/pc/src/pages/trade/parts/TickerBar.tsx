import { enumLabel, errorText, formatCompact, formatPercent, formatPrice, useSettings, useTicker, useTickerSeed, type Pair } from "@exchange/core";
import { useFavorites } from "@exchange/core/markets/favorites";
import { Badge, PriceText, Tooltip, cn, toast } from "@exchange/ui";
import { TriangleAlert } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { FavoriteStar } from "../../../features/markets/FavoriteStar";
import { PairPicker } from "./PairPicker";
import { useDirection } from "./useDirection";

/** A reference ticker older than this shows a warning (market-data runbook). */
const STALE_MS = 30_000;

/**
 * SpotTickerBar: the pair switcher, the last price in its last move's
 * colour, and the 24-hour statistics, all from the pair's ticker channel.
 */
export function SpotTickerBar({ pair, onPick, extra }: { pair: Pair; onPick: (symbol: string) => void; extra?: ReactNode }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const fav = useFavorites();
  useTickerSeed(pair.symbol);
  const tk = useTicker(pair.symbol);
  const dir = useDirection(tk?.last);
  const stale = tk?.updated_at ? Date.now() - Date.parse(tk.updated_at) > STALE_MS : false;
  const down = tk?.change?.startsWith("-");

  useEffect(() => {
    const price = tk?.last ? formatPrice(tk.last, pair.price_decimals) : "";
    document.title = `${price ? `${price} | ` : ""}${pair.base_asset}/${pair.quote_asset} | Astras`;
  }, [tk?.last, pair]);
  useEffect(() => () => void (document.title = "Astras"), []);

  return (
    <div className="flex h-14 shrink-0 items-center gap-6 bg-bg-1 px-3">
      <PairPicker kind="spot" current={pair.symbol} onPick={onPick} label={{ base: pair.base_asset, quote: pair.quote_asset }} />
      <FavoriteStar
        active={fav.has(pair.symbol)}
        onToggle={() => void fav.toggle(pair.symbol).catch((e: unknown) => toast.error(errorText(e)))}
        label={fav.has(pair.symbol) ? t("common.unfavorite") : t("common.favorite")}
        className="-ml-4"
      />
      {pair.status !== "TRADING" && <Badge tone="warn">{enumLabel(pair.status)}</Badge>}
      <div className="flex flex-col leading-tight">
        <PriceText value={tk?.last} decimals={pair.price_decimals} tone={dir ?? "neutral"} className="text-lg font-semibold" />
        <span className="text-xs text-fg-3">{pair.base_name}</span>
      </div>
      <Stat label={t("market.change")}>
        <span className={cn(tk?.change ? (down ? "text-down" : "text-up") : "text-fg-2")}>{formatPercent(tk?.change)}</span>
      </Stat>
      <Stat label={t("market.high")}>{formatPrice(tk?.high, pair.price_decimals)}</Stat>
      <Stat label={t("market.low")}>{formatPrice(tk?.low, pair.price_decimals)}</Stat>
      <Stat label={`${t("market.volume")}(${pair.base_asset})`}>{formatCompact(tk?.volume, locale)}</Stat>
      <Stat label={`${t("market.turnover")}(${pair.quote_asset})`}>{formatCompact(tk?.quote_volume, locale)}</Stat>
      {stale && (
        <Tooltip content={t("common.feedStale")}>
          <span className="flex items-center gap-1 text-xs text-warn">
            <TriangleAlert size={14} />
            {t("pcTrade.stale")}
          </span>
        </Tooltip>
      )}
      {extra && <div className="ml-auto flex items-center gap-2">{extra}</div>}
    </div>
  );
}

export function Stat({ label, children, className }: { label: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col text-xs leading-tight", className)}>
      <span className="text-fg-3">{label}</span>
      <span className="mt-1 tabular-nums text-fg-1">{children}</span>
    </div>
  );
}
