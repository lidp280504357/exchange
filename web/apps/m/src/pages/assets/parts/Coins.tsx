import { useSettings } from "@exchange/core";
import { Button, CoinIcon, Input, Sheet, Skeleton, cn, coinItems, filterItems, type ComboboxItem } from "@exchange/ui";
import { ArrowRight, Check, Search } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { PRESS } from "./bits";

// Coin choosers of the mobile assets pages: the searchable list of the
// deposit and withdrawal pages' first step, and a searchable sheet (the
// transfer's coin, the history's coin filter). Matching is the design
// system's (code, then names in both languages).

function useItems(coins: readonly string[], name: (asset: string) => string): ComboboxItem[] {
  const locale = useSettings((s) => s.locale);
  return useMemo(() => coinItems([...coins], locale).map((it) => ({ ...it, description: name(it.value) })), [coins, locale, name]);
}

/** CoinRowContent is a coin's icon, code and name, with what goes on the right. */
function CoinRowContent({ asset, name, trailing, selected }: { asset: string; name: string; trailing?: ReactNode; selected?: boolean }) {
  return (
    <>
      <CoinIcon symbol={asset} size={32} />
      <span className="flex min-w-0 flex-1 flex-col text-left">
        <span className="font-medium text-fg-1">{asset}</span>
        <span className="truncate text-xs text-fg-3">{name}</span>
      </span>
      {trailing !== undefined && <span className="shrink-0 text-right text-sm tabular-nums text-fg-2">{trailing}</span>}
      {selected && <Check size={16} className="shrink-0 text-brand" />}
    </>
  );
}

/**
 * CoinList is the first step of the deposit and withdrawal pages: the
 * coins that can move on a network, and, while searching, every other
 * listed coin, shown as "trade only" with a link to its market.
 */
export function CoinList({
  open, all, value, onPick, name, trailing, tradeLink, loading,
}: {
  /** The coins with an open network, in order. */
  open: readonly string[];
  /** Every listed coin. */
  all: readonly string[];
  value: string;
  onPick: (asset: string) => void;
  name: (asset: string) => string;
  /** What an open coin shows on the right (its networks, its balance). */
  trailing: (asset: string) => ReactNode;
  tradeLink: (asset: string) => string | null;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const served = useMemo(() => new Set(open), [open]);
  const others = useMemo(() => all.filter((c) => !served.has(c)), [all, served]);
  const ordered = useMemo(() => [...open, ...others], [open, others]);
  const items = useItems(ordered, name);
  const q = query.trim();
  const shown = q ? filterItems(items, q) : items.filter((it) => served.has(it.value));

  return (
    <div className="flex flex-col gap-2">
      <Input
        size="lg"
        value={query}
        onValueChange={setQuery}
        clearable
        onClear={() => setQuery("")}
        prefix={<Search size={16} />}
        placeholder={t("mAssets.common.searchCoin")}
        aria-label={t("mAssets.common.searchCoin")}
        enterKeyHint="search"
        autoComplete="off"
      />
      {loading ? (
        <div className="flex flex-col gap-2" aria-hidden>
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-14 w-full rounded-2" />
          ))}
        </div>
      ) : (
        <ul className="flex flex-col gap-1.5" aria-label={t("mAssets.common.coin")}>
          {shown.map((it) => {
            const ok = served.has(it.value);
            if (!ok) {
              const trade = tradeLink(it.value);
              return (
                <li key={it.value} className="flex min-h-14 items-center gap-3 rounded-2 bg-bg-2 pl-3">
                  <CoinRowContent asset={it.value} name={it.description ?? it.value} trailing={<span className="text-xs text-fg-3">{t("mAssets.common.onlyInternal")}</span>} />
                  {trade ? (
                    <Link to={trade} className="flex min-h-tap shrink-0 items-center gap-1 px-3 text-sm font-medium text-brand">
                      {t("mAssets.common.goTrade")}
                      <ArrowRight size={14} />
                    </Link>
                  ) : (
                    <span className="w-3" />
                  )}
                </li>
              );
            }
            const on = it.value === value;
            return (
              <li key={it.value}>
                <button
                  type="button"
                  aria-pressed={on}
                  onClick={() => onPick(it.value)}
                  className={cn(
                    "flex min-h-14 w-full items-center gap-3 rounded-2 border px-3 py-2",
                    PRESS,
                    on ? "border-brand bg-brand-soft" : "border-transparent bg-bg-2",
                  )}
                >
                  <CoinRowContent asset={it.value} name={it.description ?? it.value} trailing={trailing(it.value)} selected={on} />
                </button>
              </li>
            );
          })}
          {shown.length === 0 && (
            <li className="flex flex-col items-center gap-2 py-6 text-center text-sm text-fg-3">
              {q ? t("mAssets.common.noMatch") : t("mAssets.common.noneOpen")}
              {q && (
                <Button variant="secondary" className="h-tap" onClick={() => setQuery("")}>
                  {t("mAssets.common.clearSearch")}
                </Button>
              )}
            </li>
          )}
        </ul>
      )}
      {!loading && !q && others.length > 0 && <p className="px-1 text-xs text-fg-3">{t("mAssets.common.othersHint", { count: others.length })}</p>}
    </div>
  );
}

