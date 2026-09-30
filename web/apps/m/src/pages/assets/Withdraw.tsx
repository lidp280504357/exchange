import { ApiError, dec, enumLabel, errorText, formatAmount, formatDecimal, newIdempotencyKey, routes } from "@exchange/core";
import { availableOf, useBalances } from "@exchange/core/assets/hooks";
import { useAddressCheck, useWalletActions, useWalletNetworks, useWalletPushes, useWithdrawAddresses, walletKeys } from "@exchange/core/wallet/hooks";
import { assetsFor, isUsable, shortAddress, sortAssets, walletFlow, type WalletNetwork, type WithdrawAddress } from "@exchange/core/wallet/networks";
import { withdrawQuote, type WithdrawQuote } from "@exchange/core/wallet/withdraw";
import { Button, CoinIcon, ErrorState, FormField, KeyValue, NumberInput, Progress, Sheet, cn, mapServerError, toast } from "@exchange/ui";
import { useQueryClient } from "@tanstack/react-query";
import { BookUser, ChevronRight } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useSearchParams } from "react-router";
import { useStepUp } from "../../features/auth/StepUp";
import { usePageHeader } from "../../layout/header";
import { AddressBookSheet, CheckLine, type BookMode } from "./parts/AddressBook";
import { PRESS, RETRY, TextButton } from "./parts/bits";
import { CoinList, InternalOnly } from "./parts/Coins";
import { plainAmount, shownDecimals, useAssetMeta, useTradeLinks } from "./parts/meta";
import { EligibilityNotice, Notice, useAllowed } from "./parts/Notice";
import { PullToRefresh } from "../../components/PullToRefresh";
import { WithdrawalList } from "./parts/Records";
import { ChosenCoin, ChosenNetwork, NetworkCards, StepBlock } from "./parts/Steps";

// Server refusals that belong to a field of the form; the rest show above the button.
const FIELDS: Record<string, "amount" | "address"> = {
  WALLET_BELOW_MINIMUM: "amount",
  WALLET_AMOUNT_PRECISION: "amount",
  LEDGER_INSUFFICIENT_BALANCE: "amount",
  WALLET_LIMIT_EXCEEDED: "amount",
  WALLET_INVALID_ADDRESS: "address",
  WALLET_ADDRESS_NOT_WHITELISTED: "address",
  WALLET_ADDRESS_COOLDOWN: "address",
  WALLET_OWN_ADDRESS: "address",
};

type ServerError = { field?: string; message: string; code?: string; details?: Record<string, unknown> };

/**
 * Withdraw (design §7.2, §6.2): coin → network → address (the address book
 * in a sheet, or a new address checked locally and by the server, saved
 * with a step-up) → amount (the fee on top, what arrives, the minimum) →
 * a confirmation sheet → step-up → submit. Below, the withdrawals with
 * their timelines, cancelable until signing starts; the pushes move them.
 */
