import { useSettings } from "@exchange/core";
import { isTestnet, openFor, type Purpose, type WalletNetwork } from "@exchange/core/wallet/networks";
import { Badge, Button, CoinIcon, Combobox, Skeleton, cn, coinItems, type ComboboxItem } from "@exchange/ui";
import { ArrowRight, Check, Clock, Search } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { plainAmount, type AssetMeta } from "./meta";

/**
 * CoinPicker offers the coins a page serves as cards, and every other
 * listed coin through a search, where the ones without deposits or
 * withdrawals show "trade only".
 */
export function CoinPicker({
  coins, value, onChange, meta, trailing, loading,
}: {
  coins: string[];
  value: string;
  onChange: (asset: string) => void;
  meta: AssetMeta;
  /** Extra text on a card (networks, the available balance). */
  trailing?: (asset: string) => ReactNode;
  loading?: boolean;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const open = new Set(coins);
  const others = meta.list.map((a) => a.asset_code).filter((c) => !open.has(c));
  const items: ComboboxItem[] = coinItems([...coins, ...others], locale).map((it) => ({
    ...it,
    description: meta.name(it.value),
    trailing: open.has(it.value) ? undefined : <span className="text-fg-3">{t("pcAssets.common.onlyInternal")}</span>,
  }));

  return (
    <div className="flex flex-col gap-3">
      {loading ? (
        <div className="grid grid-cols-2 gap-3 xl:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-[72px] rounded-2" />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-3 xl:grid-cols-3">
          {coins.map((c) => {
            const on = c === value;
            return (
              <button
                key={c}
                type="button"
                aria-pressed={on}
                onClick={() => onChange(c)}
                className={cn(
                  "group relative flex items-center gap-3 rounded-2 border p-3 text-left transition-[transform,border-color,background-color] duration-[var(--t-base)] hover:-translate-y-0.5",
                  on ? "border-brand bg-brand-soft" : "border-line-1 bg-bg-2 hover:border-line-2",
                )}
              >
                <CoinIcon symbol={c} size={36} />
                <span className="min-w-0 flex-1">
                  <span className="block font-semibold text-fg-1">{c}</span>
                  <span className="block truncate text-xs text-fg-3">{meta.name(c)}</span>
                  {trailing && <span className="mt-0.5 block truncate text-xs text-fg-2">{trailing(c)}</span>}
                </span>
                {on && (
                  <span className="absolute right-2 top-2 grid size-4 place-items-center rounded-full bg-brand text-brand-fg">
                    <Check size={10} strokeWidth={3} />
                  </span>
                )}
              </button>
            );
          })}
        </div>
      )}
      <Combobox
        items={items}
        value={value || null}
        onValueChange={(v) => onChange(v)}
        searchPlaceholder={t("pcAssets.common.searchCoin")}
        aria-label={t("pcAssets.common.searchOther")}
        trigger={
          <button
            type="button"
            className="flex h-10 w-full items-center gap-2 rounded-2 border border-dashed border-line-2 px-3 text-sm text-fg-3 transition-colors hover:border-fg-3 hover:text-fg-1"
          >
            <Search size={14} />
            {t("pcAssets.common.searchOther")}
          </button>
        }
      />
    </div>
  );
}

/** InternalOnly tells that a coin has no deposits or withdrawals, with a way to trade it. */
export function InternalOnly({ asset, name, trade }: { asset: string; name: string; trade: string | null }) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-4 rounded-2 border border-line-1 bg-bg-2 p-4 animate-fade-up">
      <CoinIcon symbol={asset} size={40} />
      <div className="min-w-0 flex-1">
        <div className="font-medium text-fg-1">
          {t("pcAssets.common.onlyInternalTitle", { asset })} <span className="text-sm font-normal text-fg-3">{name}</span>
        </div>
        <p className="mt-0.5 text-sm text-fg-3">{t("pcAssets.common.onlyInternalHint")}</p>
      </div>
      {trade && (
        <Button asChild size="sm" icon={<ArrowRight size={14} />}>
          <Link to={trade}>{t("pcAssets.common.goTrade")}</Link>
        </Button>
      )}
    </div>
  );
}

