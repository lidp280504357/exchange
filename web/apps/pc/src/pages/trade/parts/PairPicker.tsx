import { dec, errorText, formatPercent, formatPrice, pairName, useContracts, usePairs, useTickers } from "@exchange/core";
import { useFavorites } from "@exchange/core/markets/favorites";
import { searchMarkets } from "@exchange/core/markets/search";
import { CoinIcon, Input, Popover, Segmented, cn, toast } from "@exchange/ui";
import { ChevronDown, Search } from "lucide-react";
import { useMemo, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { FavoriteStar } from "../../../features/markets/FavoriteStar";

type Row = { symbol: string; base: string; quote: string; name: string; decimals: number };

/**
 * PairPicker switches the terminal's market (design §6.2): a popover with
 * a search box, the quote groups and the live prices of every market from
 * the one tickers subscription; arrow keys move, Enter picks.
 */
export function PairPicker({
  kind, current, onPick, label,
}: {
  kind: "spot" | "futures";
  current: string;
  onPick: (symbol: string) => void;
  label: { base: string; quote: string; suffix?: string };
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const pairs = usePairs();
  const contracts = useContracts();
  const tickers = useTickers();
  const favorites = useFavorites();
  const [query, setQuery] = useState("");
  const [group, setGroup] = useState("all");
  const [active, setActive] = useState(0);

  const rows = useMemo<Row[]>(() => {
    if (kind === "futures") {
      return (contracts.data?.contracts ?? []).map((c) => ({
        symbol: c.symbol, base: c.base_asset, quote: c.quote_asset, name: t("pcTrade.perpetual"), decimals: dec.decimalsOf(c.tick_size),
      }));
    }
    return (pairs.data?.pairs ?? [])
      .filter((p) => p.status !== "DELISTED")
      .map((p) => ({ symbol: p.symbol, base: p.base_asset, quote: p.quote_asset, name: pairName(p), decimals: p.price_decimals }));
  }, [kind, pairs.data, contracts.data, t]);

  const groups = useMemo(() => ["fav", "all", ...new Set(rows.map((r) => r.quote))], [rows]);
  const shown = searchMarkets(
    rows.filter((r) => group === "all" || (group === "fav" ? favorites.has(r.symbol) : r.quote === group)),
    query,
  );
  const toggle = (symbol: string) => favorites.toggle(symbol).catch((e: unknown) => toast.error(errorText(e)));

  const pick = (symbol: string) => {
    setOpen(false);
    setQuery("");
    if (symbol !== current) onPick(symbol);
  };
  const keyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(0, Math.min(shown.length - 1, a + (e.key === "ArrowDown" ? 1 : -1))));
    } else if (e.key === "Enter" && shown[active]) {
      e.preventDefault();
      pick(shown[active].symbol);
    }
  };

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        setActive(0);
      }}
      align="start"
      className="w-[380px] p-0"
      aria-label={t("pcTrade.switchPair")}
      trigger={
        <button type="button" className="flex items-center gap-2 rounded-2 px-2 py-1 transition-colors hover:bg-bg-2" aria-label={t("pcTrade.switchPair")}>
          <CoinIcon symbol={label.base} size={28} />
          <span className="text-lg font-semibold text-fg-1">
            {label.base}
            <span className="text-fg-3">/{label.quote}</span>
            {label.suffix && <span className="ml-1.5 align-middle text-xs font-normal text-fg-3">{label.suffix}</span>}
          </span>
          <ChevronDown size={16} className={cn("text-fg-3 transition-transform duration-[var(--t-fast)]", open && "rotate-180")} />
        </button>
      }
    >
      <div className="flex flex-col gap-2 p-3">
        <Input
          size="sm"
          autoFocus
          prefix={<Search size={14} className="text-fg-3" />}
          placeholder={t("nav.search")}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={keyDown}
          aria-label={t("nav.search")}
        />
        {groups.length > 1 && (
          <Segmented
            size="sm"
            value={group}
            onValueChange={(g) => {
              setGroup(g);
              setActive(0);
            }}
            items={groups.map((g) => ({ value: g, label: g === "all" ? t("common.all") : g === "fav" ? t("market.favorites") : g }))}
          />
        )}
      </div>
      <div className="grid grid-cols-[1fr_auto_auto] gap-x-4 border-t border-line-1 px-3 py-1.5 text-xs text-fg-3">
        <span>{t("market.pair")}</span>
        <span className="text-right">{t("market.last")}</span>
        <span className="w-16 text-right">{t("market.change")}</span>
      </div>
      <div role="listbox" aria-label={t("pcTrade.switchPair")} className="max-h-[360px] overflow-y-auto pb-1">
        {shown.length === 0 && (
          <p className="px-3 py-6 text-center text-sm text-fg-3">{group === "fav" && !query.trim() ? t("pcTrade.noFavorites") : t("market.noResults")}</p>
        )}
        {shown.map((r, i) => {
          const tk = tickers.get(r.symbol);
          const down = tk?.change?.startsWith("-");
          return (
            <button
              key={r.symbol}
              type="button"
              role="option"
              aria-selected={r.symbol === current}
              onMouseEnter={() => setActive(i)}
              onClick={() => pick(r.symbol)}
              className={cn(
                "grid w-full grid-cols-[1fr_auto_auto] items-center gap-x-4 px-3 py-2 text-left text-sm transition-colors",
                i === active && "bg-bg-3",
                r.symbol === current && "text-brand",
              )}
            >
              <span className="flex min-w-0 items-center gap-1">
                <FavoriteStar
                  active={favorites.has(r.symbol)}
                  onToggle={() => void toggle(r.symbol)}
                  label={favorites.has(r.symbol) ? t("common.unfavorite") : t("common.favorite")}
                  size={14}
                />
                <CoinIcon symbol={r.base} size={18} />
                <span className="truncate font-medium">
                  {r.base}
                  <span className="text-fg-3">/{r.quote}</span>
                </span>
              </span>
              <span className="text-right tabular-nums text-fg-1">{formatPrice(tk?.last, r.decimals)}</span>
              <span className={cn("w-16 text-right tabular-nums", tk?.change ? (down ? "text-down" : "text-up") : "text-fg-3")}>
                {formatPercent(tk?.change)}
              </span>
            </button>
          );
        })}
      </div>
    </Popover>
  );
}
