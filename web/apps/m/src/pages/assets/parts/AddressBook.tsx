import { errorText, formatTime, timeZoneOf, useSettings } from "@exchange/core";
import { checkAddress } from "@exchange/core/wallet/address";
import { useAddressCheck, useWalletActions, type AddressVerdict } from "@exchange/core/wallet/hooks";
import { isUsable, type WalletNetwork, type WithdrawAddress } from "@exchange/core/wallet/networks";
import { Badge, Button, EmptyState, ErrorState, FormField, Input, Sheet, Skeleton, Spinner, cn, mapServerError, toast } from "@exchange/ui";
import { ArrowLeft, CircleCheck, CircleX, Clock, Plus, Search, Trash2, UserCheck } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { RETRY, useKept } from "./bits";
import { matchAddress } from "./logic";
import { Notice } from "./Notice";

export type BookMode = "list" | "new";

/** CheckLine shows the server's verdict on an address: valid, another user's (no fee), or why not. */
export function CheckLine({ checking, result, className }: { checking: boolean; result?: AddressVerdict; className?: string }) {
  const { t } = useTranslation();
  if (checking) {
    return (
      <p className={cn("flex items-center gap-2 text-sm text-fg-3", className)}>
        <Spinner size={12} /> {t("mAssets.withdraw.checking")}
      </p>
    );
  }
  if (!result) return null;
  if (!result.valid) {
    return (
      <p role="alert" className={cn("flex items-center gap-2 text-sm text-danger", className)}>
        <CircleX size={14} className="shrink-0" /> {t(`mAssets.withdraw.reasons.${result.reason ?? "ADDRESS_FORMAT"}`)}
      </p>
    );
  }
  return (
    <p className={cn("flex items-center gap-2 text-sm text-success", className)}>
      {result.internal ? <UserCheck size={14} className="shrink-0" /> : <CircleCheck size={14} className="shrink-0" />}
      {result.internal ? t("mAssets.withdraw.internal") : t("mAssets.withdraw.addressOk")}
    </p>
  );
}

/**
 * AddressBookSheet is the withdrawal address step in a bottom sheet
 * (design §7.2): the network's saved addresses, searchable, each ready or
 * still cooling off, removable after a confirmation; or a new address,
 * checked locally as it is typed and then by the server, saved with a
 * step-up. mode null closes it.
 */
