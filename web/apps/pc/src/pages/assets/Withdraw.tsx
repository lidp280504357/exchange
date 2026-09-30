import { ApiError, dec, errorText, formatAmount, formatDecimal, newIdempotencyKey, useSettings, timeZoneOf, formatTime } from "@exchange/core";
import { availableOf, useBalances } from "@exchange/core/assets/hooks";
import { checkAddress } from "@exchange/core/wallet/address";
import { useAddressCheck, useWalletActions, useWalletNetworks, useWalletPushes, useWithdrawAddresses } from "@exchange/core/wallet/hooks";
import { assetsFor, isUsable, walletFlow, type WalletNetwork, type WithdrawAddress } from "@exchange/core/wallet/networks";
import { withdrawQuote, type WithdrawQuote } from "@exchange/core/wallet/withdraw";
import {
  Badge,
  Button,
  CoinIcon,
  Dialog,
  EmptyState,
  ErrorState,
  FormField,
  IconButton,
  Input,
  KeyValue,
  NumberInput,
  Popover,
  PopoverClose,
  Progress,
  Segmented,
  Skeleton,
  Spinner,
  cn,
  mapServerError,
  toast,
} from "@exchange/ui";
import { BadgeCheck, BookUser, CircleCheck, CircleX, Clock, KeyRound, ShieldCheck, Trash2, UserCheck } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useSearchParams } from "react-router";
import { useStepUp } from "../../features/auth/StepUp";
import { AssetsLayout, Card, Tips } from "./parts/AssetsLayout";
import { codeLabel, plainAmount, shownDecimals, useAssetMeta, useTradeLinks } from "./parts/meta";
import { EligibilityNotice, Notice, useAllowed } from "./parts/Notice";
import { CoinPicker, InternalOnly, NetworkCards } from "./parts/Picker";
import { WithdrawalList } from "./parts/Records";
import { StepSection } from "./parts/StepSection";

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
 * Withdraw (design §6.2): coin → network → address (the address book, or a
 * new address checked locally as it is typed and then by the server) →
 * amount (fee on top, what arrives, the minimum) → a confirmation → step-up
 * → submit. Below, the withdrawals with their timelines, cancelable until
 * signing starts; the pushes move them live.
 */