export default function Withdraw() {
  const { t } = useTranslation();
  const title = t("nav.withdraw");
  usePageHeader({ title, back: routes.assets }, [title]);
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const qc = useQueryClient();
  const reduced = useReducedMotion();
  const meta = useAssetMeta();
  const links = useTradeLinks();
  const networks = useWalletNetworks();
  const balances = useBalances();
  const book = useWithdrawAddresses();
  const recordsRef = useRef<HTMLDivElement>(null);
  useWalletPushes();

  const all = useMemo(() => networks.data ?? [], [networks.data]);
  const coins = useMemo(() => assetsFor(all, "withdraw"), [all]);
  const everyCoin = useMemo(() => sortAssets(meta.list.map((a) => a.asset_code)), [meta.list]);
  const asset = (params.get("asset") ?? "").toUpperCase();
  const { list, network: net, internalOnly, paused } = walletFlow(networks.data, asset, params.get("network"), "withdraw");
  const openNetworks = list.filter((n) => n.withdraw_enabled).length;
  const decimals = meta.decimals(asset);
  const spot = balances.data?.balances;
  const availableText = (a: string) => formatAmount(availableOf(spot, "SPOT", a), shownDecimals(meta.decimals(a)));

  // The records are the target of "#records" links (the fund flow's).
  useEffect(() => {
    if (location.hash === "#records") recordsRef.current?.scrollIntoView({ block: "start" });
  }, [location.hash]);

  const choose = (next: { asset?: string; network?: string | null }) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (next.asset !== undefined) {
          if (next.asset) p.set("asset", next.asset);
          else p.delete("asset");
          p.delete("network");
        }
        if (next.network !== undefined) {
          if (next.network) p.set("network", next.network);
          else p.delete("network");
        }
        return p;
      },
      { replace: true },
    );

  const refresh = () =>
    Promise.all([networks.refetch(), balances.refetch(), book.refetch(), qc.refetchQueries({ queryKey: walletKeys.withdrawals })]);

  const coinState = !asset || internalOnly ? "active" : "done";
  const networkState = !asset || internalOnly ? "locked" : net ? "done" : "active";

  return (
    <PullToRefresh onRefresh={refresh}>
      <div className="flex flex-col gap-3 px-4 pb-6 pt-2">
        <EligibilityNotice feature="WITHDRAW" title={title} />
        {balances.isError && (
          <Notice tone="danger" role="alert">
            <span className="flex flex-wrap items-center justify-between gap-2">
              {errorText(balances.error)}
              <Button variant="secondary" className="h-11" onClick={() => void balances.refetch()}>
                {t("common.retry")}
              </Button>
            </span>
          </Notice>
        )}
        <section aria-label={title} className="rounded-3 bg-bg-1 p-4">
          {networks.isError ? (
            <ErrorState compact message={errorText(networks.error)} onRetry={() => void networks.refetch()} className={RETRY} />
          ) : (
            <>
              <StepBlock
                n={1}
                title={t("mAssets.withdraw.stepCoin")}
                state={coinState}
                onChange={() => choose({ asset: "" })}
                summary={
                  <ChosenCoin asset={asset} name={meta.name(asset)} extra={t("mAssets.withdraw.availableLine", { amount: availableText(asset), asset })} />
                }
              >
                {internalOnly && (
                  <div className="mb-3">
                    <InternalOnly asset={asset} name={meta.name(asset)} trade={links.spot(asset)} />
                  </div>
                )}
                <CoinList
                  open={coins}
                  all={everyCoin}
                  value={asset}
                  onPick={(a) => choose({ asset: a })}
                  name={meta.name}
                  trailing={(a) => (
                    <span className="flex flex-col items-end">
                      <span className="text-xs text-fg-3">{t("mAssets.withdraw.available")}</span>
                      <span>{availableText(a)}</span>
                    </span>
                  )}
                  tradeLink={links.spot}
                  loading={networks.isPending}
                />
              </StepBlock>
              <StepBlock
                n={2}
                title={t("mAssets.withdraw.stepNetwork")}
                state={networkState}
                onChange={openNetworks > 1 ? () => choose({ network: null }) : undefined}
                summary={
                  net && (
                    <ChosenNetwork
                      network={net}
                      detail={`${t("mAssets.common.fee")} ${plainAmount(net.withdraw_fee, decimals)} ${asset} · ${t("mAssets.withdraw.minWithdraw")} ${plainAmount(net.min_withdraw, decimals)} ${asset}`}
                    />
                  )
                }
              >
                {paused && (
                  <Notice tone="warn" className="mb-2">
                    {t("mAssets.withdraw.pausedAll", { asset })}
                  </Notice>
                )}
                <NetworkCards
                  list={list}
                  value={net?.network ?? null}
                  onChange={(n) => choose({ network: n })}
                  purpose="withdraw"
                  asset={asset}
                  decimals={decimals}
                  loading={networks.isPending}
                />
              </StepBlock>
              {net ? (
                <WithdrawForm
                  key={net.network + asset}
                  asset={asset}
                  net={net}
                  decimals={decimals}
                  available={availableOf(spot, "SPOT", asset)}
                  balancesReady={balances.isSuccess}
                  onDone={() => recordsRef.current?.scrollIntoView({ behavior: reduced ? "auto" : "smooth", block: "start" })}
                />
              ) : (
                <>
                  <StepBlock n={3} title={t("mAssets.withdraw.stepAddress")} state="locked" />
                  <StepBlock n={4} title={t("mAssets.withdraw.stepAmount")} state="locked" last />
                </>
              )}
            </>
          )}
        </section>
        <div ref={recordsRef} id="records" className="flex scroll-mt-14 flex-col gap-3">
          <h2 className="px-1 pt-2 text-md font-semibold text-fg-1">{t("mAssets.withdraw.records")}</h2>
          <WithdrawalList networks={all} decimals={meta.decimals} />
        </div>
      </div>
    </PullToRefresh>
  );
}

