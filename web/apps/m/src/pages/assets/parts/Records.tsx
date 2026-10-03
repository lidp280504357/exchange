import { enumLabel, errorText, formatAmount } from "@exchange/core";
import { useDeposits, useWalletActions, useWithdrawals } from "@exchange/core/wallet/hooks";
import { explorerUrl, shortAddress, type Deposit, type WalletNetwork, type Withdrawal } from "@exchange/core/wallet/networks";
import { depositPhase, depositTimeline, withdrawalCancelable, withdrawalTimeline, type DepositPhase } from "@exchange/core/wallet/timeline";
import {
  Badge,
  Button,
  CoinIcon,
  EmptyState,
  ErrorState,
  KeyValue,
  Progress,
  Sheet,
  Stepper,
  TimeText,
  toast,
  type BadgeTone,
  type StepItem,
} from "@exchange/ui";
import { ExternalLink } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AddressValue, Appear, CardSkeleton, CopyIcon, LoadMore, RETRY, TextButton, useKept } from "./bits";
import { currentStep } from "./logic";
import { shownDecimals } from "./meta";
import { MoreError, Notice } from "./Notice";
import { Track } from "./Track";

// The deposit and withdrawal records of the mobile site: cards with a
// compact status timeline (the "deposits" and "withdrawals" pushes move
// them live; the page calls useWalletPushes), each opening a sheet with
// the full timeline, the addresses and hashes, and for a withdrawal that
// has not reached signing, its cancellation.

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

/** TxLink is a transaction hash, linked to the network's explorer when it has one. */
function TxLink({ network, hash }: { network: WalletNetwork | undefined; hash: string | null | undefined }) {
  const { t } = useTranslation();
  if (!hash || hash.startsWith("internal:")) return <span className="text-fg-3">—</span>;
  const url = explorerUrl(network?.explorer_tx_url, hash);
  return (
    <span className="-my-2.5 inline-flex items-center">
      {url ? (
        <a href={url} target="_blank" rel="noreferrer noopener" className="inline-flex min-h-11 items-center gap-1 text-fg-2 active:text-brand">
          <span className="font-mono text-xs">{shortAddress(hash, 10, 6)}</span>
          <ExternalLink size={12} />
          <span className="sr-only">{t("mAssets.common.viewTx")}</span>
        </a>
      ) : (
        <span className="font-mono text-xs text-fg-2">{shortAddress(hash, 10, 6)}</span>
      )}
      <CopyIcon value={hash} className="-mr-3" />
    </span>
  );
}

function useDepositSteps(d: Deposit) {
  const { t } = useTranslation();
  const tl = depositTimeline(d);
  const steps = tl.steps.map((s) => ({
    key: s.key,
    label: t(`mAssets.deposit.steps.${s.key}`),
    state: s.state,
    description:
      s.key === "confirming" && tl.confirmations ? (
        t("mAssets.common.progress", tl.confirmations)
      ) : s.at ? (
        <TimeText value={s.at} format="monthDay" />
      ) : undefined,
  }));
  return { tl, steps };
}

function depositFailure(d: Deposit, t: (key: string) => string): string | null {
  if (d.status === "ORPHANED") return t("mAssets.deposit.orphaned");
  return d.reason && d.status !== "CREDITED" ? t(`mAssets.deposit.reasons.${d.reason}`) : null;
}

/** depositNote says a credited deposit that first waited (its reason) was credited after a check. */
function depositNote(d: Deposit, t: (key: string) => string): string | null {
  return d.reason && d.status === "CREDITED" ? t("mAssets.deposit.reviewed") : null;
}

/**
 * DepositList shows the caller's deposits, newest first: amount, network,
 * status and the detected → confirming n/m → credited track.
 */
