import { ApiError, dec, errorText, formatAmount, formatDecimal, newIdempotencyKey, useContracts, useSettings, useSettleAssets } from "@exchange/core";
import { availableOf, useBalances, useFuturesAccount, useTransferAction, useTransfers, type Transfer as TransferRecord } from "@exchange/core/assets/hooks";
import { checkTransfer, otherAccount, transferCoins, transferMax, type AccountType } from "@exchange/core/assets/transfer";
import { futuresLineOf, useOpenProducts } from "@exchange/core/platform/products";
import { sortAssets } from "@exchange/core/wallet/networks";
import {
  Badge,
  Button,
  CoinIcon,
  Combobox,
  CountUp,
  CrossLiquidatingNotice,
  DataTable,
  EmptyState,
  FormField,
  HIDDEN_AMOUNT,
  NumberInput,
  Skeleton,
  TimeText,
  Tooltip,
  coinItems,
  mapServerError,
  toast,
  type ColumnDef,
  type DataColumnMeta,
} from "@exchange/ui";
import { ArrowLeftRight, ArrowRight, ChartCandlestick, ChevronDown, CircleCheck, Info, ScrollText, Wallet, Zap } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { AssetsLayout, Card, Tips } from "./parts/AssetsLayout";
import { codeLabel, shownDecimals, useAssetMeta } from "./parts/meta";
import { EligibilityNotice, MoreError, Notice, useAllowed } from "./parts/Notice";

const FIELDS = { LEDGER_INSUFFICIENT_BALANCE: "amount", LEDGER_AMOUNT_PRECISION: "amount", COMMON_INVALID_ARGUMENT: "amount" };

/**
 * Transfer (design §6.2): spot ⇄ futures, with a direction switch, the
 * coin, what may move (out of futures, also bounded by the cross positions'
 * unrealized loss) and exact checks against the coin's decimals. Balances
 * follow the balance pushes and roll to their new values.
 */