/**
 * WithdrawForm holds the address and amount of one asset on one network
 * (it starts afresh when either changes), the confirmation and the submit.
 */
function WithdrawForm({
  asset, net, decimals, available, balancesReady, onDone,
}: { asset: string; net: WalletNetwork; decimals: number; available: string; balancesReady: boolean; onDone: () => void }) {
  const { t } = useTranslation();
  const book = useWithdrawAddresses();
  const actions = useWalletActions();
  const stepUp = useStepUp();
  const allowed = useAllowed("WITHDRAW");
  const entries = useMemo(() => (book.data ?? []).filter((e) => e.network === net.network), [book.data, net.network]);
  const usable = entries.filter((e) => isUsable(e));

  const [picked, setPicked] = useState<string | null>(null);
  const [bookMode, setBookMode] = useState<BookMode | null>(null);
  const [amount, setAmount] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [serverError, setServerError] = useState<ServerError | null>(null);
  const idem = useRef<{ body: string; key: string } | null>(null);

  // A lone usable address is chosen by itself.
  const onlyUsable = usable.length === 1 ? usable[0]!.id : null;
  useEffect(() => {
    if (onlyUsable && picked === null) setPicked(onlyUsable);
  }, [onlyUsable, picked]);

  const target = usable.find((e) => e.id === picked) ?? null;
  const check = useAddressCheck({ network: net.network, address: target?.address ?? "", asset, enabled: target !== null });
  const internal = check.data?.valid === true && check.data.internal;
  const refused = check.data && !check.data.valid ? check.data.reason : null;

  const quote = withdrawQuote({ amount, available, fee: net.withdraw_fee, min: net.min_withdraw, decimals, internal });
  const issue = useIssueText(quote, net, asset, decimals);
  const amountError = issue ?? (serverError?.field === "amount" ? serverError.message : undefined);
  const ready = allowed && net.withdraw_enabled && target !== null && !refused && !check.isPending && quote.received !== null && quote.issue === null;

  const edit = (v: string) => {
    setAmount(v);
    if (serverError) setServerError(null);
  };

  const openBook = () => setBookMode(entries.length === 0 && book.isSuccess ? "new" : "list");

  const submit = async () => {
    setConfirming(false);
    if (!target || !quote.received) return;
    const token = await stepUp.ask();
    if (!token) return;
    const body = { asset, network: net.network, address: target.address, amount: quote.received };
    const bodyKey = JSON.stringify(body);
    // One key per intended withdrawal: a retry after a lost response
    // counts once; a refusal replays under its key, so the next try gets a new one.
    if (idem.current?.body !== bodyKey) idem.current = { body: bodyKey, key: newIdempotencyKey() };
    setSubmitting(true);
    setServerError(null);
    try {
      const w = await actions.requestWithdrawal(body, token, idem.current.key);
      idem.current = null;
      setAmount("");
      toast.success(t("mAssets.withdraw.submitted"), { description: t("mAssets.withdraw.submittedHint", { status: enumLabel(w.status, "withdrawal") }) });
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.status < 500) idem.current = null;
      const m = mapServerError(e, FIELDS);
      setServerError({ field: m.field, message: m.message, code: m.code, details: e instanceof ApiError ? e.details : undefined });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <>
      <StepBlock n={3} title={t("mAssets.withdraw.stepAddress")} state="active">
        <AddressField entry={target} count={entries.length} loading={book.isPending} onOpen={openBook} />
        {target && <CheckLine checking={check.isPending} result={check.data} className="mt-2" />}
        {serverError?.field === "address" && (
          <p role="alert" className="mt-2 text-sm text-danger">
            {serverError.message}
          </p>
        )}
      </StepBlock>
      <StepBlock n={4} title={t("mAssets.withdraw.stepAmount")} state="active" last>
        <FormField
          label={t("mAssets.withdraw.amount")}
          error={amountError}
          extra={<span className="tabular-nums">{t("mAssets.withdraw.availableLine", { amount: formatAmount(available, shownDecimals(decimals)), asset })}</span>}
        >
          <NumberInput
            size="lg"
            value={amount}
            onValueChange={edit}
            decimals={decimals}
            maxButton={false}
            placeholder={`${t("mAssets.withdraw.minWithdraw")} ${plainAmount(net.min_withdraw, decimals)}`}
            unit={asset}
            suffix={
              <TextButton disabled={dec.sign(quote.max) <= 0} onClick={() => edit(quote.max)}>
                {t("mAssets.withdraw.all")}
              </TextButton>
            }
          />
        </FormField>
        {balancesReady && !quote.enough && (
          <Notice tone="warn" className="mt-3">
            {t("mAssets.withdraw.notEnough")}
          </Notice>
        )}
        <div className="mt-3 rounded-2 bg-bg-2 p-3">
          <KeyValue
            density="compact"
            items={[
              {
                key: "fee",
                label: t("mAssets.common.fee"),
                value: internal ? (
                  <span className="text-success">
                    0 {asset} · {t("mAssets.common.free")}
                  </span>
                ) : (
                  `${plainAmount(quote.fee, decimals)} ${asset}`
                ),
              },
              { key: "min", label: t("mAssets.withdraw.minWithdraw"), value: `${plainAmount(net.min_withdraw, decimals)} ${asset}` },
              {
                key: "received",
                label: t("mAssets.withdraw.received"),
                value: quote.received ? <span className="font-semibold text-fg-1">{`${formatDecimal(quote.received)} ${asset}`}</span> : "—",
              },
              { key: "total", label: t("mAssets.withdraw.total"), value: quote.total ? `${formatDecimal(quote.total)} ${asset}` : "—" },
            ]}
          />
        </div>
        {serverError?.code === "WALLET_LIMIT_EXCEEDED" && serverError.details ? (
          <LimitBars details={serverError.details} />
        ) : (
          <p className="mt-2 text-xs text-fg-3">{t("mAssets.withdraw.limitsNote")}</p>
        )}
        {serverError && !serverError.field && (
          <Notice tone="danger" role="alert" className="mt-3">
            {serverError.message}
          </Notice>
        )}
        <Button size="lg" block className="mt-4" disabled={!ready} loading={submitting} onClick={() => setConfirming(true)}>
          {t("mAssets.withdraw.submit")}
        </Button>
        {!target && <p className="mt-2 text-center text-xs text-fg-3">{t("mAssets.withdraw.pickAddress")}</p>}
      </StepBlock>
      <AddressBookSheet
        mode={bookMode}
        onMode={setBookMode}
        onClose={() => setBookMode(null)}
        net={net}
        asset={asset}
        entries={entries}
        loading={book.isPending}
        error={book.isError ? book.error : null}
        onRetry={() => void book.refetch()}
        picked={picked}
        onPick={(id) => {
          setPicked(id);
          setServerError(null);
          setBookMode(null);
        }}
        stepUp={stepUp.ask}
      />
      <ConfirmSheet
        open={confirming}
        onOpenChange={setConfirming}
        asset={asset}
        net={net}
        target={target}
        quote={quote}
        decimals={decimals}
        onConfirm={() => void submit()}
      />
      {stepUp.sheet}
    </>
  );
}