export default function Withdraw() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const meta = useAssetMeta();
  const links = useTradeLinks();
  const networks = useWalletNetworks();
  const balances = useBalances();
  const recordsRef = useRef<HTMLDivElement>(null);
  useWalletPushes();

  const all = networks.data ?? [];
  const coins = assetsFor(all, "withdraw");
  const asset = (params.get("asset") ?? "").toUpperCase();
  const { list, network: net, internalOnly, paused } = walletFlow(networks.data, asset, params.get("network"), "withdraw");
  const network = net?.network ?? null;
  const openNetworks = list.filter((n) => n.withdraw_enabled).length;
  const decimals = meta.decimals(asset);
  const spot = balances.data?.balances;

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

  const coinState = !asset || internalOnly ? "active" : "done";
  const networkState = !asset || internalOnly ? "locked" : net ? "done" : "active";

  return (
    <AssetsLayout title={t("pcAssets.withdraw.title")} subtitle={t("pcAssets.withdraw.subtitle")}>
      <EligibilityNotice feature="WITHDRAW" title={t("pcAssets.withdraw.title")} />
      {balances.isError && (
        <Notice tone="danger" role="alert" className="mb-4">
          <span className="flex flex-wrap items-center justify-between gap-2">
            {errorText(balances.error)}
            <Button size="sm" variant="secondary" onClick={() => void balances.refetch()}>
              {t("common.retry")}
            </Button>
          </span>
        </Notice>
      )}
      <div className="grid grid-cols-[minmax(0,1fr)_320px] items-start gap-4">
        <Card bodyClassName="p-6">
          {networks.isError ? (
            <ErrorState compact message={errorText(networks.error)} onRetry={() => void networks.refetch()} />
          ) : (
            <>
              <StepSection
                n={1}
                title={t("pcAssets.withdraw.stepCoin")}
                state={coinState}
                onChange={() => choose({ asset: "" })}
                summary={
                  <span className="flex items-center gap-2 text-sm">
                    <CoinIcon symbol={asset} size={22} />
                    <span className="font-medium text-fg-1">{asset}</span>
                    <span className="text-fg-3">{meta.name(asset)}</span>
                    <span className="ml-2 text-fg-2">
                      {t("pcAssets.withdraw.availableLine", { amount: formatAmount(availableOf(spot, "SPOT", asset), shownDecimals(decimals)), asset })}
                    </span>
                  </span>
                }
              >
                {!networks.isPending && coins.length === 0 && <p className="mb-3 text-sm text-fg-3">{t("pcAssets.withdraw.noneOpen")}</p>}
                <CoinPicker
                  coins={coins}
                  value={asset}
                  onChange={(a) => choose({ asset: a })}
                  meta={meta}
                  loading={networks.isPending}
                  trailing={(a) =>
                    t("pcAssets.withdraw.availableLine", { amount: formatAmount(availableOf(spot, "SPOT", a), shownDecimals(meta.decimals(a))), asset: a })
                  }
                />
                {internalOnly && (
                  <div className="mt-4">
                    <InternalOnly asset={asset} name={meta.name(asset)} trade={links.spot(asset)} />
                  </div>
                )}
              </StepSection>
              <StepSection
                n={2}
                title={t("pcAssets.withdraw.stepNetwork")}
                state={networkState}
                onChange={openNetworks > 1 ? () => choose({ network: null }) : undefined}
                summary={
                  net && (
                    <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
                      <span className="font-medium text-fg-1">{net.display_name}</span>
                      <span className="text-fg-3">{net.network}</span>
                      <span className="text-fg-2">
                        {t("pcAssets.withdraw.fee")} {plainAmount(net.withdraw_fee, decimals)} {asset}
                      </span>
                      <span className="text-fg-2">
                        {t("pcAssets.withdraw.minWithdraw")} {plainAmount(net.min_withdraw, decimals)} {asset}
                      </span>
                    </span>
                  )
                }
              >
                {paused && (
                  <Notice tone="warn" className="mb-3">
                    {t("pcAssets.withdraw.pausedAll", { asset })}
                  </Notice>
                )}
                <NetworkCards
                  list={list}
                  value={network}
                  onChange={(n) => choose({ network: n })}
                  purpose="withdraw"
                  asset={asset}
                  decimals={decimals}
                  loading={networks.isPending}
                />
              </StepSection>
              {net ? (
                <WithdrawForm
                  key={net.network + asset}
                  asset={asset}
                  net={net}
                  decimals={decimals}
                  available={availableOf(spot, "SPOT", asset)}
                  balancesReady={balances.isSuccess}
                  onDone={() => recordsRef.current?.scrollIntoView({ behavior: "smooth", block: "start" })}
                />
              ) : (
                <>
                  <StepSection n={3} title={t("pcAssets.withdraw.stepAddress")} state="locked" />
                  <StepSection n={4} title={t("pcAssets.withdraw.stepAmount")} state="locked" last />
                </>
              )}
            </>
          )}
        </Card>
        <Tips
          title={t("pcAssets.withdraw.tipsTitle")}
          tips={[
            [<BookUser key="1" size={14} />, t("pcAssets.withdraw.tip1")],
            [<KeyRound key="2" size={14} />, t("pcAssets.withdraw.tip2")],
            [<ShieldCheck key="3" size={14} />, t("pcAssets.withdraw.tip3")],
            [<BadgeCheck key="4" size={14} />, t("pcAssets.withdraw.tip4")],
          ]}
        />
      </div>
      <div ref={recordsRef} id="records" className="mt-4 scroll-mt-20">
        <Card title={t("pcAssets.withdraw.records")} bodyClassName="p-4">
          <WithdrawalList networks={all} decimals={meta.decimals} />
        </Card>
      </div>
    </AssetsLayout>
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
  const entries = (book.data ?? []).filter((e) => e.network === net.network);
  const usable = entries.filter((e) => isUsable(e));

  const [mode, setMode] = useState<"book" | "new">("book");
  const [picked, setPicked] = useState<string | null>(null);
  const [amount, setAmount] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [serverError, setServerError] = useState<ServerError | null>(null);
  const idem = useRef<{ body: string; key: string } | null>(null);

  // An empty book opens the new-address form; a lone usable address is chosen by itself.
  const loaded = book.isSuccess;
  const onlyUsable = usable.length === 1 ? usable[0]!.id : null;
  useEffect(() => {
    if (loaded && entries.length === 0) setMode("new");
  }, [loaded, entries.length]);
  useEffect(() => {
    if (onlyUsable && picked === null) setPicked(onlyUsable);
  }, [onlyUsable, picked]);

  const entry = usable.find((e) => e.id === picked) ?? null;
  const target = mode === "book" ? entry : null;
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
      toast.success(t("pcAssets.withdraw.submitted"), { description: t("pcAssets.withdraw.submittedHint", { status: codeLabel(w.status, "withdrawal") }) });
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
      <StepSection n={3} title={t("pcAssets.withdraw.stepAddress")} state="active">
        <Segmented
          value={mode}
          onValueChange={(v) => {
            setMode(v as "book" | "new");
            setServerError(null);
          }}
          aria-label={t("pcAssets.withdraw.stepAddress")}
          items={[
            { value: "book", label: `${t("pcAssets.withdraw.book")} (${entries.length})` },
            { value: "new", label: t("pcAssets.withdraw.newAddress") },
          ]}
        />
        <div className="mt-3">
          {mode === "book" ? (
            <AddressBook
              loading={book.isPending}
              error={book.isError ? book.error : null}
              onRetry={() => void book.refetch()}
              entries={entries}
              picked={picked}
              onPick={(id) => {
                setPicked(id);
                setServerError(null);
              }}
              onAdd={() => setMode("new")}
              network={net}
            />
          ) : (
            <NewAddress
              net={net}
              asset={asset}
              onSaved={(e) => {
                setMode("book");
                if (isUsable(e)) setPicked(e.id);
              }}
              stepUp={stepUp.ask}
              save={actions.addAddress}
            />
          )}
          {mode === "book" && target && <CheckLine checking={check.isPending} result={check.data} />}
          {serverError?.field === "address" && (
            <p role="alert" className="mt-2 text-sm text-danger">
              {serverError.message}
            </p>
          )}
        </div>
      </StepSection>
      <StepSection n={4} title={t("pcAssets.withdraw.stepAmount")} state="active" last>
        <FormField
          label={t("pcAssets.withdraw.amount")}
          error={amountError}
          extra={
            <span className="tabular-nums">
              {t("pcAssets.withdraw.availableLine", { amount: formatAmount(available, shownDecimals(decimals)), asset })}
            </span>
          }
        >
          <NumberInput
            size="lg"
            value={amount}
            onValueChange={edit}
            decimals={decimals}
            maxButton={false}
            placeholder={t("pcAssets.withdraw.minWithdraw") + " " + plainAmount(net.min_withdraw, decimals)}
            unit={asset}
            suffix={
              <button
                type="button"
                disabled={dec.sign(quote.max) <= 0}
                onClick={() => edit(quote.max)}
                className="mr-2 text-sm font-medium text-brand hover:brightness-110 disabled:opacity-50"
              >
                {t("pcAssets.withdraw.all")}
              </button>
            }
          />
        </FormField>
        {balancesReady && !quote.enough && (
          <Notice tone="warn" className="mt-3">
            {t("pcAssets.withdraw.notEnough")}
          </Notice>
        )}
        <div className="mt-4 rounded-2 bg-bg-2 p-4">
          <KeyValue
            density="compact"
            items={[
              {
                key: "fee",
                label: t("pcAssets.withdraw.fee"),
                value: internal ? (
                  <span className="text-success">
                    0 {asset} · {t("pcAssets.common.free")}
                  </span>
                ) : (
                  `${plainAmount(quote.fee, decimals)} ${asset}`
                ),
              },
              { key: "min", label: t("pcAssets.withdraw.minWithdraw"), value: `${plainAmount(net.min_withdraw, decimals)} ${asset}` },
              {
                key: "received",
                label: t("pcAssets.withdraw.received"),
                value: quote.received ? <span className="font-semibold text-fg-1">{`${formatDecimal(quote.received)} ${asset}`}</span> : "—",
              },
              { key: "total", label: t("pcAssets.withdraw.total"), value: quote.total ? `${formatDecimal(quote.total)} ${asset}` : "—" },
            ]}
          />
        </div>
        {serverError?.code === "WALLET_LIMIT_EXCEEDED" && serverError.details ? (
          <LimitBars details={serverError.details} />
        ) : (
          <p className="mt-3 text-xs text-fg-3">{t("pcAssets.withdraw.limitsNote")}</p>
        )}
        {serverError && !serverError.field && (
          <Notice tone="danger" role="alert" className="mt-4">
            {serverError.message}
          </Notice>
        )}
        <Button
          size="lg"
          block
          className="mt-5"
          disabled={!ready}
          loading={submitting}
          onClick={() => setConfirming(true)}
          title={!target ? t("pcAssets.withdraw.pickAddress") : undefined}
        >
          {t("pcAssets.withdraw.submit")}
        </Button>
        {!target && <p className="mt-2 text-center text-xs text-fg-3">{t("pcAssets.withdraw.pickAddress")}</p>}
      </StepSection>
      <Dialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t("pcAssets.withdraw.confirmTitle")}
        onConfirm={() => void submit()}
        confirmText={t("pcAssets.withdraw.confirmAction")}
      >
        {target && quote.received && quote.total && (
          <div className="flex flex-col gap-4">
            <div className="flex items-center gap-3 rounded-2 bg-bg-2 p-3">
              <CoinIcon symbol={asset} size={32} />
              <div>
                <div className="text-lg font-semibold tabular-nums text-fg-1">
                  {formatDecimal(quote.received)} {asset}
                </div>
                <div className="text-xs text-fg-3">{t("pcAssets.withdraw.received")}</div>
              </div>
            </div>
            <KeyValue
              density="compact"
              items={[
                { key: "network", label: t("pcAssets.common.network"), value: `${net.display_name} (${net.network})` },
                { key: "address", label: t("pcAssets.common.address"), value: <span className="font-mono text-xs">{target.address}</span> },
                { key: "fee", label: t("pcAssets.withdraw.fee"), value: `${plainAmount(quote.fee, decimals)} ${asset}` },
                { key: "total", label: t("pcAssets.withdraw.total"), value: `${formatDecimal(quote.total)} ${asset}` },
              ]}
            />
            <Notice tone="warn">{t("pcAssets.withdraw.confirmWarn")}</Notice>
          </div>
        )}
      </Dialog>
      {stepUp.dialog}
    </>
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
      return t("pcAssets.withdraw.issueBelowMin", { min: plainAmount(net.min_withdraw, decimals), asset });
    case "insufficient":
      return t("pcAssets.withdraw.issueInsufficient", { fee: plainAmount(q.fee, decimals), asset });
    default:
      return undefined;
  }
}

