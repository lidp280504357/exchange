import { ApiError, dec, enumLabel, errorText, formatAmount, formatDecimal, newIdempotencyKey, routes, useContracts, useSettings, useSettleAssets } from "@exchange/core";
import {
  accountKeys,
  availableOf,
  useBalances,
  useFuturesAccount,
  useTransferAction,
  useTransfers,
  type Transfer as TransferRecord,
} from "@exchange/core/assets/hooks";
import { checkTransfer, otherAccount, transferCoins, transferMax, type AccountType } from "@exchange/core/assets/transfer";
import { futuresLineOf, useOpenProducts } from "@exchange/core/platform/products";
import { sortAssets } from "@exchange/core/wallet/networks";
import {
  Badge, Button, CoinIcon, CountUp, CrossLiquidatingNotice, EmptyState, ErrorState, FormField, HIDDEN_AMOUNT, NumberInput, Skeleton, TimeText, cn,
  mapServerError, toast,
} from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, ArrowUpDown, ChevronDown, CircleCheck } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { usePageHeader } from "../../layout/header";
import { Appear, CardSkeleton, LoadMore, PRESS, RETRY, TextButton } from "./parts/bits";
import { CoinSheet } from "./parts/Coins";
import { shownDecimals, useAssetMeta } from "./parts/meta";
import { EligibilityNotice, MoreError, Notice, useAllowed } from "./parts/Notice";
import { PullToRefresh } from "../../components/PullToRefresh";

const FIELDS = { LEDGER_INSUFFICIENT_BALANCE: "amount", LEDGER_AMOUNT_PRECISION: "amount", COMMON_INVALID_ARGUMENT: "amount" };

/**
 * Transfer (design §7.2, §6.2): spot ⇄ futures with a direction switch
 * that turns, the coin picked in a sheet, what may move (out of futures,
 * also bounded by the cross positions' unrealized loss) and exact checks
 * against the coin's decimals. The balances follow the balance pushes and
 * roll to their new values after a transfer; the recent transfers follow.
 */