/** AddressField shows the chosen address and opens the address book. */
function AddressField({ entry, count, loading, onOpen }: { entry: WithdrawAddress | null; count: number; loading: boolean; onOpen: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-haspopup="dialog"
      className={cn("flex min-h-14 w-full items-center gap-3 rounded-2 border border-line-1 bg-bg-2 px-3 py-2 text-left", PRESS)}
    >
      <BookUser size={18} className="shrink-0 text-fg-3" />
      <span className="min-w-0 flex-1">
        {entry ? (
          <>
            <span className="block truncate text-sm font-medium text-fg-1">{entry.label || t("mAssets.withdraw.noLabel")}</span>
            <span className="block truncate font-mono text-xs text-fg-2">{shortAddress(entry.address, 12, 10)}</span>
          </>
        ) : (
          <span className="block text-sm text-fg-3">
            {loading ? t("common.loading") : count > 0 ? t("mAssets.withdraw.pickFromBook", { count }) : t("mAssets.withdraw.addFirst")}
          </span>
        )}
      </span>
      <ChevronRight size={16} className="shrink-0 text-fg-3" />
    </button>
  );
}

/** ConfirmSheet sums the withdrawal up before the step-up. */
function ConfirmSheet({
  open, onOpenChange, asset, net, target, quote, decimals, onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  asset: string;
  net: WalletNetwork;
  target: WithdrawAddress | null;
  quote: WithdrawQuote;
  decimals: number;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={t("mAssets.withdraw.confirmTitle")}
      closeButton
      footer={
        <Button size="lg" block onClick={onConfirm}>
          {t("mAssets.withdraw.confirmAction")}
        </Button>
      }
    >
      {target && quote.received && quote.total && (
        <div className="flex flex-col gap-4 pt-1">
          <div className="flex items-center gap-3 rounded-2 bg-bg-2 p-3">
            <CoinIcon symbol={asset} size={36} />
            <div className="min-w-0">
              <div className="break-all text-lg font-semibold tabular-nums text-fg-1">
                {formatDecimal(quote.received)} {asset}
              </div>
              <div className="text-xs text-fg-3">{t("mAssets.withdraw.received")}</div>
            </div>
          </div>
          <KeyValue
            items={[
              { key: "network", label: t("mAssets.common.network"), value: `${net.display_name} (${net.network})` },
              { key: "address", label: t("mAssets.common.address"), value: <span className="font-mono text-xs">{target.address}</span> },
              { key: "fee", label: t("mAssets.common.fee"), value: `${plainAmount(quote.fee, decimals)} ${asset}` },
              { key: "total", label: t("mAssets.withdraw.total"), value: `${formatDecimal(quote.total)} ${asset}` },
            ]}
          />
          <Notice tone="warn">{t("mAssets.withdraw.confirmWarn")}</Notice>
        </div>
      )}
    </Sheet>
  );
}