/** CheckLine shows the server's verdict on an address. */
function CheckLine({ checking, result }: { checking: boolean; result?: { valid: boolean; internal: boolean; reason: string | null } }) {
  const { t } = useTranslation();
  if (checking) {
    return (
      <p className="mt-2 flex items-center gap-2 text-sm text-fg-3">
        <Spinner size={12} /> {t("pcAssets.withdraw.checking")}
      </p>
    );
  }
  if (!result) return null;
  if (!result.valid) {
    return (
      <p role="alert" className="mt-2 flex items-center gap-2 text-sm text-danger">
        <CircleX size={14} /> {t(`pcAssets.withdraw.reasons.${result.reason ?? "ADDRESS_FORMAT"}`)}
      </p>
    );
  }
  return (
    <p className="mt-2 flex items-center gap-2 text-sm text-success">
      {result.internal ? <UserCheck size={14} /> : <CircleCheck size={14} />}
      {result.internal ? t("pcAssets.withdraw.internal") : t("pcAssets.withdraw.addressOk")}
    </p>
  );
}

function AddressBook({
  loading, error, onRetry, entries, picked, onPick, onAdd, network,
}: {
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  entries: WithdrawAddress[];
  picked: string | null;
  onPick: (id: string) => void;
  onAdd: () => void;
  network: WalletNetwork;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const zone = useSettings((s) => timeZoneOf(s));
  const actions = useWalletActions();
  const [removing, setRemoving] = useState<string | null>(null);

  if (loading) {
    return (
      <div className="flex flex-col gap-2">
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-14 w-full rounded-2" />
        ))}
      </div>
    );
  }
  if (error) return <ErrorState compact message={errorText(error)} onRetry={onRetry} />;
  if (entries.length === 0) {
    return (
      <EmptyState
        compact
        title={t("pcAssets.withdraw.bookEmpty", { network: network.display_name })}
        description={t("pcAssets.withdraw.bookEmptyHint")}
        action={
          <Button size="sm" onClick={onAdd}>
            {t("pcAssets.withdraw.addNew")}
          </Button>
        }
      />
    );
  }

  const remove = async (id: string) => {
    setRemoving(id);
    try {
      await actions.removeAddress(id);
      toast.success(t("pcAssets.withdraw.removed"));
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setRemoving(null);
    }
  };

  return (
    <div role="radiogroup" aria-label={t("pcAssets.withdraw.book")} className="flex flex-col gap-2">
      {entries.map((e) => {
        const ready = isUsable(e);
        const on = picked === e.id && ready;
        return (
          <div
            key={e.id}
            className={cn(
              "flex items-center gap-3 rounded-2 border p-3 transition-colors duration-[var(--t-fast)]",
              on ? "border-brand bg-brand-soft" : "border-line-1 bg-bg-2",
              !ready && "opacity-70",
            )}
          >
            <button
              type="button"
              role="radio"
              aria-checked={on}
              disabled={!ready}
              onClick={() => onPick(e.id)}
              className="flex min-w-0 flex-1 items-center gap-3 text-left disabled:cursor-not-allowed"
            >
              <span aria-hidden className={cn("grid size-4 shrink-0 place-items-center rounded-full border", on ? "border-brand" : "border-line-2")}>
                {on && <span className="size-2 rounded-full bg-brand" />}
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2">
                  <span className="truncate text-sm font-medium text-fg-1">{e.label || t("pcAssets.withdraw.noLabel")}</span>
                  {ready ? (
                    <Badge tone="success">{t("pcAssets.withdraw.usable")}</Badge>
                  ) : (
                    <Badge tone="warn" icon={<Clock size={10} />}>
                      {t("pcAssets.withdraw.cooling")}
                    </Badge>
                  )}
                </span>
                <span className="mt-0.5 block truncate font-mono text-xs text-fg-3">{e.address}</span>
                {!ready && (
                  <span className="mt-0.5 block text-xs text-warn">
                    {t("pcAssets.withdraw.coolingUntil", { time: formatTime(e.usable_at, "datetime", locale, zone) })}
                  </span>
                )}
              </span>
            </button>
            <Popover
              align="end"
              trigger={<IconButton icon={<Trash2 />} label={t("pcAssets.withdraw.remove")} size="sm" disabled={removing === e.id} />}
            >
              <p className="max-w-56 text-sm text-fg-1">{t("pcAssets.withdraw.removeConfirm")}</p>
              <div className="mt-3 flex justify-end gap-2">
                <PopoverClose asChild>
                  <Button size="sm" variant="secondary">
                    {t("common.cancel")}
                  </Button>
                </PopoverClose>
                <PopoverClose asChild>
                  <Button size="sm" variant="danger" onClick={() => void remove(e.id)}>
                    {t("pcAssets.withdraw.remove")}
                  </Button>
                </PopoverClose>
              </div>
            </Popover>
          </div>
        );
      })}
    </div>
  );
}