export default function Transfer() {
  const { t } = useTranslation();
  const title = t("nav.transfer");
  usePageHeader({ title, back: routes.assets }, [title]);
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const meta = useAssetMeta();
  const balances = useBalances();
  const allowed = useAllowed("TRANSFER");
  const move = useTransferAction();

  const [from, setFrom] = useState<AccountType>(params.get("from") === "FUTURES" ? "FUTURES" : "SPOT");
  const [asset, setAsset] = useState((params.get("asset") || "USDT").toUpperCase());
  const [amount, setAmount] = useState("");
  const [turns, setTurns] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ field?: string; message: string } | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [picking, setPicking] = useState(false);
  const idem = useRef<{ body: string; key: string } | null>(null);

  const to = otherAccount(from);
  const list = balances.data?.balances;
  // What may leave FUTURES is bounded by the account of the asset's own
  // contracts: USDT's, or a coin-margined contract's coin (design 2026-10-06 §2.6).
  const settles = useSettleAssets();
  const futures = useFuturesAccount(asset, { enabled: from === "FUTURES" && settles.includes(asset) });
  const decimals = meta.decimals(asset);
  const available = availableOf(list, from, asset);
  const transferable = from === "FUTURES" && futures.data?.asset === asset ? futures.data.transferable : null;
  const max = transferMax(available, transferable);
  const issue = checkTransfer(amount, max, decimals);
  const issueText =
    issue === "format"
      ? t("errors.format")
      : issue === "zero"
        ? t("errors.zero")
        : issue === "precision"
          ? t("errors.precision", { n: decimals })
          : issue === "insufficient"
            ? t("mAssets.transfer.issueInsufficient")
            : undefined;
  const ready = allowed && amount.trim() !== "" && issue === null && !busy && balances.isSuccess;

  // The coins a futures account can hold (its settlement assets, and what
  // it still holds of another), those with a balance on the "from" side
  // first (review FE, B128). A coin asked for in the address that it
  // cannot hold falls back to USDT once the lists are in.
  const contracts = useContracts();
  // Nothing moves into a closed product line's futures account (design
  // 2026-10-07, product line switches §1 #3): its coins are not offered
  // that way, and with both contract lines closed only out of futures.
  const products = useOpenProducts();
  const inward = products.usdt_m || products.coin_m;
  useEffect(() => {
    if (!inward && from === "SPOT") setFrom("FUTURES");
  }, [inward, from]);
  const ordered = useMemo(() => {
    const coins = transferCoins(sortAssets(meta.list.map((a) => a.asset_code)), settles, list, from);
    const all = from === "SPOT" ? coins.filter((a) => products[futuresLineOf(a)]) : coins;
    return all.length > 0 ? all : [asset];
  }, [list, from, meta.list, asset, settles, products]);
  const known = contracts.isSuccess && balances.isSuccess && meta.list.length > 0;
  useEffect(() => {
    if (known && !ordered.includes(asset)) setAsset(ordered.includes("USDT") ? "USDT" : (ordered[0] ?? "USDT"));
  }, [known, ordered, asset]);

  const edit = (v: string) => {
    setAmount(v);
    setError(null);
    setDone(null);
  };

  const swap = () => {
    setFrom(to);
    setTurns((n) => n + 1);
    edit("");
  };

  const refresh = () =>
    Promise.all([balances.refetch(), qc.refetchQueries({ queryKey: accountKeys.transfers }), from === "FUTURES" ? futures.refetch() : null]);

  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!ready) return;
    const body = { asset, amount: dec.normalize(amount.trim()), from_account_type: from, to_account_type: to };
    const bodyKey = JSON.stringify(body);
    // One key per intended transfer: a retry after a lost response moves
    // the funds once; a refusal replays under its key, so the next try gets a new one.
    if (idem.current?.body !== bodyKey) idem.current = { body: bodyKey, key: newIdempotencyKey() };
    setBusy(true);
    setError(null);
    try {
      await move(body, idem.current.key);
      idem.current = null;
      const message = t("mAssets.transfer.done", { amount: formatDecimal(body.amount), asset, to: t(`mAssets.common.account.${to}`) });
      toast.success(message);
      setDone(message);
      setAmount("");
    } catch (err) {
      if (err instanceof ApiError && err.status < 500) idem.current = null;
      const m = mapServerError(err, FIELDS);
      setError({ field: m.field, message: m.message });
    } finally {
      setBusy(false);
    }
  };

  return (
    <PullToRefresh onRefresh={refresh}>
      <div className="flex flex-col gap-3 px-4 pb-6 pt-2">
        <EligibilityNotice feature="TRANSFER" title={title} />
        <form data-testid="transfer-form" onSubmit={(e) => void submit(e)} className="flex flex-col gap-4 rounded-3 bg-bg-1 p-4">
          <Direction
            from={from}
            to={to}
            asset={asset}
            decimals={decimals}
            fromBalance={balances.isPending ? undefined : available}
            toBalance={balances.isPending ? undefined : availableOf(list, to, asset)}
            turns={turns}
            onSwap={inward ? swap : null}
          />
          <FormField label={t("mAssets.transfer.coin")}>
            {(control) => (
              <button
                type="button"
                id={control.id}
                aria-haspopup="dialog"
                onClick={() => setPicking(true)}
                className={cn("flex min-h-tap w-full items-center gap-3 rounded-2 border border-line-1 bg-bg-2 px-3 text-left", PRESS)}
              >
                <CoinIcon symbol={asset} size={24} />
                <span className="font-medium text-fg-1">{asset}</span>
                <span className="min-w-0 truncate text-sm text-fg-3">{meta.name(asset)}</span>
                <ChevronDown size={16} className="ml-auto shrink-0 text-fg-3" />
              </button>
            )}
          </FormField>
          <FormField
            label={t("mAssets.transfer.amount")}
            error={issueText ?? (error?.field === "amount" ? error.message : undefined)}
            extra={
              balances.isPending ? (
                <Skeleton className="h-3 w-24" />
              ) : (
                <span className="tabular-nums">{t("mAssets.transfer.max", { amount: formatAmount(max, shownDecimals(decimals)), asset })}</span>
              )
            }
          >
            <NumberInput
              size="lg"
              value={amount}
              onValueChange={edit}
              decimals={decimals}
              maxButton={false}
              placeholder="0"
              unit={asset}
              enterKeyHint="done"
              suffix={
                <TextButton disabled={dec.sign(max) <= 0} onClick={() => edit(max)}>
                  {t("mAssets.transfer.all")}
                </TextButton>
              }
            />
          </FormField>
          {from === "FUTURES" && <p className="-mt-2 text-xs text-fg-3">{t("mAssets.transfer.transferableHint")}</p>}
          {balances.isError && (
            <Notice tone="danger" role="alert">
              <span className="flex flex-wrap items-center justify-between gap-2">
                {errorText(balances.error)}
                <Button type="button" variant="secondary" className="h-tap" onClick={() => void balances.refetch()}>
                  {t("common.retry")}
                </Button>
              </span>
            </Notice>
          )}
          {to === "FUTURES" && asset !== "USDT" && <Notice tone="info">{t("mAssets.transfer.futuresNote")}</Notice>}
          {/* Nothing leaves while the asset's cross positions are being liquidated (C68, F24): transferable is 0. */}
          {from === "FUTURES" && futures.data?.asset === asset && futures.data.liquidating && <CrossLiquidatingNotice asset={asset} stops="transfer" />}
          {error && !error.field && (
            <Notice tone="danger" role="alert">
              {error.message}
            </Notice>
          )}
          <Button type="submit" size="lg" block disabled={!ready} loading={busy}>
            {t("mAssets.transfer.submit")}
          </Button>
          {done && (
            <p role="status" className="flex items-center justify-center gap-2 text-center text-sm text-success animate-fade-in">
              <CircleCheck size={16} className="shrink-0" />
              {done}
            </p>
          )}
        </form>
        <h2 className="px-1 pt-2 text-md font-semibold text-fg-1">{t("mAssets.transfer.recent")}</h2>
        <RecentTransfers decimals={meta.decimals} />
      </div>
      <CoinSheet
        open={picking}
        onOpenChange={setPicking}
        title={t("mAssets.transfer.pickCoin")}
        coins={ordered}
        value={asset}
        onPick={(a) => {
          setAsset(a);
          edit("");
        }}
        name={meta.name}
        trailing={(a) => formatAmount(availableOf(list, from, a), shownDecimals(meta.decimals(a)))}
      />
    </PullToRefresh>
  );
}

