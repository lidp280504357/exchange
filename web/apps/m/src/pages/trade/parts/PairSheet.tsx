import { dec, errorText, formatPercent, formatPrice, pairName, useContracts, usePairs, useTickers } from "@exchange/core";
import { useFavorites } from "@exchange/core/markets/favorites";
import { searchMarkets } from "@exchange/core/markets/search";
import { CoinIcon, Input, Segmented, Sheet, cn, toast } from "@exchange/ui";
import { Search, Star } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

type Row = { symbol: string; base: string; quote: string; name: string; decimals: number; futures: boolean };

/**
 * PairSheet switches the terminal's market (design §7.2): a bottom sheet
 * with a search box, favourites and quote groups, and live prices from the
 * one tickers subscription.
 */
export function PairSheet({
  open, onOpenChange, current, onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  current: string;
  onPick: (symbol: string, futures: boolean) => void;
}) {
  const { t } = useTranslation();
  const pairs = usePairs();
  const contracts = useContracts();
  const tickers = useTickers();
  const favorites = useFavorites();
  const [query, setQuery] = useState("");
  const [group, setGroup] = useState("all");

  const rows = useMemo<Row[]>(() => {
    const spot = (pairs.data?.pairs ?? [])
      .filter((p) => p.status !== "DELISTED")
      .map((p) => ({ symbol: p.symbol, base: p.base_asset, quote: p.quote_asset, name: pairName(p), decimals: p.price_decimals, futures: false }));
    const perp = (contracts.data?.contracts ?? []).map((c) => ({
      symbol: c.symbol, base: c.base_asset, quote: c.quote_asset, name: t("m.perpetual"), decimals: dec.decimalsOf(c.tick_size), futures: true,
    }));
    return [...spot, ...perp];
  }, [pairs.data, contracts.data, t]);

  const shown = searchMarkets(
    rows.filter((r) => group === "all" || (group === "fav" ? favorites.has(r.symbol) : group === "perp" ? r.futures : !r.futures && r.quote === group)),
    query,
  );
  const quotes = [...new Set(rows.filter((r) => !r.futures).map((r) => r.quote))];

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={t("mTrade.switchPair")} bodyClassName="px-0">
      <div className="flex flex-col gap-3 px-4 pb-2">
        <Input
          size="lg"
          prefix={<Search size={16} className="text-fg-3" />}
          placeholder={t("nav.search")}
          aria-label={t("nav.search")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          enterKeyHint="search"
        />
        <Segmented
          size="sm"
          value={group}
          onValueChange={setGroup}
          items={[
            { value: "fav", label: t("market.favorites") },
            { value: "all", label: t("common.all") },
            ...quotes.map((q2) => ({ value: q2, label: q2 })),
            { value: "perp", label: t("market.futures") },
          ]}
        />
      </div>
      <div role="listbox" aria-label={t("mTrade.switchPair")} className="max-h-[60dvh] overflow-y-auto pb-2">
        {shown.length === 0 && <p className="px-4 py-10 text-center text-sm text-fg-3">{group === "fav" ? t("mTrade.noFavorites") : t("market.noResults")}</p>}
        {shown.map((r) => {
          const tk = tickers.get(r.symbol);
          const fav = favorites.has(r.symbol);
          return (
            <div key={r.symbol} className={cn("flex items-center gap-2 px-2", r.symbol === current && "bg-bg-2")}>
              <button
                type="button"
                aria-pressed={fav}
                aria-label={fav ? t("common.unfavorite") : t("common.favorite")}
                onClick={() => void favorites.toggle(r.symbol).catch((e: unknown) => toast.error(errorText(e)))}
                className={cn("grid size-tap shrink-0 place-items-center", fav ? "text-brand" : "text-fg-3")}
              >
                <Star size={16} className={cn(fav && "fill-current")} />
              </button>
              <button
                type="button"
                role="option"
                aria-selected={r.symbol === current}
                onClick={() => {
                  onOpenChange(false);
                  onPick(r.symbol, r.futures);
                }}
                className="flex min-h-14 flex-1 items-center justify-between gap-3 pr-3 text-left"
              >
                <span className="flex min-w-0 items-center gap-2">
                  <CoinIcon symbol={r.base} size={24} />
                  <span className="flex min-w-0 flex-col">
                    <span className="font-medium text-fg-1">
                      {r.base}
                      <span className="text-fg-3">/{r.quote}</span>
                    </span>
                    <span className="truncate text-xs text-fg-3">{r.name}</span>
                  </span>
                </span>
                <span className="flex flex-col items-end tabular-nums">
                  <span className="text-fg-1">{formatPrice(tk?.last, r.decimals)}</span>
                  <span className={cn("text-xs", tk?.change?.startsWith("-") ? "text-down" : "text-up")}>{formatPercent(tk?.change)}</span>
                </span>
              </button>
            </div>
          );
        })}
      </div>
    </Sheet>
  );
}