/** etaText is a network's usual time to arrive. */
export function useEtaText() {
  const { t } = useTranslation();
  return (minutes: number) => (minutes > 0 ? t("pcAssets.common.minutes", { n: minutes }) : t("pcAssets.common.unknownEta"));
}

/**
 * NetworkCards lets the user pick a network: its name, and what matters
 * for the page (confirmations, minimum, time, fee). Paused networks show
 * but cannot be picked.
 */
export function NetworkCards({
  list, value, onChange, purpose, asset, decimals, loading,
}: {
  list: WalletNetwork[];
  value: string | null;
  onChange: (network: string) => void;
  purpose: Purpose;
  asset: string;
  decimals: number;
  loading?: boolean;
}) {
  const { t } = useTranslation();
  const eta = useEtaText();
  const amount = (v: string) => `${plainAmount(v, decimals)} ${asset}`;
  if (loading) {
    return (
      <div className="grid grid-cols-2 gap-3">
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-[108px] rounded-2" />
        ))}
      </div>
    );
  }
  return (
    <div role="group" aria-label={t(purpose === "deposit" ? "pcAssets.deposit.pickNetwork" : "pcAssets.withdraw.pickNetwork")} className="grid grid-cols-2 gap-3">
      {list.map((n) => {
        const open = openFor(n, purpose);
        const on = n.network === value;
        const rows: [string, ReactNode][] =
          purpose === "deposit"
            ? [
                [t("pcAssets.deposit.confirmations"), t("pcAssets.common.confirmations", { n: n.confirmations })],
                [t("pcAssets.deposit.minDeposit"), amount(n.min_deposit)],
                [t("pcAssets.deposit.eta"), eta(n.eta_minutes)],
                [t("pcAssets.deposit.fee"), <span className="text-success">{t("pcAssets.common.free")}</span>],
              ]
            : [
                [t("pcAssets.withdraw.fee"), amount(n.withdraw_fee)],
                [t("pcAssets.withdraw.minWithdraw"), amount(n.min_withdraw)],
                [t("pcAssets.withdraw.eta"), eta(n.eta_minutes)],
              ];
        return (
          <button
            key={n.network}
            type="button"
            disabled={!open}
            aria-pressed={on}
            onClick={() => onChange(n.network)}
            className={cn(
              "relative flex flex-col gap-2 rounded-2 border p-4 text-left transition-[transform,border-color,background-color] duration-[var(--t-base)]",
              on ? "border-brand bg-brand-soft" : "border-line-1 bg-bg-2",
              open ? "hover:-translate-y-0.5 hover:border-line-2" : "cursor-not-allowed opacity-50",
            )}
          >
            <span className="flex items-center gap-2">
              <span className="font-semibold text-fg-1">{n.display_name}</span>
              <span className="text-xs text-fg-3">{n.network}</span>
              {isTestnet(n) && (
                <Badge tone="info" className="ml-auto">
                  {t("pcAssets.common.testnet")}
                </Badge>
              )}
              {!open && (
                <Badge tone="warn" className={cn(!isTestnet(n) && "ml-auto")}>
                  {t(purpose === "deposit" ? "pcAssets.deposit.paused" : "pcAssets.withdraw.paused")}
                </Badge>
              )}
            </span>
            <span className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
              {rows.map(([k, v]) => (
                <span key={k} className="flex min-w-0 flex-col">
                  <span className="text-fg-3">{k}</span>
                  <span className="truncate text-fg-1 tabular-nums">{v}</span>
                </span>
              ))}
            </span>
            {on && (
              <span className="absolute right-3 top-3 grid size-4 place-items-center rounded-full bg-brand text-brand-fg">
                <Check size={10} strokeWidth={3} />
              </span>
            )}
          </button>
        );
      })}
      {list.length === 0 && (
        <p className="col-span-2 flex items-center gap-2 text-sm text-fg-3">
          <Clock size={14} />
          {t(purpose === "deposit" ? "pcAssets.deposit.noneOpen" : "pcAssets.withdraw.closed", { asset })}
        </p>
      )}
    </div>
  );
}
