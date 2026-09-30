import { errorText, formatAmount } from "@exchange/core";
import { useDeposits, useWalletActions, useWithdrawals } from "@exchange/core/wallet/hooks";
import { explorerUrl, shortAddress, type Deposit, type WalletNetwork, type Withdrawal } from "@exchange/core/wallet/networks";
import { depositPhase, depositTimeline, withdrawalCancelable, withdrawalTimeline, type DepositPhase } from "@exchange/core/wallet/timeline";
import {
  Badge,
  Button,
  CoinIcon,
  CopyButton,
  Dialog,
  EmptyState,
  ErrorState,
  Progress,
  Skeleton,
  Stepper,
  TimeText,
  listItem,
  toast,
  type BadgeTone,
  type StepItem,
} from "@exchange/ui";
import { ExternalLink } from "lucide-react";
import { motion } from "motion/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { codeLabel, shownDecimals } from "./meta";

const phaseTone: Record<DepositPhase, BadgeTone> = { confirming: "info", crediting: "info", credited: "success", failed: "danger" };

function withdrawalTone(status: string): BadgeTone {
  switch (status) {
    case "CONFIRMED":
    case "INTERNAL_TRANSFER":
      return "success";
    case "REJECTED":
    case "FAILED":
      return "danger";
    case "CANCELED":
      return "neutral";
    case "PENDING_REVIEW":
      return "warn";
    default:
      return "info";
  }
}

// currentIndex is where a Stepper stands: the step in progress, or past the end.
function currentIndex(steps: StepItem[]): number {
  const i = steps.findIndex((s) => s.status === "current");
  return i < 0 ? steps.length : i;
}

function TxLink({ network, hash }: { network: WalletNetwork | undefined; hash: string | null | undefined }) {
  const { t } = useTranslation();
  const url = explorerUrl(network?.explorer_tx_url, hash);
  if (!hash || hash.startsWith("internal:")) return null;
  return url ? (
    <a href={url} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 text-fg-2 hover:text-brand">
      <span className="font-mono">{shortAddress(hash, 10, 6)}</span>
      <ExternalLink size={12} />
      <span className="sr-only">{t("pcAssets.common.viewTx")}</span>
    </a>
  ) : (
    <span className="inline-flex items-center gap-1 font-mono text-fg-2">
      {shortAddress(hash, 10, 6)}
      <CopyButton value={hash} size={12} />
    </span>
  );
}

function ListSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <ul className="flex flex-col gap-3">
      {Array.from({ length: rows }, (_, i) => (
        <li key={i} className="rounded-2 border border-line-1 bg-bg-2 p-3">
          <div className="flex items-center gap-3">
            <Skeleton round className="size-7" />
            <div className="flex flex-1 flex-col gap-1.5">
              <Skeleton className="h-3.5 w-28" />
              <Skeleton className="h-3 w-40" />
            </div>
          </div>
          <Skeleton className="mt-3 h-6 w-full" />
        </li>
      ))}
    </ul>
  );
}

/**
 * DepositList shows the caller's deposits, newest first, each with its
 * timeline (detected → confirming n/m → credited) and explorer link. The
 * "deposits" pushes move them live (useWalletPushes on the page).
 */
export function DepositList({ networks, decimals }: { networks: WalletNetwork[]; decimals: (asset: string) => number }) {
  const { t } = useTranslation();
  const q = useDeposits();
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const byCode = new Map(networks.map((n) => [n.network, n]));
  if (q.isPending) return <ListSkeleton />;
  if (q.isError && items.length === 0) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  if (items.length === 0) return <EmptyState compact title={t("pcAssets.deposit.recentEmpty")} description={t("pcAssets.deposit.recentEmptyHint")} />;
  return (
    <div className="flex flex-col gap-3">
      <ul className="flex flex-col gap-3">
        {items.map((d, i) => (
          <motion.li key={d.id} variants={listItem} initial="initial" animate="animate" custom={Math.min(i, 11)}>
            <DepositItem d={d} network={byCode.get(d.network)} decimals={d.asset ? decimals(d.asset) : 8} />
          </motion.li>
        ))}
      </ul>
      {q.hasNextPage && (
        <Button variant="ghost" block loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
          {t("pcAssets.common.loadMore")}
        </Button>
      )}
    </div>
  );
}