export default function Transfer() {
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const locale = useSettings((s) => s.locale);
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
  const idem = useRef<{ body: string; key: string } | null>(null);

  const to = otherAccount(from);
  const list = balances.data?.balances;
  // What may leave FUTURES is bounded by the account of the asset's own
  // contracts: USDT's, or a coin-margined contract's coin (design 2026-10-06 §2.6).
  const settles = useSettleAssets();
  const futures = useFuturesAccount(from === "FUTURES" && settles.includes(asset), asset);
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
            ? t("pcAssets.transfer.issueInsufficient")
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
    return from === "SPOT" ? coins.filter((a) => products[futuresLineOf(a)]) : coins;
  }, [meta.list, settles, list, from, products]);
  const known = contracts.isSuccess && balances.isSuccess && meta.list.length > 0;
  useEffect(() => {
    if (known && !ordered.includes(asset)) setAsset(ordered.includes("USDT") ? "USDT" : (ordered[0] ?? "USDT"));
  }, [known, ordered, asset]);
  const items = coinItems(ordered.length > 0 ? ordered : [asset], locale).map((it) => ({
    ...it,
    description: meta.name(it.value),
    trailing: <span className="tabular-nums">{formatAmount(availableOf(list, from, it.value), shownDecimals(meta.decimals(it.value)))}</span>,
  }));

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
      const message = t("pcAssets.transfer.done", { amount: formatDecimal(body.amount), asset, to: t(`pcAssets.common.account.${to}`) });
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
    <AssetsLayout title={t("pcAssets.transfer.title")} subtitle={t("pcAssets.transfer.subtitle")}>
      <EligibilityNotice feature="TRANSFER" title={t("pcAssets.transfer.title")} />
      <div className="grid grid-cols-[minmax(0,1fr)_320px] items-start gap-4">
        <Card bodyClassName="p-6">
          <form data-testid="transfer-form" onSubmit={(e) => void submit(e)} className="flex flex-col gap-5">
            <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3">
              <AccountCard side="from" account={from} asset={asset} balances={balances.isPending ? undefined : available} decimals={decimals} />
              <motion.button
                type="button"
                onClick={swap}
                disabled={!inward}
                aria-label={t("pcAssets.transfer.swap")}
                title={t("pcAssets.transfer.swap")}
                animate={{ rotate: turns * 180 }}
                transition={{ type: "spring", stiffness: 300, damping: 22 }}
                className="grid size-11 place-items-center rounded-full border border-line-2 bg-bg-2 text-fg-2 transition-colors hover:border-brand hover:text-brand disabled:pointer-events-none disabled:opacity-40"
              >
                <ArrowLeftRight size={18} />
              </motion.button>
              <AccountCard
                side="to"
                account={to}
                asset={asset}
                balances={balances.isPending ? undefined : availableOf(list, to, asset)}
                decimals={decimals}
              />
            </div>

            <FormField label={t("pcAssets.transfer.coin")}>
              {(control) => (
                <Combobox
                  items={items}
                  value={asset}
                  onValueChange={(v) => {
                    setAsset(v);
                    edit("");
                  }}
                  searchPlaceholder={t("pcAssets.common.searchCoin")}
                  aria-label={t("pcAssets.transfer.coin")}
                  trigger={
                    <button
                      type="button"
                      id={control.id}
                      className="flex h-12 w-full items-center gap-3 rounded-2 border border-line-1 bg-bg-2 px-4 text-left transition-colors hover:border-line-2 focus-visible:ring-1 focus-visible:ring-brand"
                    >
                      <CoinIcon symbol={asset} size={24} />
                      <span className="font-medium text-fg-1">{asset}</span>
                      <span className="truncate text-sm text-fg-3">{meta.name(asset)}</span>
                      <ChevronDown size={16} className="ml-auto shrink-0 text-fg-3" />
                    </button>
                  }
                />
              )}
            </FormField>

            <FormField
              label={t("pcAssets.transfer.amount")}
              error={issueText ?? (error?.field === "amount" ? error.message : undefined)}
              extra={
                <span className="flex items-center gap-1 tabular-nums">
                  {balances.isPending ? (
                    <Skeleton className="h-3 w-24" />
                  ) : (
                    t("pcAssets.transfer.max", { amount: formatAmount(max, shownDecimals(decimals)), asset })
                  )}
                  {from === "FUTURES" && (
                    <Tooltip content={t("pcAssets.transfer.transferableHint")}>
                      <span tabIndex={0} aria-label={t("pcAssets.transfer.transferableHint")} className="cursor-help text-fg-3">
                        <Info size={12} />
                      </span>
                    </Tooltip>
                  )}
                </span>
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
                suffix={
                  <button
                    type="button"
                    disabled={dec.sign(max) <= 0}
                    onClick={() => edit(max)}
                    className="mr-2 text-sm font-medium text-brand hover:brightness-110 disabled:opacity-50"
                  >
                    {t("pcAssets.transfer.all")}
                  </button>
                }
              />
            </FormField>

            {balances.isError && (
              <Notice tone="danger" role="alert">
                <span className="flex flex-wrap items-center justify-between gap-2">
                  {errorText(balances.error)}
                  <Button type="button" size="sm" variant="secondary" onClick={() => void balances.refetch()}>
                    {t("common.retry")}
                  </Button>
                </span>
              </Notice>
            )}
            {to === "FUTURES" && asset !== "USDT" && <Notice tone="info">{t("pcAssets.transfer.futuresNote")}</Notice>}
            {/* Nothing leaves while the asset's cross positions are being liquidated (C68, F24): transferable is 0. */}
            {from === "FUTURES" && futures.data?.asset === asset && futures.data.liquidating && <CrossLiquidatingNotice asset={asset} stops="transfer" />}
            {error && !error.field && (
              <Notice tone="danger" role="alert">
                {error.message}
              </Notice>
            )}
            <Button type="submit" size="lg" block disabled={!ready} loading={busy}>
              {t("pcAssets.transfer.submit")}
            </Button>
            {done && (
              <p role="status" className="flex items-center justify-center gap-2 text-sm text-success animate-fade-in">
                <CircleCheck size={16} />
                {done}
              </p>
            )}
          </form>
        </Card>
        <Tips
          title={t("pcAssets.transfer.tipsTitle")}
          tips={[
            [<Zap key="1" size={14} />, t("pcAssets.transfer.tip1")],
            [<ChartCandlestick key="2" size={14} />, t("pcAssets.transfer.tip2")],
            [<Info key="3" size={14} />, t("pcAssets.transfer.tip3")],
            [<ScrollText key="4" size={14} />, t("pcAssets.transfer.tip4")],
          ]}
        />
      </div>
      <Card title={t("pcAssets.transfer.recent")} className="mt-4" bodyClassName="p-0">
        <RecentTransfers decimals={meta.decimals} />
      </Card>
    </AssetsLayout>
  );
}