function NewAddress({
  net, asset, onSaved, stepUp, save,
}: {
  net: WalletNetwork;
  asset: string;
  onSaved: (e: WithdrawAddress) => void;
  stepUp: () => Promise<string | null>;
  save: (body: { network: string; address: string; label?: string }, token: string) => Promise<WithdrawAddress>;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const zone = useSettings((s) => timeZoneOf(s));
  const [address, setAddress] = useState("");
  const [label, setLabel] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const local = checkAddress(address, { format: net.address_format, chain: net.chain, memoRequired: false });
  const remote = useAddressCheck({ network: net.network, address, asset, enabled: local.ok });
  const typed = address.trim() !== "";
  const localError = typed && !local.ok && local.reason ? t(`pcAssets.withdraw.reasons.${local.reason}`) : null;
  const remoteError =
    remote.data && !remote.data.valid
      ? t(`pcAssets.withdraw.reasons.${remote.data.reason ?? "ADDRESS_FORMAT"}`)
      : remote.error && !remote.settling
        ? errorText(remote.error)
        : null;
  const valid = local.ok && !remote.settling && remote.data?.valid === true;

  const submit = async () => {
    if (!valid) return;
    const token = await stepUp();
    if (!token) return;
    setSaving(true);
    setError(null);
    try {
      const entry = await save({ network: net.network, address: remote.data?.normalized ?? address.trim(), label: label.trim() || undefined }, token);
      toast.success(t("pcAssets.withdraw.saved"), {
        description: isUsable(entry) ? undefined : t("pcAssets.withdraw.savedCooling", { time: formatTime(entry.usable_at, "datetime", locale, zone) }),
      });
      setAddress("");
      setLabel("");
      onSaved(entry);
    } catch (e) {
      setError(mapServerError(e).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <FormField label={t("pcAssets.withdraw.addressLabel")} error={localError ?? remoteError ?? error ?? undefined}>
        <Input
          value={address}
          onValueChange={(v) => {
            setAddress(v);
            setError(null);
          }}
          placeholder={t("pcAssets.withdraw.addressPlaceholder", { network: net.display_name })}
          autoComplete="off"
          spellCheck={false}
          clearable
          className="font-mono"
        />
      </FormField>
      {local.ok && !remoteError && (
        <CheckLine checking={remote.settling || remote.isFetching} result={remote.settling ? undefined : remote.data} />
      )}
      {local.ok && local.unverified && !remote.data && !remote.settling && !remote.isFetching && (
        <p className="text-xs text-fg-3">{t("pcAssets.withdraw.checksumPending")}</p>
      )}
      <FormField label={t("pcAssets.withdraw.labelLabel")} optional>
        <Input value={label} onValueChange={setLabel} maxLength={50} placeholder={t("pcAssets.withdraw.labelPlaceholder")} />
      </FormField>
      <Notice tone="info">{t("pcAssets.withdraw.coolingHint")}</Notice>
      <div>
        <Button onClick={() => void submit()} disabled={!valid} loading={saving} icon={<BookUser size={16} />}>
          {t("pcAssets.withdraw.save")}
        </Button>
      </div>
    </div>
  );
}

/** LimitBars shows the daily and monthly use a refused withdrawal reported (WALLET_LIMIT_EXCEEDED). */
function LimitBars({ details }: { details: Record<string, unknown> }) {
  const { t } = useTranslation();
  const str = (k: string) => (typeof details[k] === "string" && dec.isDecimal(details[k] as string) ? (details[k] as string) : null);
  const bars = [
    { key: "daily", label: t("pcAssets.withdraw.limitDaily"), used: str("used_today"), limit: str("daily_limit") },
    { key: "monthly", label: t("pcAssets.withdraw.limitMonthly"), used: str("used_this_month"), limit: str("monthly_limit") },
  ].filter((b) => b.used !== null && b.limit !== null);
  const value = str("value_usdt");
  if (bars.length === 0) return null;
  return (
    <div className="mt-4 flex flex-col gap-3 rounded-2 border border-warn/40 bg-warn/10 p-4">
      {bars.map((b) => {
        const over = dec.gte(b.used!, b.limit!);
        return (
          <Progress
            key={b.key}
            label={b.label}
            tone={over ? "danger" : "warn"}
            value={dec.toNumber(b.used)}
            max={Math.max(dec.toNumber(b.limit), 1e-9)}
            valueText={t("pcAssets.withdraw.limitValue", { used: formatDecimal(b.used, { decimals: 2 }), limit: formatDecimal(b.limit, { decimals: 2 }) })}
          />
        );
      })}
      {value && <p className="text-xs text-fg-2">{t("pcAssets.withdraw.limitThis", { value: formatDecimal(value, { decimals: 2 }) })}</p>}
    </div>
  );
}