function DepositItem({ d, network, decimals }: { d: Deposit; network: WalletNetwork | undefined; decimals: number }) {
  const { t } = useTranslation();
  const tl = depositTimeline(d);
  const phase = depositPhase(d.status);
  const steps: StepItem[] = tl.steps.map((s) => ({
    key: s.key,
    title: t(`pcAssets.deposit.steps.${s.key}`),
    status: s.state,
    description:
      s.key === "confirming" && tl.confirmations ? (
        t("pcAssets.deposit.progress", tl.confirmations)
      ) : s.at ? (
        <TimeText value={s.at} format="monthDay" />
      ) : undefined,
  }));
  const failure = d.status === "ORPHANED" ? t("pcAssets.deposit.orphaned") : d.reason ? t(`pcAssets.deposit.reasons.${d.reason}`) : null;
  return (
    <div data-testid="deposit-row" data-status={d.status} className="rounded-2 border border-line-1 bg-bg-2 p-3">
      <div className="flex items-center gap-3">
        <CoinIcon symbol={d.asset ?? "?"} size={28} />
        <div className="min-w-0 flex-1">
          <div className="truncate font-medium tabular-nums text-fg-1">
            {d.asset ? (
              <>
                <span className="text-up">+{formatAmount(d.amount, shownDecimals(decimals))}</span> {d.asset}
              </>
            ) : (
              t("pcAssets.deposit.unknownToken")
            )}
          </div>
          <div className="truncate text-xs text-fg-3">
            {d.kind === "INTERNAL" ? t("pcAssets.deposit.internal") : (network?.display_name ?? d.network)} · <TimeText value={d.detected_at} relative />
          </div>
        </div>
        <Badge tone={phaseTone[phase]} dot>
          {t(`pcAssets.deposit.phase.${phase}`)}
        </Badge>
      </div>
      <Stepper steps={steps} current={currentIndex(steps)} size="sm" className="mt-3" aria-label={t("pcAssets.deposit.recent")} />
      {tl.confirmations && (
        <Progress
          className="mt-2"
          size="xs"
          tone="info"
          value={tl.confirmations.n}
          max={tl.confirmations.of}
          aria-label={t("pcAssets.deposit.progress", tl.confirmations)}
        />
      )}
      {(failure || d.tx_hash) && (
        <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs">
          {failure ? <span className="text-danger">{failure}</span> : <span />}
          <TxLink network={network} hash={d.tx_hash} />
        </div>
      )}
    </div>
  );
}

/**
 * WithdrawalList shows the caller's withdrawals with their timeline (risk
 * → review → sign → broadcast → confirm → done), the review reasons, the
 * transaction and, until signing starts, a cancel button.
 */
export function WithdrawalList({ networks, decimals }: { networks: WalletNetwork[]; decimals: (asset: string) => number }) {
  const { t } = useTranslation();
  const q = useWithdrawals();
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const byCode = new Map(networks.map((n) => [n.network, n]));
  if (q.isPending) return <ListSkeleton />;
  if (q.isError && items.length === 0) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  if (items.length === 0) return <EmptyState compact title={t("pcAssets.withdraw.recordsEmpty")} description={t("pcAssets.withdraw.recordsEmptyHint")} />;
  return (
    <div className="flex flex-col gap-3">
      <ul className="flex flex-col gap-3">
        {items.map((w, i) => (
          <motion.li key={w.id} variants={listItem} initial="initial" animate="animate" custom={Math.min(i, 11)}>
            <WithdrawalItem w={w} network={byCode.get(w.network)} decimals={decimals(w.asset)} />
          </motion.li>
        ))}
      </ul>
      {q.hasNextPage && (
        <Button variant="ghost" block loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
          {t("pcAssets.common.loadMore")}
        </Button>
      )}
    </div>
  );
}