/** Direction shows where the funds go (from over to) with the switch that turns them around. */
function Direction({
  from, to, asset, decimals, fromBalance, toBalance, turns, onSwap,
}: {
  from: AccountType;
  to: AccountType;
  asset: string;
  decimals: number;
  fromBalance: string | undefined;
  toBalance: string | undefined;
  turns: number;
  /** null while the funds may only go one way (both contract lines closed). */
  onSwap: (() => void) | null;
}) {
  const { t } = useTranslation();
  const reduced = useReducedMotion();
  return (
    <div className="flex items-center gap-3 rounded-2 bg-bg-2 p-3">
      <div className="relative flex min-w-0 flex-1 flex-col gap-3">
        <span aria-hidden className="absolute bottom-7 left-[4px] top-5 border-l border-dashed border-line-2" />
        <AccountRow side="from" account={from} balance={fromBalance} asset={asset} decimals={decimals} />
        <AccountRow side="to" account={to} balance={toBalance} asset={asset} decimals={decimals} />
      </div>
      <motion.button
        type="button"
        onClick={onSwap ?? undefined}
        disabled={!onSwap}
        aria-label={t("mAssets.transfer.swap")}
        animate={{ rotate: turns * 180 }}
        transition={reduced ? { duration: 0 } : { type: "spring", stiffness: 300, damping: 22 }}
        className="grid size-tap shrink-0 place-items-center rounded-full border border-line-2 bg-bg-1 text-brand active:bg-bg-3 disabled:opacity-40"
      >
        <ArrowUpDown size={18} />
      </motion.button>
    </div>
  );
}