export function DepositList({ networks, decimals }: { networks: WalletNetwork[]; decimals: (asset: string) => number }) {
  const { t } = useTranslation();
  const q = useDeposits();
  const [open, setOpen] = useState<string | null>(null);
  const kept = useKept(open);
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const byCode = new Map(networks.map((n) => [n.network, n]));
  const selected = items.find((d) => d.id === kept) ?? null;

  if (q.isPending) return <CardSkeleton tall />;
  if (q.isError && items.length === 0) {
    return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} className={RETRY} />;
  }
  if (items.length === 0) {
    return <EmptyState compact title={t("mAssets.deposit.recentEmpty")} description={t("mAssets.deposit.recentEmptyHint")} />;
  }
  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-2">
        {items.map((d, i) => (
          <Appear key={d.id} index={i}>
            <DepositCard d={d} network={byCode.get(d.network)} decimals={d.asset ? decimals(d.asset) : 8} onOpen={() => setOpen(d.id)} />
          </Appear>
        ))}
      </ul>
      {q.isError && <MoreError error={q.error} onRetry={() => void (q.isFetchNextPageError ? q.fetchNextPage() : q.refetch())} />}
      {q.hasNextPage && !q.isError && <LoadMore loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()} />}
      <DepositSheet
        open={open !== null && selected !== null}
        d={selected}
        network={selected ? byCode.get(selected.network) : undefined}
        decimals={selected?.asset ? decimals(selected.asset) : 8}
        onClose={() => setOpen(null)}
      />
    </div>
  );
}

function DepositCard({ d, network, decimals, onOpen }: { d: Deposit; network: WalletNetwork | undefined; decimals: number; onOpen: () => void }) {
  const { t } = useTranslation();
  const { tl, steps } = useDepositSteps(d);
  const phase = depositPhase(d.status);
  const failure = depositFailure(d, t);
  const note = depositNote(d, t);
  const now = steps[currentStep(steps)];
  return (
    <button
      type="button"
      data-testid="deposit-row"
      data-status={d.status}
      onClick={onOpen}
      className="block w-full rounded-3 bg-bg-1 p-3 text-left transition-colors active:bg-bg-2"
    >
      <span className="flex items-center gap-3">
        <CoinIcon symbol={d.asset ?? "?"} size={32} />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium tabular-nums text-fg-1">
            {d.asset ? (
              <>
                <span className="text-up">+{formatAmount(d.amount, shownDecimals(decimals))}</span> {d.asset}
              </>
            ) : (
              t("mAssets.deposit.unknownToken")
            )}
          </span>
          <span className="block truncate text-xs text-fg-3">
            {d.kind === "INTERNAL" ? t("mAssets.deposit.internal") : (network?.display_name ?? d.network)} · <TimeText value={d.detected_at} relative />
          </span>
        </span>
        <Badge tone={phaseTone[phase]} dot>
          {t(`mAssets.deposit.phase.${phase}`)}
        </Badge>
      </span>
      <Track steps={steps} className="mt-3" />
      {now && <span className="sr-only">{t("mAssets.common.nowAt", { step: now.label })}</span>}
      {tl.confirmations && (
        <Progress
          className="mt-2"
          size="xs"
          tone="info"
          value={tl.confirmations.n}
          max={tl.confirmations.of}
          aria-label={t("mAssets.common.progress", tl.confirmations)}
        />
      )}
      {failure && <span className="mt-2 block text-xs text-danger">{failure}</span>}
      {note && <span className="mt-2 block text-xs text-fg-3">{note}</span>}
    </button>
  );
}

function DepositSheet({
  open, d, network, decimals, onClose,
}: { open: boolean; d: Deposit | null; network: WalletNetwork | undefined; decimals: number; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title={t("mAssets.deposit.detail")} closeButton>
      {d && <DepositDetail d={d} network={network} decimals={decimals} />}
    </Sheet>
  );
}

