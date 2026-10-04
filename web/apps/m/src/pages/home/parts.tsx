import { coinProfile, enumLabel, errorText, routes, useSettings } from "@exchange/core";
import { useFavorites, useInView, type MarketRow } from "@exchange/core/markets/index";
import { Badge, CoinIcon, Skeleton, Sparkline, Tag, cn, toast } from "@exchange/ui";
import { ChevronRight } from "lucide-react";
import { useCallback, useEffect, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useDaySpark } from "./spark";

// Pieces the mobile home page, the markets list and the coin page share.

/** useCoinName returns a coin's name in the user's language (its profile), else the API's. */
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

/** statusTone colours the badge of a market that is not trading. */
export function statusTone(status: string): "info" | "warn" | "danger" | "neutral" {
  if (status === "PREPARE") return "info";
  if (status === "HALT" || status === "CANCEL_ONLY") return "warn";
  if (status === "DELISTED") return "danger";
  return "neutral";
}

/** MarketName is a market's icon, symbol (with its quote or the perpetual tag), status and coin name. */
export function MarketName({ row, size = 28, hideName }: { row: MarketRow; size?: number; hideName?: boolean }) {
  const { t } = useTranslation();
  const nameOf = useCoinName();
  const code = row.kind === "perp" ? `${row.base}${row.quote}` : row.base;
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <CoinIcon symbol={row.base} size={size} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1 overflow-hidden whitespace-nowrap">
          <span className="min-w-0 truncate font-medium text-fg-1" title={code}>
            {code}
          </span>
          {row.kind === "spot" ? (
            <span className="shrink-0 text-xs text-fg-3">/{row.quote}</span>
          ) : (
            <Tag tone="brand">
              {t("mMarkets.perp")}
            </Tag>
          )}
        </div>
        {/* A status sits on the second line: on the first it left a 360 px screen "S… /BTC 即将上线". */}
        {(!hideName || row.status !== "TRADING") && (
          <div className="flex min-w-0 items-center gap-1">
            {row.status !== "TRADING" && <Badge tone={statusTone(row.status)}>{enumLabel(row.status)}</Badge>}
            {!hideName && <span className="min-w-0 truncate text-xs text-fg-3">{nameOf(row)}</span>}
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * DaySpark draws a market's 24-hour line once it scrolls into view (the
 * candles are fetched then and shared by every list showing the market).
 */
export function DaySpark({ symbol, width, height, fluid, className }: { symbol: string; width: number; height: number; fluid?: boolean; className?: string }) {
  const [ref, seen] = useInView<HTMLDivElement>();
  const q = useDaySpark(symbol, seen);
  return (
    <div ref={ref} aria-hidden className={cn("flex shrink-0 items-center justify-end", className)} style={{ width: fluid ? "100%" : width, height }}>
      {q.data && q.data.length > 1 ? (
        <Sparkline data={q.data} width={width} height={height} fluid={fluid} />
      ) : q.isError || (q.data && q.data.length <= 1) ? (
        <span className="text-xs text-fg-3">—</span>
      ) : (
        <Skeleton className="h-3/5 w-full" />
      )}
    </div>
  );
}

/**
 * useFavoriteToggle wraps the favourites for the pages: a stable toggle
 * that waits for a signed-in user's list, reports a refused save and, when
 * asked (a long press shows nothing else), confirms the change.
 */
export function useFavoriteToggle() {
  const { t } = useTranslation();
  const fav = useFavorites();
  const ref = useRef(fav);
  useEffect(() => {
    ref.current = fav;
  });
  const toggle = useCallback(
    (symbol: string, announce = false) => {
      const f = ref.current;
      if (!f.ready) {
        toast.info(t("mMarkets.favoriteLoading"), { id: "m-favorite" });
        return;
      }
      const adding = !f.has(symbol);
      f.toggle(symbol).then(
        () => {
          if (announce) toast.success(adding ? t("mMarkets.favoriteAdded") : t("mMarkets.favoriteRemoved"), { id: "m-favorite", duration: 1800 });
        },
        (err: unknown) => toast.error(t("mMarkets.favoriteFailed"), { id: "m-favorite", description: errorText(err) }),
      );
    },
    [t],
  );
  return { symbols: fav.symbols, has: fav.has, ready: fav.ready, toggle };
}

/** SectionHead is a home section's title with an optional "all" link (a 44 px target). */
export function SectionHead({ title, to, more, extra }: { title: ReactNode; to?: string; more?: string; extra?: ReactNode }) {
  return (
    <div className="flex min-h-tap items-center justify-between gap-2">
      <h2 className="min-w-0 truncate text-md font-semibold text-fg-1">{title}</h2>
      {extra}
      {to && more && (
        <Link to={to} className="-mr-2 flex h-tap shrink-0 items-center gap-0.5 rounded-2 px-2 text-sm text-fg-3 transition-colors active:text-fg-1">
          {more}
          <ChevronRight size={16} />
        </Link>
      )}
    </div>
  );
}