function AccountCard({
  side, account, asset, balances, decimals,
}: { side: "from" | "to"; account: AccountType; asset: string; balances: string | undefined; decimals: number }) {
  const { t } = useTranslation();
  const hidden = useSettings((s) => s.hideAmounts);
  const Icon = account === "SPOT" ? Wallet : ChartCandlestick;
  const places = shownDecimals(decimals);
  return (
    <div className="rounded-2 border border-line-1 bg-bg-2 p-4">
      <div className="text-xs text-fg-3">{t(side === "from" ? "pcAssets.transfer.from" : "pcAssets.transfer.to")}</div>
      <div key={account} className="mt-2 flex items-center gap-3 animate-fade-in">
        <span className="grid size-10 shrink-0 place-items-center rounded-full bg-bg-3 text-brand">
          <Icon size={18} />
        </span>
        <span className="min-w-0">
          <span className="block font-medium text-fg-1">{t(`pcAssets.common.account.${account}`)}</span>
          <span className="block truncate text-xs text-fg-3">{t(account === "SPOT" ? "pcAssets.transfer.spotHint" : "pcAssets.transfer.futuresHint")}</span>
        </span>
      </div>
      <div className="mt-3 flex items-baseline gap-1.5 text-md font-semibold text-fg-1">
        {balances === undefined ? (
          <Skeleton className="h-5 w-24" />
        ) : hidden ? (
          HIDDEN_AMOUNT
        ) : (
          <CountUp value={dec.round(balances, places, "down")} decimals={places} />
        )}
        <span className="text-xs font-normal text-fg-3">{asset}</span>
      </div>
    </div>
  );
}

function RecentTransfers({ decimals }: { decimals: (asset: string) => number }) {
  const { t } = useTranslation();
  const q = useTransfers();
  const items = useMemo(() => q.data?.pages.flatMap((p) => p.items) ?? [], [q.data]);
  const columns = useMemo<ColumnDef<TransferRecord, any>[]>(
    () => [
      {
        id: "time",
        header: t("common.time"),
        enableSorting: false,
        cell: ({ row }) => <TimeText value={row.original.created_at} format="datetimeSeconds" className="text-fg-2" />,
        meta: { width: 180 } satisfies DataColumnMeta,
      },
      {
        id: "asset",
        header: t("pcAssets.transfer.coin"),
        enableSorting: false,
        cell: ({ row }) => (
          <span className="flex items-center gap-2">
            <CoinIcon symbol={row.original.asset} size={20} />
            {row.original.asset}
          </span>
        ),
        meta: { width: 120 } satisfies DataColumnMeta,
      },
      {
        id: "direction",
        header: `${t("pcAssets.transfer.from")} → ${t("pcAssets.transfer.to")}`,
        enableSorting: false,
        cell: ({ row }) => (
          <span className="flex items-center gap-1.5 text-fg-2">
            {t(`pcAssets.common.account.${row.original.from_account_type}`)}
            <ArrowRight size={12} className="shrink-0 text-fg-3" />
            {t(`pcAssets.common.account.${row.original.to_account_type}`)}
          </span>
        ),
      },
      {
        id: "amount",
        header: t("pcAssets.transfer.amount"),
        enableSorting: false,
        cell: ({ row }) => (
          <span className="font-medium tabular-nums">
            {formatAmount(row.original.amount, shownDecimals(decimals(row.original.asset)))} <span className="text-fg-3">{row.original.asset}</span>
          </span>
        ),
        meta: { align: "right", width: 200 } satisfies DataColumnMeta,
      },
      {
        id: "status",
        header: t("common.status"),
        enableSorting: false,
        cell: ({ row }) => <TransferStatus tr={row.original} />,
        meta: { align: "right", width: 120 } satisfies DataColumnMeta,
      },
    ],
    [t, decimals],
  );
  return (
    <>
      <DataTable
        aria-label={t("pcAssets.transfer.recent")}
        columns={columns}
        data={items}
        getRowId={(tr) => tr.transfer_id}
        loading={q.isPending}
        loadingRows={4}
        error={q.isError ? q.error : undefined}
        onRetry={() => void q.refetch()}
        onEndReached={() => void q.fetchNextPage()}
        loadingMore={q.isFetchingNextPage}
        hasMore={q.hasNextPage}
        empty={<EmptyState compact title={t("pcAssets.transfer.recentEmpty")} description={t("pcAssets.transfer.recentEmptyHint")} />}
      />
      {q.isError && items.length > 0 && <MoreError error={q.error} onRetry={() => void q.fetchNextPage()} />}
    </>
  );
}

function TransferStatus({ tr }: { tr: TransferRecord }) {
  const { t } = useTranslation();
  const ok = tr.status === "COMPLETED";
  const badge = (
    <Badge tone={ok ? "success" : "danger"} dot>
      {codeLabel(tr.status, "transfer")}
    </Badge>
  );
  if (ok || !tr.failure_reason) return badge;
  return (
    <Tooltip content={t("pcAssets.transfer.failed", { reason: codeLabel(tr.failure_reason) })}>
      <span tabIndex={0}>{badge}</span>
    </Tooltip>
  );
}