function AccountRow({
  side, account, balance, asset, decimals,
}: { side: "from" | "to"; account: AccountType; balance: string | undefined; asset: string; decimals: number }) {
  const { t } = useTranslation();
  const hidden = useSettings((s) => s.hideAmounts);
  const places = shownDecimals(decimals);
  return (
    <div className="relative pl-5">
      <span
        aria-hidden
        className={cn("absolute left-0 top-1 size-2.5 rounded-full", side === "from" ? "bg-brand" : "border border-brand bg-bg-2")}
      />
      <div className="text-xs text-fg-3">{t(side === "from" ? "mAssets.transfer.from" : "mAssets.transfer.to")}</div>
      <div key={account} className="mt-0.5 flex flex-wrap items-baseline justify-between gap-x-2 animate-fade-in">
        <span className="font-medium text-fg-1">{t(`mAssets.common.account.${account}`)}</span>
        {balance === undefined ? (
          <Skeleton className="h-4 w-24" />
        ) : (
          <span className="min-w-0 break-all text-sm tabular-nums text-fg-2">
            <span className="mr-1 text-xs text-fg-3">{t("mAssets.transfer.available")}</span>
            {hidden ? HIDDEN_AMOUNT : <CountUp value={dec.round(balance, places, "down")} decimals={places} />}
            <span className="ml-1 text-xs text-fg-3">{asset}</span>
          </span>
        )}
      </div>
    </div>
  );
}

function RecentTransfers({ decimals }: { decimals: (asset: string) => number }) {
  const { t } = useTranslation();
  const q = useTransfers();
  const items = useMemo(() => q.data?.pages.flatMap((p) => p.items) ?? [], [q.data]);
  if (q.isPending) return <CardSkeleton />;
  if (q.isError && items.length === 0) {
    return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} className={RETRY} />;
  }
  if (items.length === 0) {
    return <EmptyState compact title={t("mAssets.transfer.recentEmpty")} description={t("mAssets.transfer.recentEmptyHint")} />;
  }
  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-2">
        {items.map((tr, i) => (
          <Appear key={tr.transfer_id} index={i}>
            <TransferCard tr={tr} decimals={decimals(tr.asset)} />
          </Appear>
        ))}
      </ul>
      {q.isError && <MoreError error={q.error} onRetry={() => void (q.isFetchNextPageError ? q.fetchNextPage() : q.refetch())} />}
      {q.hasNextPage && !q.isError && <LoadMore loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()} />}
    </div>
  );
}

function TransferCard({ tr, decimals }: { tr: TransferRecord; decimals: number }) {
  const { t } = useTranslation();
  const ok = tr.status === "COMPLETED";
  return (
    <div data-testid="transfer-row" data-status={tr.status} className="rounded-3 bg-bg-1 p-3">
      <div className="flex items-center gap-3">
        <CoinIcon symbol={tr.asset} size={32} />
        <div className="min-w-0 flex-1">
          <div className="break-all font-medium tabular-nums text-fg-1">
            {formatAmount(tr.amount, shownDecimals(decimals))} <span className="text-fg-3">{tr.asset}</span>
          </div>
          <div className="flex items-center gap-1 text-xs text-fg-3">
            {t(`mAssets.common.account.${tr.from_account_type}`)}
            <ArrowRight size={12} className="shrink-0" />
            {t(`mAssets.common.account.${tr.to_account_type}`)}
          </div>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          <Badge tone={ok ? "success" : "danger"} dot>
            {enumLabel(tr.status, "transfer")}
          </Badge>
          <TimeText value={tr.created_at} format="monthDay" className="text-xs text-fg-3" />
        </div>
      </div>
      {!ok && tr.failure_reason && <p className="mt-2 text-xs text-danger">{t("mAssets.transfer.failed", { reason: enumLabel(tr.failure_reason) })}</p>}
    </div>
  );
}
