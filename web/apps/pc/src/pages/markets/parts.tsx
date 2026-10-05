import { coinProfile, enumLabel, errorText, routes, useSettings } from "@exchange/core";
import { useIsolatedLeverage } from "@exchange/core/margin/hooks";
import { useFavorites, useInView, useSparkline, type MarketRow } from "@exchange/core/markets/index";
import { Badge, CoinIcon, Skeleton, Sparkline, Tag, cn, toast } from "@exchange/ui";
import { useCallback, useRef } from "react";
import { useTranslation } from "react-i18next";

// Pieces the home page, the markets table and the coin page share.

/** displayName is a coin's name in the user's language (profile), else the API's. */
export function useCoinName(): (row: Pick<MarketRow, "base" | "name">) => string {
  const locale = useSettings((s) => s.locale);
  return useCallback((row) => coinProfile(row.base)?.name[locale] ?? row.name, [locale]);
}

/** marketLabel is how a market is written: "BTC/USDT", or "BTCUSDT" for a perpetual. */
export function marketLabel(row: Pick<MarketRow, "base" | "quote" | "kind">): string {
  return row.kind === "perp" ? `${row.base}${row.quote}` : `${row.base}/${row.quote}`;
}

/** tradePath is where a market trades: the spot terminal or the futures one. */
export function tradePath(row: Pick<MarketRow, "kind" | "symbol">): string {
  return row.kind === "perp" ? routes.futures(row.symbol) : routes.trade(row.symbol);
}

/** statusTone colours a pair status badge. */
export function statusTone(status: string): "info" | "warn" | "danger" | "neutral" {
  if (status === "PREPARE") return "info";
  if (status === "HALT" || status === "CANCEL_ONLY") return "warn";
  if (status === "DELISTED") return "danger";
  return "neutral";
}

/** MarketName is a market's icon, symbol, perpetual tag, isolated margin leverage, status and coin name. */
export function MarketName({ row, size = 28, hideName }: { row: MarketRow; size?: number; hideName?: boolean }) {
  const { t } = useTranslation();
  const nameOf = useCoinName();
  const leverage = useIsolatedLeverage().get(row.symbol);
  return (
    <div className="flex min-w-0 items-center gap-3">
      <CoinIcon symbol={row.base} size={size} />
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-1.5 overflow-hidden whitespace-nowrap">
          <span className="font-medium text-fg-1">{row.kind === "perp" ? `${row.base}${row.quote}` : row.base}</span>
          {row.kind === "spot" ? (
            <span className="text-fg-3">/{row.quote}</span>
          ) : (
            <Tag tone="brand">
              {t("pcMarkets.perp")}
            </Tag>
          )}
          {row.kind === "spot" && leverage && (
            <Tag tone="neutral" title={t("pcMarkets.isolatedLeverage", { n: leverage })}>
              {leverage}x
            </Tag>
          )}
          {row.status !== "TRADING" && (
            <Badge tone={statusTone(row.status)}>
              {enumLabel(row.status)}
            </Badge>
          )}
        </div>
        {!hideName && <div className="truncate text-xs text-fg-3">{nameOf(row)}</div>}
      </div>
    </div>
  );
}

/**
 * SparkCell draws a market's 7-day line once the cell scrolls into view;
 * the candles are fetched then and cached for every list.
 */
export function SparkCell({ symbol, width = 96, height = 32, className }: { symbol: string; width?: number; height?: number; className?: string }) {
  const [ref, seen] = useInView<HTMLDivElement>();
  const q = useSparkline(symbol, seen);
  return (
    <div ref={ref} className={cn("flex items-center justify-end", className)} style={{ width, height }}>
      {q.data && q.data.length > 1 ? (
        <Sparkline data={q.data} width={width} height={height} />
      ) : q.isError || (q.data && q.data.length <= 1) ? (
        <span className="text-xs text-fg-3">—</span>
      ) : (
        <Skeleton className="h-4/5 w-full" />
      )}
    </div>
  );
}

/**
 * useFavoriteToggle wraps the favourites for pages: a stable toggle that
 * waits for a signed-in user's list and reports a refused save.
 */
export function useFavoriteToggle() {
  const { t } = useTranslation();
  const fav = useFavorites();
  const ref = useRef(fav);
  ref.current = fav;
  const toggle = useCallback(
    (symbol: string) => {
      const f = ref.current;
      if (!f.ready) {
        toast.info(t("pcMarkets.favoriteLoading"));
        return;
      }
      f.toggle(symbol).catch((err: unknown) => toast.error(t("pcMarkets.favoriteFailed"), { description: errorText(err) }));
    },
    [t],
  );
  return { symbols: fav.symbols, has: fav.has, ready: fav.ready, toggle };
}