/** useIssueText says what is wrong with the amount, in place under the field. */
function useIssueText(q: WithdrawQuote, net: WalletNetwork, asset: string, decimals: number): string | undefined {
  const { t } = useTranslation();
  switch (q.issue) {
    case "format":
      return t("errors.format");
    case "zero":
      return t("errors.zero");
    case "precision":
      return t("errors.precision", { n: decimals });
    case "belowMin":
      return t("mAssets.withdraw.issueBelowMin", { min: plainAmount(net.min_withdraw, decimals), asset });
    case "insufficient":
      return t("mAssets.withdraw.issueInsufficient", { fee: plainAmount(q.fee, decimals), asset });
    default:
      return undefined;
  }
}

/** LimitBars shows the daily and monthly use a refused withdrawal reported (WALLET_LIMIT_EXCEEDED). */
function LimitBars({ details }: { details: Record<string, unknown> }) {
  const { t } = useTranslation();
  const str = (k: string) => (typeof details[k] === "string" && dec.isDecimal(details[k] as string) ? (details[k] as string) : null);
  const bars = [
    { key: "daily", label: t("mAssets.withdraw.limitDaily"), used: str("used_today"), limit: str("daily_limit") },
    { key: "monthly", label: t("mAssets.withdraw.limitMonthly"), used: str("used_this_month"), limit: str("monthly_limit") },
  ].filter((b) => b.used !== null && b.limit !== null);
  const value = str("value_usdt");
  if (bars.length === 0) return null;
  return (
    <div className="mt-3 flex flex-col gap-3 rounded-2 border border-warn/40 bg-warn/10 p-3">
      {bars.map((b) => (
        <Progress
          key={b.key}
          label={b.label}
          tone={dec.gte(b.used!, b.limit!) ? "danger" : "warn"}
          value={dec.toNumber(b.used)}
          max={Math.max(dec.toNumber(b.limit), 1e-9)}
          valueText={t("mAssets.withdraw.limitValue", {
            used: formatDecimal(b.used, { decimals: 2 }),
            limit: formatDecimal(b.limit, { decimals: 2 }),
          })}
        />
      ))}
      {value && <p className="text-xs text-fg-2">{t("mAssets.withdraw.limitThis", { value: formatDecimal(value, { decimals: 2 }) })}</p>}
    </div>
  );
}