function DepositDetail({ d, network, decimals }: { d: Deposit; network: WalletNetwork | undefined; decimals: number }) {
  const { t } = useTranslation();
  const { tl, steps } = useDepositSteps(d);
  const phase = depositPhase(d.status);
  const failure = depositFailure(d, t);
  const note = depositNote(d, t);
  const stepItems: StepItem[] = steps.map((s) => ({ key: s.key, title: s.label, status: s.state, description: s.description }));
  return (
    <div className="flex flex-col gap-4 pt-1">
      <div className="flex items-center gap-3">
        <CoinIcon symbol={d.asset ?? "?"} size={40} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-lg font-semibold tabular-nums text-fg-1">
            {d.asset ? `+${formatAmount(d.amount, shownDecimals(decimals))} ${d.asset}` : t("mAssets.deposit.unknownToken")}
          </div>
          <div className="text-xs text-fg-3">{d.kind === "INTERNAL" ? t("mAssets.deposit.internal") : (network?.display_name ?? d.network)}</div>
        </div>
        <Badge tone={phaseTone[phase]} dot size="md">
          {t(`mAssets.deposit.phase.${phase}`)}
        </Badge>
      </div>
      <Stepper steps={stepItems} current={currentStep(steps)} orientation="vertical" size="sm" aria-label={t("mAssets.common.timeline")} />
      {tl.confirmations && (
        <Progress
          size="sm"
          tone="info"
          value={tl.confirmations.n}
          max={tl.confirmations.of}
          label={t("mAssets.deposit.confirmations")}
          valueText={t("mAssets.common.progress", tl.confirmations)}
        />
      )}
      {failure && (
        <Notice tone="danger" role="alert">
          {failure}
        </Notice>
      )}
      {note && <Notice>{note}</Notice>}
      <KeyValue
        items={[
          { key: "network", label: t("mAssets.common.network"), value: network?.display_name ?? d.network },
          { key: "kind", label: t("mAssets.common.kind"), value: enumLabel(d.kind) },
          ...(d.address ? [{ key: "address", label: t("mAssets.common.address"), value: <AddressValue address={d.address} /> }] : []),
          { key: "tx", label: t("mAssets.common.txHash"), value: <TxLink network={network} hash={d.tx_hash} /> },
          { key: "detected", label: t("mAssets.deposit.detectedAt"), value: <TimeText value={d.detected_at} format="datetimeSeconds" /> },
          ...(d.credited_at
            ? [{ key: "credited", label: t("mAssets.deposit.creditedAt"), value: <TimeText value={d.credited_at} format="datetimeSeconds" /> }]
            : []),
        ]}
      />
    </div>
  );
}

function useWithdrawalSteps(w: Withdrawal) {
  const { t } = useTranslation();
  const tl = withdrawalTimeline(w);
  const steps = tl.steps.map((s) => ({
    key: s.key,
    label: s.key === "failed" ? enumLabel(w.status, "withdrawal") : t(`mAssets.withdraw.steps.${s.key}`),
    state: s.state,
    description:
      s.key === "failed" ? (
        w.reject_reason ? reasonLabel(w.reject_reason) : undefined
      ) : s.key === "confirm" && tl.confirmations ? (
        t("mAssets.common.progress", tl.confirmations)
      ) : s.at ? (
        <TimeText value={s.at} format="monthDay" />
      ) : undefined,
  }));
  return { tl, steps };
}

// A refusal's reason: a code gets its label, free text shows as it is.
function reasonLabel(reason: string): string {
  return /^[A-Z][A-Z0-9_]+$/.test(reason) ? enumLabel(reason) : reason;
}

/**
 * WithdrawalList shows the caller's withdrawals: amount and fee, status,
 * the risk → review → sign → broadcast → confirm → done track, and until
 * signing starts, a cancel button.
 */
