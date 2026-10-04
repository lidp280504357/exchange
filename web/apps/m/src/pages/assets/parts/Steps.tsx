import { isTestnet, openFor, type Purpose, type WalletNetwork } from "@exchange/core/wallet/networks";
import { Badge, CoinIcon, Skeleton, cn } from "@exchange/ui";
import { Check, Clock } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PRESS, TextButton } from "./bits";
import { plainAmount } from "./meta";

export type StepBlockState = "active" | "done" | "locked";

/**
 * StepBlock is one numbered step of a flow laid out top to bottom (the
 * deposit and withdrawal pages): open while it is being filled, a one-line
 * summary with "change" once done, a hint while an earlier step is missing.
 */
export function StepBlock({
  n, title, state, summary, onChange, last, children,
}: {
  n: number;
  title: string;
  state: StepBlockState;
  summary?: ReactNode;
  onChange?: () => void;
  last?: boolean;
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <section aria-label={title} className={cn("relative pl-10", !last && "pb-5")}>
      {!last && (
        <span
          aria-hidden
          className={cn("absolute bottom-0 left-3.5 top-10 w-px transition-colors duration-[var(--t-slow)]", state === "done" ? "bg-brand" : "bg-line-2")}
        />
      )}
      <span
        aria-hidden
        className={cn(
          "absolute left-0 top-2 grid size-7 place-items-center rounded-full border text-sm font-semibold tabular-nums transition-colors duration-[var(--t-base)]",
          state === "done" && "border-brand bg-brand text-brand-fg",
          state === "active" && "border-brand bg-brand-soft text-brand",
          state === "locked" && "border-line-2 bg-bg-2 text-fg-3",
        )}
      >
        {state === "done" ? <Check size={14} strokeWidth={3} /> : n}
      </span>
      <div className="flex min-h-tap items-center justify-between gap-2">
        <h2 className={cn("text-md font-medium", state === "locked" ? "text-fg-3" : "text-fg-1")}>
          <span className="sr-only">{t("mAssets.common.step", { n })} </span>
          {title}
        </h2>
        {state === "done" && onChange && (
          <TextButton onClick={onChange} className="-mr-3">
            {t("mAssets.common.change")}
          </TextButton>
        )}
      </div>
      {state === "done" && summary && <div className="min-w-0">{summary}</div>}
      {state === "active" && <div className="mt-1 animate-fade-up">{children}</div>}
      {state === "locked" && <p className="text-sm text-fg-3">{t("mAssets.common.locked")}</p>}
    </section>
  );
}

/** useEtaText is a network's usual time to arrive. */
export function useEtaText() {
  const { t } = useTranslation();
  return (minutes: number) => (minutes > 0 ? t("mAssets.common.minutes", { n: minutes }) : t("mAssets.common.unknownEta"));
}

/**
 * NetworkCards lets the user pick a network, one card per row: its name
 * and what matters on the page (confirmations, minimum, time and the free
 * fee for deposits; fee, minimum and time for withdrawals). Paused
 * networks show but cannot be picked.
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
      <div className="flex flex-col gap-2" aria-hidden>
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-28 w-full rounded-2" />
        ))}
      </div>
    );
  }
  if (list.length === 0) {
    return (
      <p className="flex items-center gap-2 text-sm text-fg-3">
        <Clock size={14} />
        {t(purpose === "deposit" ? "mAssets.deposit.noNetwork" : "mAssets.withdraw.noNetwork", { asset })}
      </p>
    );
  }
  return (
    <div role="group" aria-label={t(purpose === "deposit" ? "mAssets.deposit.pickNetwork" : "mAssets.withdraw.pickNetwork")} className="flex flex-col gap-2">
      {list.map((n) => {
        const open = openFor(n, purpose);
        const on = n.network === value;
        const facts: [string, ReactNode][] =
          purpose === "deposit"
            ? [
                [t("mAssets.deposit.confirmations"), t("mAssets.common.confirmations", { n: n.confirmations })],
                [t("mAssets.deposit.minDeposit"), amount(n.min_deposit)],
                [t("mAssets.common.eta"), eta(n.eta_minutes)],
                [t("mAssets.common.fee"), <span className="text-success">{t("mAssets.common.free")}</span>],
              ]
            : [
                [t("mAssets.common.fee"), amount(n.withdraw_fee)],
                [t("mAssets.withdraw.minWithdraw"), amount(n.min_withdraw)],
                [t("mAssets.common.eta"), eta(n.eta_minutes)],
              ];
        return (
          <button
            key={n.network}
            type="button"
            disabled={!open}
            aria-pressed={on}
            onClick={() => onChange(n.network)}
            className={cn(
              "flex w-full flex-col gap-2.5 rounded-2 border p-3 text-left",
              on ? "border-brand bg-brand-soft" : "border-line-1 bg-bg-2",
              open ? PRESS : "cursor-not-allowed opacity-50",
            )}
          >
            <span className="flex w-full items-center gap-2">
              <span className="font-semibold text-fg-1">{n.display_name}</span>
              <span className="min-w-0 truncate text-xs text-fg-3">{n.network}</span>
              <span className="ml-auto flex shrink-0 items-center gap-1.5">
                {isTestnet(n) && <Badge tone="info">{t("mAssets.common.testnet")}</Badge>}
                {!open && <Badge tone="warn">{t(purpose === "deposit" ? "mAssets.deposit.paused" : "mAssets.withdraw.paused")}</Badge>}
                {on && (
                  <span className="grid size-5 place-items-center rounded-full bg-brand text-brand-fg">
                    <Check size={12} strokeWidth={3} />
                  </span>
                )}
              </span>
            </span>
            <span className="grid w-full grid-cols-2 gap-x-3 gap-y-1.5 text-xs">
              {facts.map(([k, v]) => (
                <span key={k} className="flex min-w-0 flex-col">
                  <span className="text-fg-3">{k}</span>
                  <span className="truncate tabular-nums text-fg-1">{v}</span>
                </span>
              ))}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** ChosenCoin sums up the chosen coin in a done step, with an extra line (the balance). */
export function ChosenCoin({ asset, name, extra }: { asset: string; name: string; extra?: ReactNode }) {
  return (
    <span className="flex min-w-0 flex-col gap-0.5">
      <span className="flex min-w-0 items-center gap-2 text-sm">
        <CoinIcon symbol={asset} size={24} />
        <span className="font-medium text-fg-1">{asset}</span>
        <span className="min-w-0 truncate text-fg-3">{name}</span>
      </span>
      {extra && <span className="break-all pl-8 text-xs tabular-nums text-fg-2">{extra}</span>}
    </span>
  );
}

/** ChosenNetwork sums up the chosen network in a done step. */
export function ChosenNetwork({ network, detail }: { network: WalletNetwork; detail?: ReactNode }) {
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-sm">
      <span className="font-medium text-fg-1">{network.display_name}</span>
      <span className="text-xs text-fg-3">{network.network}</span>
      {detail && <span className="text-xs text-fg-2">{detail}</span>}
    </span>
  );
}