export function AddressBookSheet({
  mode, onMode, onClose, net, asset, entries, loading, error, onRetry, picked, onPick, stepUp,
}: {
  mode: BookMode | null;
  onMode: (mode: BookMode) => void;
  onClose: () => void;
  net: WalletNetwork;
  asset: string;
  /** The book's entries of this network. */
  entries: WithdrawAddress[];
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  picked: string | null;
  /** Picks a ready entry (and closes the sheet). */
  onPick: (id: string) => void;
  stepUp: () => Promise<string | null>;
}) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const zone = useSettings((s) => timeZoneOf(s));
  const actions = useWalletActions();
  const view = useKept(mode) ?? "list";

  const [address, setAddress] = useState("");
  const [label, setLabel] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const local = checkAddress(address, { format: net.address_format, chain: net.chain, memoRequired: false });
  const remote = useAddressCheck({ network: net.network, address, asset, enabled: view === "new" && local.ok });
  const typed = address.trim() !== "";
  const localError = typed && !local.ok && local.reason ? t(`mAssets.withdraw.reasons.${local.reason}`) : null;
  const remoteError =
    remote.data && !remote.data.valid
      ? t(`mAssets.withdraw.reasons.${remote.data.reason ?? "ADDRESS_FORMAT"}`)
      : remote.error && !remote.settling
        ? errorText(remote.error)
        : null;
  const valid = local.ok && !remote.settling && remote.data?.valid === true;

  const save = async () => {
    if (!valid) return;
    // The step-up sheet opens over this one; closing it keeps the form.
    const token = await stepUp();
    if (!token) return;
    setSaving(true);
    setSaveError(null);
    try {
      const entry = await actions.addAddress(
        { network: net.network, address: remote.data?.normalized ?? address.trim(), label: label.trim() || undefined },
        token,
      );
      toast.success(t("mAssets.withdraw.saved"), {
        description: isUsable(entry) ? undefined : t("mAssets.withdraw.savedCooling", { time: formatTime(entry.usable_at, "datetime", locale, zone) }),
      });
      setAddress("");
      setLabel("");
      if (isUsable(entry)) onPick(entry.id);
      else onMode("list");
    } catch (e) {
      setSaveError(mapServerError(e).message);
    } finally {
      setSaving(false);
    }
  };

  const footer =
    view === "list" ? (
      entries.length > 0 ? (
        <Button size="lg" variant="secondary" block icon={<Plus size={18} />} onClick={() => onMode("new")}>
          {t("mAssets.withdraw.addNew")}
        </Button>
      ) : undefined
    ) : (
      <Button size="lg" block disabled={!valid} loading={saving} onClick={() => void save()}>
        {t("mAssets.withdraw.save")}
      </Button>
    );

  return (
    <Sheet
      open={mode !== null}
      onOpenChange={(o) => !o && !saving && onClose()}
      title={t(view === "list" ? "mAssets.withdraw.bookTitle" : "mAssets.withdraw.newTitle", { network: net.display_name })}
      closeButton
      footer={footer}
    >
      {view === "list" ? (
        <BookList
          entries={entries}
          loading={loading}
          error={error}
          onRetry={onRetry}
          picked={picked}
          onPick={onPick}
          onAdd={() => onMode("new")}
          network={net}
          time={(v) => formatTime(v, "datetime", locale, zone)}
        />
      ) : (
        <div className="flex flex-col gap-3 pt-1">
          {entries.length > 0 && (
            <button type="button" onClick={() => onMode("list")} className="-ml-1 flex min-h-tap items-center gap-1 self-start pr-2 text-sm text-fg-2">
              <ArrowLeft size={16} />
              {t("mAssets.withdraw.backToBook")}
            </button>
          )}
          <FormField label={t("mAssets.withdraw.addressLabel")} error={localError ?? remoteError ?? saveError ?? undefined}>
            <Input
              size="lg"
              value={address}
              onValueChange={(v) => {
                setAddress(v);
                setSaveError(null);
              }}
              placeholder={t("mAssets.withdraw.addressPlaceholder", { network: net.display_name })}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              enterKeyHint="next"
              clearable
              onClear={() => setAddress("")}
              className="font-mono"
            />
          </FormField>
          {local.ok && !remoteError && <CheckLine checking={remote.settling || remote.isFetching} result={remote.settling ? undefined : remote.data} />}
          {local.ok && local.unverified && !remote.data && !remote.settling && !remote.isFetching && (
            <p className="text-xs text-fg-3">{t("mAssets.withdraw.checksumPending")}</p>
          )}
          <FormField label={t("mAssets.withdraw.labelLabel")} optional>
            <Input size="lg" value={label} onValueChange={setLabel} maxLength={50} placeholder={t("mAssets.withdraw.labelPlaceholder")} enterKeyHint="done" />
          </FormField>
          <Notice tone="info">{t("mAssets.withdraw.coolingHint")}</Notice>
        </div>
      )}
    </Sheet>
  );
}

