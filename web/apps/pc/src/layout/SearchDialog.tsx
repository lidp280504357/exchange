import { dec, formatPercent, formatPrice, pairName, routes, useContracts, usePairs, useTickers } from "@exchange/core";
import { searchMarkets } from "@exchange/core/markets/search";
import { CoinIcon, DialogPrimitive as RDialog, cn } from "@exchange/ui";
import { Search } from "lucide-react";
import { useMemo, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";

type Entry = { key: string; symbol: string; base: string; quote: string; name: string; to: string; decimals: number; futures: boolean };

/** SearchDialog is the ⌘K palette's dialog: the markets filtered as you type. */
export default function SearchDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const go = (to: string) => {
    onOpenChange(false);
    setQuery("");
    navigate(to);
  };
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <RDialog.Portal>
        <RDialog.Overlay className="fixed inset-0 z-[var(--z-dialog)] bg-overlay data-[state=open]:animate-fade-in" />
        <RDialog.Content
          aria-describedby={undefined}
          className="fixed left-1/2 top-[12vh] z-[var(--z-dialog)] w-[560px] max-w-[calc(100vw-32px)] -translate-x-1/2 overflow-hidden rounded-3 border border-line-1 bg-bg-1 shadow-pop outline-none data-[state=open]:animate-pop-in"
        >
          <RDialog.Title className="sr-only">{t("nav.search")}</RDialog.Title>
          <Results query={query} setQuery={setQuery} active={active} setActive={setActive} onGo={go} />
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  );
}

function Results({
  query, setQuery, active, setActive, onGo,
}: {
  query: string;
  setQuery: (q: string) => void;
  active: number;
  setActive: (fn: number | ((a: number) => number)) => void;
  onGo: (to: string) => void;
}) {
  const { t } = useTranslation();
  const pairs = usePairs();
  const contracts = useContracts();
  const tickers = useTickers();
  const entries = useMemo<Entry[]>(() => {
    const spot = (pairs.data?.pairs ?? [])
      .filter((p) => p.status !== "DELISTED")
      .map((p) => ({
        key: p.symbol, symbol: p.symbol, base: p.base_asset, quote: p.quote_asset, name: pairName(p), to: routes.trade(p.symbol),
        decimals: p.price_decimals, futures: false,
      }));
    const perp = (contracts.data?.contracts ?? []).map((c) => ({
      key: c.symbol, symbol: c.symbol, base: c.base_asset, quote: c.quote_asset, name: t("pc.perpetual"), to: routes.futures(c.symbol),
      decimals: dec.decimalsOf(c.tick_size), futures: true,
    }));
    return [...spot, ...perp];
  }, [pairs.data, contracts.data, t]);
  const shown = searchMarkets(entries, query).slice(0, 12);

  const keyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(0, Math.min(shown.length - 1, a + (e.key === "ArrowDown" ? 1 : -1))));
    } else if (e.key === "Enter" && shown[active]) {
      e.preventDefault();
      onGo(shown[active].to);
    }
  };

  return (
    <>
      <div className="flex items-center gap-3 border-b border-line-1 px-4">
        <Search size={18} className="text-fg-3" />
        <input
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={keyDown}
          placeholder={t("pc.searchHint")}
          aria-label={t("nav.search")}
          className="h-14 flex-1 bg-transparent text-md text-fg-1 outline-none placeholder:text-fg-3"
        />
        <kbd className="rounded-1 border border-line-2 px-1.5 text-xs text-fg-3">Esc</kbd>
      </div>
      <div role="listbox" className="max-h-[420px] overflow-y-auto py-2">
        {shown.length === 0 && <p className="px-4 py-8 text-center text-sm text-fg-3">{t("market.noResults")}</p>}
        {shown.map((e, i) => {
          const tk = tickers.get(e.symbol);
          return (
            <button
              key={e.key}
              type="button"
              role="option"
              aria-selected={i === active}
              onMouseEnter={() => setActive(i)}
              onClick={() => onGo(e.to)}
              className={cn("flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors", i === active && "bg-bg-2")}
            >
              <CoinIcon symbol={e.base} size={28} />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="font-medium text-fg-1">
                  {e.base}
                  <span className="text-fg-3">/{e.quote}</span>
                  {e.futures && <span className="ml-2 rounded-1 bg-brand-soft px-1.5 py-0.5 text-xs text-brand">{t("pc.perpetual")}</span>}
                </span>
                <span className="truncate text-xs text-fg-3">{e.name}</span>
              </span>
              <span className="flex flex-col items-end text-sm tabular-nums">
                <span className="text-fg-1">{formatPrice(tk?.last, e.decimals)}</span>
                <span className={cn("text-xs", tk?.change?.startsWith("-") ? "text-down" : "text-up")}>{formatPercent(tk?.change)}</span>
              </span>
            </button>
          );
        })}
      </div>
    </>
  );
}
