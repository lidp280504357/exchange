import { dec, enumLabel, formatAmount, formatDecimal, formatPercent, useSettings } from "@exchange/core";
import { useMarginForm, type MarginActionKind, type MarginDone, type MarginFormInit } from "@exchange/core/margin/form";
import { owed } from "@exchange/core/margin/math";
import { Combobox, Dialog, FormField, NumberInput, Segmented, Select, coinItems, mapServerError, toast } from "@exchange/ui";
import type { FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { shownDecimals } from "./meta";

const FIELDS = {
  LEDGER_INSUFFICIENT_BALANCE: "amount",
  LEDGER_AMOUNT_PRECISION: "amount",
  COMMON_INVALID_ARGUMENT: "amount",
  MARGIN_LIMIT: "amount",
  MARGIN_POOL_EMPTY: "amount",
  MARGIN_LEVEL_TOO_LOW: "amount",
  MARGIN_REPAY_EXCEEDS_DEBT: "amount",
  MARGIN_AMOUNT_PRECISION: "amount",
};

/** doneText announces what a margin action did. */
export function useDoneText() {
  const { t } = useTranslation();
  return (d: MarginDone) => {
    const args = { amount: formatDecimal(d.amount), asset: d.asset };
    if (d.kind === "borrow") return t("pcMargin.dialog.doneBorrow", args);
    if (d.kind === "repay") return t("pcMargin.dialog.doneRepay", args);
    return t(d.direction === "IN" ? "pcMargin.dialog.doneIn" : "pcMargin.dialog.doneOut", args);
  };
}

/**
 * MarginDialog is the transfer, borrow or repay form of a margin account
 * (margin design §7): the account (cross, or a pair's isolated one), the
 * coin, the amount up to what may move, and the call; refusals show at
 * the amount with their details (MARGIN_LIMIT's max_borrowable, the
 * margin level of MARGIN_LEVEL_TOO_LOW).
 */
export function MarginDialog({ kind, init, onClose }: { kind: MarginActionKind; init: MarginFormInit; onClose: () => void }) {
  const { t } = useTranslation();
  const locale = useSettings((s) => s.locale);
  const f = useMarginForm(kind, init);
  const done = useDoneText();
  const shown = shownDecimals(f.decimals);

  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    const d = await f.submit();
    if (!d) return;
    toast.success(done(d));
    onClose();
  };

  const serverError = f.error ? mapServerError(f.error, FIELDS) : null;
  const issueText =
    f.issue === "format"
      ? t("errors.format")
      : f.issue === "zero"
        ? t("errors.zero")
        : f.issue === "precision"
          ? t("errors.precision", { n: f.decimals })
          : f.issue === "insufficient"
            ? t("pcMargin.dialog.insufficient")
            : undefined;
  const amountError = issueText ?? (serverError?.field === "amount" ? serverError.message : undefined);

  const title = kind === "transfer" ? t("pcMargin.dialog.transferTitle") : kind === "borrow" ? t("pcMargin.dialog.borrowTitle") : t("pcMargin.dialog.repayTitle");
  const submitText =
    kind === "transfer" ? t("pcMargin.dialog.submitTransfer") : kind === "borrow" ? t("pcMargin.dialog.submitBorrow") : t("pcMargin.dialog.submitRepay");
  const maxText =
    kind === "transfer"
      ? t(f.direction === "OUT" ? "pcMargin.dialog.maxOut" : f.repayOnly ? "pcMargin.dialog.maxInRepay" : "pcMargin.dialog.maxIn", {
          amount: formatAmount(f.max, shown),
          asset: f.asset,
        })
      : kind === "borrow"
        ? t("pcMargin.dialog.maxBorrow", { amount: formatAmount(f.max, shown), asset: f.asset })
        : t("pcMargin.dialog.maxRepay", { amount: formatAmount(f.max, shown), asset: f.asset });

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={title}
      size="md"
      persistent={f.amount !== ""}
      onConfirm={() => void submit()}
      confirmText={submitText}
      confirmDisabled={!f.ready}
      confirmLoading={f.busy}
    >
      <form data-testid={`margin-${kind}-form`} onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        {kind === "transfer" && (
          <Segmented
            block
            value={f.direction}
            onValueChange={(v) => f.setDirection(v as "IN" | "OUT")}
            aria-label={t("pcMargin.dialog.transferTitle")}
            items={[
              // While spot is closed a transfer in only repays: none when the account owes nothing (F20).
              ...(f.inward ? [{ value: "IN", label: t("pcMargin.dialog.in") }] : []),
              { value: "OUT", label: t("pcMargin.dialog.out") },
            ]}
          />
        )}
        {kind === "transfer" && f.direction === "IN" && f.repayOnly && !f.loading && (
          <p className="-mt-2 text-xs text-fg-3 tabular-nums">{t("pcMargin.dialog.repayOnlyIn", { amount: formatAmount(owed(f.row), shown), asset: f.asset })}</p>
        )}
        <FormField label={t("pcMargin.dialog.account")}>
          <Segmented
            block
            value={f.account}
            onValueChange={(v) => f.setAccount(v as "MARGIN_CROSS" | "MARGIN_ISOLATED")}
            items={[
              { value: "MARGIN_CROSS", label: enumLabel("MARGIN_CROSS") },
              { value: "MARGIN_ISOLATED", label: enumLabel("MARGIN_ISOLATED") },
            ]}
          />
        </FormField>
        {f.account === "MARGIN_ISOLATED" && (
          <FormField label={t("pcMargin.dialog.pair")} error={f.issue === "account" && f.symbol !== "" ? t("pcMargin.dialog.noPair") : undefined}>
            <Select
              value={f.symbol || undefined}
              onValueChange={f.setSymbol}
              placeholder={t("pcMargin.dialog.selectPair")}
              aria-label={t("pcMargin.dialog.pair")}
              options={f.pairs.map((p) => ({ value: p.symbol, label: `${p.base}/${p.quote}`, hint: t("pcMargin.leverage", { n: p.leverage }) }))}
            />
          </FormField>
        )}
        <FormField label={t("pcMargin.dialog.asset")}>
          <Combobox
            items={coinItems(f.choices.length > 0 ? f.choices : [f.asset], locale)}
            value={f.asset}
            onValueChange={f.setAsset}
            aria-label={t("pcMargin.dialog.asset")}
            disabled={f.account === "MARGIN_ISOLATED" && !f.pair}
          />
        </FormField>
        <FormField
          label={t("pcMargin.dialog.amount")}
          error={amountError}
          hint={
            <span className="flex flex-col gap-0.5">
              <span className="tabular-nums">
                {maxText}
                {f.limitedBy && dec.sign(f.max) >= 0 && <> · {t("pcMargin.dialog.limitedBy", { bound: enumLabel(f.limitedBy) })}</>}
              </span>
              {kind === "transfer" && f.direction === "OUT" && <span>{t("pcMargin.dialog.outHint")}</span>}
              {kind === "borrow" && f.term && (
                <span>{t("pcMargin.dialog.rate", { rate: formatPercent(f.term.hourly_rate, 4, false) })}</span>
              )}
              {kind === "repay" && (
                <span className="tabular-nums">
                  {t("pcMargin.dialog.owed", { amount: formatAmount(owed(f.row), shown), asset: f.asset, interest: formatAmount(f.row.interest, shown) })}
                </span>
              )}
              {kind === "repay" && f.all && <span>{t("pcMargin.dialog.repayAll")}</span>}
              {kind === "repay" && !f.all && <span>{t("pcMargin.dialog.repayHint")}</span>}
            </span>
          }
        >
          <NumberInput
            value={f.amount}
            onValueChange={f.setAmount}
            decimals={f.decimals}
            unit={f.asset}
            max={null}
            aria-label={t("pcMargin.dialog.amount")}
            autoFocus
            suffix={
              <button type="button" className="text-sm font-medium text-brand hover:text-brand-strong" onClick={f.fillMax}>
                {t("pcMargin.dialog.max")}
              </button>
            }
          />
        </FormField>
        {serverError && serverError.field !== "amount" && (
          <p role="alert" className="text-sm text-danger">
            {serverError.message}
          </p>
        )}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