export function WithdrawalList({ networks, decimals }: { networks: WalletNetwork[]; decimals: (asset: string) => number }) {
  const { t } = useTranslation();
  const q = useWithdrawals();
  // The open record, and whether its sheet asks to confirm the cancellation.
  const [open, setOpen] = useState<{ id: string; cancel: boolean } | null>(null);
  const kept = useKept(open);
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const byCode = new Map(networks.map((n) => [n.network, n]));
  const selected = items.find((w) => w.id === kept?.id) ?? null;

  if (q.isPending) return <CardSkeleton tall />;
  if (q.isError && items.length === 0) {
    return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} className={RETRY} />;
  }
  if (items.length === 0) {
    return <EmptyState compact title={t("mAssets.withdraw.recordsEmpty")} description={t("mAssets.withdraw.recordsEmptyHint")} />;
  }
  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-2">
        {items.map((w, i) => (
          <Appear key={w.id} index={i}>
            <WithdrawalCard
              w={w}
              network={byCode.get(w.network)}
              decimals={decimals(w.asset)}
              onOpen={() => setOpen({ id: w.id, cancel: false })}
              onCancel={() => setOpen({ id: w.id, cancel: true })}
            />
          </Appear>
        ))}
      </ul>
      {q.isError && <MoreError error={q.error} onRetry={() => void (q.isFetchNextPageError ? q.fetchNextPage() : q.refetch())} />}
      {q.hasNextPage && !q.isError && <LoadMore loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()} />}
      <WithdrawalSheet
        open={open !== null && selected !== null}
        w={selected}
        network={selected ? byCode.get(selected.network) : undefined}
        decimals={selected ? decimals(selected.asset) : 8}
        confirming={kept?.cancel ?? false}
        onConfirming={(cancel) => setOpen((o) => (o ? { ...o, cancel } : o))}
        onClose={() => setOpen(null)}
      />
    </div>
  );
}