function BookList({
  entries, loading, error, onRetry, picked, onPick, onAdd, network, time,
}: {
  entries: WithdrawAddress[];
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  picked: string | null;
  onPick: (id: string) => void;
  onAdd: () => void;
  network: WalletNetwork;
  time: (v: string) => string;
}) {
  const { t } = useTranslation();
  const actions = useWalletActions();
  const [query, setQuery] = useState("");
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);

  if (loading) {
    return (
      <div className="flex flex-col gap-2 pt-1" aria-hidden>
        {[0, 1].map((i) => (
          <Skeleton key={i} className="h-20 w-full rounded-2" />
        ))}
      </div>
    );
  }
  if (error) return <ErrorState compact message={errorText(error)} onRetry={onRetry} className={RETRY} />;
  if (entries.length === 0) {
    return (
      <EmptyState
        compact
        title={t("mAssets.withdraw.bookEmpty", { network: network.display_name })}
        description={t("mAssets.withdraw.bookEmptyHint")}
        action={
          <Button className="h-tap" icon={<Plus size={16} />} onClick={onAdd}>
            {t("mAssets.withdraw.addNew")}
          </Button>
        }
      />
    );
  }

  const shown = entries.filter((e) => matchAddress(e, query));
  const remove = async (id: string) => {
    setRemoving(id);
    try {
      await actions.removeAddress(id);
      toast.success(t("mAssets.withdraw.removed"));
      setConfirmId(null);
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setRemoving(null);
    }
  };

  return (
    <div className="flex flex-col gap-2 pt-1">
      <Input
        size="lg"
        value={query}
        onValueChange={setQuery}
        clearable
        onClear={() => setQuery("")}
        prefix={<Search size={16} />}
        placeholder={t("mAssets.withdraw.searchBook")}
        aria-label={t("mAssets.withdraw.searchBook")}
        enterKeyHint="search"
        autoComplete="off"
      />
      <div role="radiogroup" aria-label={t("mAssets.withdraw.book")} className="flex flex-col gap-2">
        {shown.map((e) => {
          const ready = isUsable(e);
          const on = picked === e.id && ready;
          return (
            <div key={e.id} className={cn("rounded-2 border", on ? "border-brand bg-brand-soft" : "border-line-1 bg-bg-2")}>
              <div className="flex items-start">
                <button
                  type="button"
                  role="radio"
                  aria-checked={on}
                  disabled={!ready}
                  onClick={() => onPick(e.id)}
                  className="flex min-h-16 min-w-0 flex-1 items-start gap-3 p-3 text-left active:opacity-80 disabled:cursor-not-allowed"
                >
                  <span aria-hidden className={cn("mt-0.5 grid size-4 shrink-0 place-items-center rounded-full border", on ? "border-brand" : "border-line-2")}>
                    {on && <span className="size-2 rounded-full bg-brand" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-sm font-medium text-fg-1">{e.label || t("mAssets.withdraw.noLabel")}</span>
                      {ready ? (
                        <Badge tone="success">{t("mAssets.withdraw.usable")}</Badge>
                      ) : (
                        <Badge tone="warn" icon={<Clock size={10} />}>
                          {t("mAssets.withdraw.cooling")}
                        </Badge>
                      )}
                    </span>
                    <span className="mt-1 block break-all font-mono text-xs text-fg-2">{e.address}</span>
                    {!ready && <span className="mt-1 block text-xs text-warn">{t("mAssets.withdraw.coolingUntil", { time: time(e.usable_at) })}</span>}
                  </span>
                </button>
                <button
                  type="button"
                  aria-label={t("mAssets.withdraw.remove")}
                  aria-expanded={confirmId === e.id}
                  disabled={removing === e.id}
                  onClick={() => setConfirmId((c) => (c === e.id ? null : e.id))}
                  className="grid size-tap shrink-0 place-items-center text-fg-3 active:text-danger disabled:opacity-50"
                >
                  <Trash2 size={16} />
                </button>
              </div>
              {confirmId === e.id && (
                <div className="flex flex-wrap items-center gap-2 border-t border-line-1 px-3 py-2 animate-fade-in">
                  <span className="min-w-0 flex-1 text-sm text-fg-1">{t("mAssets.withdraw.removeConfirm")}</span>
                  <span className="flex shrink-0 gap-2">
                    <Button variant="secondary" className="h-tap" disabled={removing === e.id} onClick={() => setConfirmId(null)}>
                      {t("common.cancel")}
                    </Button>
                    <Button variant="danger" className="h-tap" loading={removing === e.id} onClick={() => void remove(e.id)}>
                      {t("mAssets.withdraw.remove")}
                    </Button>
                  </span>
                </div>
              )}
            </div>
          );
        })}
        {shown.length === 0 && <p className="py-6 text-center text-sm text-fg-3">{t("mAssets.withdraw.bookNoMatch")}</p>}
      </div>
    </div>
  );
}