function reasonLabel(reason: string): string {
  return /^[A-Z][A-Z0-9_]+$/.test(reason) ? codeLabel(reason) : reason;
}

function WithdrawalItem({ w, network, decimals }: { w: Withdrawal; network: WalletNetwork | undefined; decimals: number }) {
  const { t } = useTranslation();
  const actions = useWalletActions();
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const tl = withdrawalTimeline(w);
  const places = shownDecimals(decimals);
  const steps: StepItem[] = tl.steps.map((s) => ({
    key: s.key,
    title: s.key === "failed" ? codeLabel(w.status, "withdrawal") : t(`pcAssets.withdraw.steps.${s.key}`),
    status: s.state,
    description:
      s.key === "failed" ? (
        w.reject_reason ? reasonLabel(w.reject_reason) : undefined
      ) : s.key === "confirm" && tl.confirmations ? (
        t("pcAssets.deposit.progress", tl.confirmations)
      ) : s.at ? (
        <TimeText value={s.at} format="monthDay" />
      ) : undefined,
  }));

  const cancel = async () => {
    setBusy(true);
    try {
      await actions.cancelWithdrawal(w.id);
      toast.success(t("pcAssets.withdraw.canceled"));
      setAsking(false);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-testid="withdrawal-row" data-status={w.status} className="rounded-2 border border-line-1 bg-bg-2 p-4">
      <div className="flex flex-wrap items-center gap-3">
        <CoinIcon symbol={w.asset} size={32} />
        <div className="min-w-0 flex-1">
          <div className="font-medium tabular-nums text-fg-1">
            <span className="text-down">-{formatAmount(w.amount, places)}</span> {w.asset}
            <span className="ml-2 text-xs font-normal text-fg-3">
              {t("pcAssets.withdraw.feeLine", { fee: formatAmount(w.fee, places), asset: w.asset })}
            </span>
          </div>
          <div className="text-xs text-fg-3">
            {w.internal ? codeLabel("INTERNAL") : (network?.display_name ?? w.network)} · <TimeText value={w.created_at} />
          </div>
        </div>
        <Badge tone={withdrawalTone(w.status)} dot>
          {codeLabel(w.status, "withdrawal")}
        </Badge>
        {withdrawalCancelable(w.status) && (
          <Button size="sm" variant="secondary" onClick={() => setAsking(true)}>
            {t("pcAssets.withdraw.cancel")}
          </Button>
        )}
      </div>
      <Stepper steps={steps} current={currentIndex(steps)} size="sm" className="mt-4" aria-label={t("pcAssets.withdraw.records")} />
      {tl.confirmations && (
        <Progress className="mt-3" size="xs" tone="info" value={tl.confirmations.n} max={tl.confirmations.of} aria-label={t("pcAssets.deposit.progress", tl.confirmations)} />
      )}
      <div className="mt-3 flex flex-wrap items-center gap-x-6 gap-y-1 text-xs text-fg-3">
        <span className="inline-flex items-center gap-1">
          {t("pcAssets.common.address")}
          <span className="font-mono text-fg-2">{shortAddress(w.address, 10, 8)}</span>
          <CopyButton value={w.address} size={12} />
        </span>
        <TxLink network={network} hash={w.tx_hash} />
        {w.status === "PENDING_REVIEW" && w.risk_reasons.length > 0 && (
          <span className="text-warn">
            {t("pcAssets.withdraw.reviewing", { reasons: w.risk_reasons.map((r) => t(`pcAssets.withdraw.risk.${r}`)).join(t("pcAssets.common.listSeparator")) })}
          </span>
        )}
      </div>
      <Dialog
        open={asking}
        onOpenChange={(o) => !busy && setAsking(o)}
        title={t("pcAssets.withdraw.cancelTitle")}
        description={t("pcAssets.withdraw.cancelBody")}
        size="sm"
        onConfirm={() => void cancel()}
        confirmText={t("pcAssets.withdraw.cancel")}
        cancelText={t("pcAssets.withdraw.keep")}
        confirmVariant="danger"
        confirmLoading={busy}
      />
    </div>
  );
}