function WithdrawalCard({
  w, network, decimals, onOpen, onCancel,
}: { w: Withdrawal; network: WalletNetwork | undefined; decimals: number; onOpen: () => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const { tl, steps } = useWithdrawalSteps(w);
  const places = shownDecimals(decimals);
  const now = steps[currentStep(steps)];
  const reviewing = w.status === "PENDING_REVIEW" && w.risk_reasons.length > 0;
  return (
    <div data-testid="withdrawal-row" data-status={w.status} className="overflow-hidden rounded-3 bg-bg-1">
      <button type="button" onClick={onOpen} className="block w-full p-3 text-left transition-colors active:bg-bg-2">
        <span className="flex items-center gap-3">
          <CoinIcon symbol={w.asset} size={32} />
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium tabular-nums text-fg-1">
              <span className="text-down">-{formatAmount(w.amount, places)}</span> {w.asset}
            </span>
            <span className="block truncate text-xs text-fg-3">
              {w.internal ? enumLabel("INTERNAL") : (network?.display_name ?? w.network)} · <TimeText value={w.created_at} format="monthDay" />
            </span>
          </span>
          <Badge tone={withdrawalTone(w.status)} dot>
            {enumLabel(w.status, "withdrawal")}
          </Badge>
        </span>
        <Track steps={steps} className="mt-3" />
        {now && <span className="sr-only">{t("mAssets.common.nowAt", { step: now.label })}</span>}
        {tl.confirmations && (
          <Progress
            className="mt-2"
            size="xs"
            tone="info"
            value={tl.confirmations.n}
            max={tl.confirmations.of}
            aria-label={t("mAssets.common.progress", tl.confirmations)}
          />
        )}
        <span className="mt-2 flex items-center justify-between gap-2 text-xs text-fg-3">
          <span className="tabular-nums">{t("mAssets.withdraw.feeLine", { fee: formatAmount(w.fee, places), asset: w.asset })}</span>
          <span className="truncate font-mono">{shortAddress(w.address, 6, 6)}</span>
        </span>
        {reviewing && <span className="mt-1 block text-xs text-warn">{t("mAssets.withdraw.reviewing", { reasons: riskText(w, t) })}</span>}
      </button>
      {withdrawalCancelable(w.status) && (
        <div className="flex justify-end border-t border-line-1 px-1">
          <TextButton onClick={onCancel}>{t("mAssets.withdraw.cancel")}</TextButton>
        </div>
      )}
    </div>
  );
}

function riskText(w: Withdrawal, t: (key: string) => string): string {
  return w.risk_reasons.map((r) => t(`mAssets.withdraw.risk.${r}`)).join(t("mAssets.common.listSeparator"));
}

function WithdrawalSheet({
  open, w, network, decimals, confirming, onConfirming, onClose,
}: {
  open: boolean;
  w: Withdrawal | null;
  network: WalletNetwork | undefined;
  decimals: number;
  confirming: boolean;
  onConfirming: (confirming: boolean) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const actions = useWalletActions();
  const [busy, setBusy] = useState(false);
  const cancelable = w !== null && withdrawalCancelable(w.status);

  const cancel = async () => {
    if (!w) return;
    setBusy(true);
    try {
      await actions.cancelWithdrawal(w.id);
      toast.success(t("mAssets.withdraw.canceled"));
      onClose();
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const footer = !cancelable ? undefined : confirming ? (
    <div className="flex flex-col gap-2">
      <p className="text-sm text-fg-2">{t("mAssets.withdraw.cancelBody")}</p>
      <div className="grid grid-cols-2 gap-2">
        <Button size="lg" variant="secondary" disabled={busy} onClick={() => onConfirming(false)}>
          {t("mAssets.withdraw.keep")}
        </Button>
        <Button size="lg" variant="danger" loading={busy} onClick={() => void cancel()}>
          {t("mAssets.withdraw.cancel")}
        </Button>
      </div>
    </div>
  ) : (
    <Button size="lg" variant="secondary" block onClick={() => onConfirming(true)}>
      {t("mAssets.withdraw.cancelAction")}
    </Button>
  );

  return (
    <Sheet
      open={open}
      onOpenChange={(o) => !o && !busy && onClose()}
      title={confirming && cancelable ? t("mAssets.withdraw.cancelTitle") : t("mAssets.withdraw.detail")}
      closeButton
      footer={footer}
    >
      {w && <WithdrawalDetail w={w} network={network} decimals={decimals} />}
    </Sheet>
  );
}

function WithdrawalDetail({ w, network, decimals }: { w: Withdrawal; network: WalletNetwork | undefined; decimals: number }) {
  const { t } = useTranslation();
  const { tl, steps } = useWithdrawalSteps(w);
  const places = shownDecimals(decimals);
  const stepItems: StepItem[] = steps.map((s) => ({ key: s.key, title: s.label, status: s.state, description: s.description }));
  return (
    <div className="flex flex-col gap-4 pt-1">
      <div className="flex items-center gap-3">
        <CoinIcon symbol={w.asset} size={40} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-lg font-semibold tabular-nums text-fg-1">
            -{formatAmount(w.amount, places)} {w.asset}
          </div>
          <div className="text-xs text-fg-3">{w.internal ? enumLabel("INTERNAL") : (network?.display_name ?? w.network)}</div>
        </div>
        <Badge tone={withdrawalTone(w.status)} dot size="md">
          {enumLabel(w.status, "withdrawal")}
        </Badge>
      </div>
      {w.status === "PENDING_REVIEW" && w.risk_reasons.length > 0 && (
        <Notice tone="warn">{t("mAssets.withdraw.reviewing", { reasons: riskText(w, t) })}</Notice>
      )}
      <Stepper steps={stepItems} current={currentStep(steps)} orientation="vertical" size="sm" aria-label={t("mAssets.common.timeline")} />
      {tl.confirmations && (
        <Progress
          size="sm"
          tone="info"
          value={tl.confirmations.n}
          max={tl.confirmations.of}
          label={t("mAssets.withdraw.confirmations")}
          valueText={t("mAssets.common.progress", tl.confirmations)}
        />
      )}
      <KeyValue
        items={[
          { key: "network", label: t("mAssets.common.network"), value: w.internal ? enumLabel("INTERNAL") : (network?.display_name ?? w.network) },
          { key: "address", label: t("mAssets.common.address"), value: <AddressValue address={w.address} /> },
          { key: "fee", label: t("mAssets.common.fee"), value: `${formatAmount(w.fee, places)} ${w.asset}` },
          { key: "tx", label: t("mAssets.common.txHash"), value: <TxLink network={network} hash={w.tx_hash} /> },
          { key: "created", label: t("mAssets.withdraw.createdAt"), value: <TimeText value={w.created_at} format="datetimeSeconds" /> },
        ]}
      />
    </div>
  );
}