/** InternalOnly tells that a coin has no deposits or withdrawals, with a way to trade it. */
export function InternalOnly({ asset, name, trade }: { asset: string; name: string; trade: string | null }) {
  const { t } = useTranslation();
  return (
    <div role="status" className="flex items-center gap-3 rounded-2 border border-line-1 bg-bg-2 p-3 animate-fade-up">
      <CoinIcon symbol={asset} size={36} />
      <div className="min-w-0 flex-1">
        <div className="font-medium text-fg-1">{t("mAssets.common.onlyInternalTitle", { asset })}</div>
        <p className="mt-0.5 text-xs text-fg-3">
          {name !== asset && `${name} · `}
          {t("mAssets.common.onlyInternalHint")}
        </p>
      </div>
      {trade && (
        <Button asChild className="h-tap shrink-0">
          <Link to={trade}>{t("mAssets.common.goTrade")}</Link>
        </Button>
      )}
    </div>
  );
}

/**
 * CoinSheet picks a coin in a bottom sheet with a search field; allLabel
 * adds a first row for "every coin" (value "").
 */
export function CoinSheet({
  open, onOpenChange, title, coins, value, onPick, name, trailing, allLabel,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  coins: readonly string[];
  value: string;
  onPick: (asset: string) => void;
  name: (asset: string) => string;
  trailing?: (asset: string) => ReactNode;
  allLabel?: string;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState("");
  const items = useItems(coins, name);
  const shown = filterItems(items, query);
  useEffect(() => {
    if (!open) setQuery("");
  }, [open]);

  const pick = (asset: string) => {
    onPick(asset);
    onOpenChange(false);
  };

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={title} bodyClassName="px-0 pb-4">
      <div className="sticky top-0 z-10 bg-bg-1 px-4 pb-2">
        <Input
          size="lg"
          value={query}
          onValueChange={setQuery}
          clearable
          onClear={() => setQuery("")}
          prefix={<Search size={16} />}
          placeholder={t("mAssets.common.searchCoin")}
          aria-label={t("mAssets.common.searchCoin")}
          enterKeyHint="search"
          autoComplete="off"
        />
      </div>
      <div role="listbox" aria-label={title} className="flex flex-col">
        {allLabel && !query.trim() && (
          <button
            type="button"
            role="option"
            aria-selected={value === ""}
            onClick={() => pick("")}
            className={cn("flex min-h-14 items-center gap-3 px-4 active:bg-bg-2", value === "" && "bg-bg-2")}
          >
            <span className="flex-1 text-left font-medium text-fg-1">{allLabel}</span>
            {value === "" && <Check size={16} className="shrink-0 text-brand" />}
          </button>
        )}
        {shown.map((it) => (
          <button
            key={it.value}
            type="button"
            role="option"
            aria-selected={it.value === value}
            onClick={() => pick(it.value)}
            className={cn("flex min-h-14 items-center gap-3 px-4 active:bg-bg-2", it.value === value && "bg-bg-2")}
          >
            <CoinRowContent asset={it.value} name={it.description ?? it.value} trailing={trailing?.(it.value)} selected={it.value === value} />
          </button>
        ))}
        {shown.length === 0 && <p className="px-4 py-8 text-center text-sm text-fg-3">{t("mAssets.common.noMatch")}</p>}
      </div>
    </Sheet>
  );
}
