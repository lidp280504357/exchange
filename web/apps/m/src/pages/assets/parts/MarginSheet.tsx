import { dec, enumLabel, formatAmount, formatDecimal, formatPercent } from "@exchange/core";
import { useMarginForm, type MarginActionKind, type MarginDone, type MarginFormInit } from "@exchange/core/margin/form";
import { owed } from "@exchange/core/margin/math";
import { Button, CoinIcon, FormField, NumberInput, Segmented, Select, Sheet, cn, mapServerError, toast } from "@exchange/ui";
import { ChevronDown } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { TextButton } from "./bits";
import { CoinSheet } from "./Coins";
import { shownDecimals, useAssetMeta } from "./meta";

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

function useDoneText() {
  const { t } = useTranslation();
  return (d: MarginDone) => {
    const args = { amount: formatDecimal(d.amount), asset: d.asset };
    if (d.kind === "borrow") return t("mMargin.dialog.doneBorrow", args);
    if (d.kind === "repay") return t("mMargin.dialog.doneRepay", args);
    return t(d.direction === "IN" ? "mMargin.dialog.doneIn" : "mMargin.dialog.doneOut", args);
  };
}

/**
 * MarginSheet is the transfer, borrow or repay form of a margin account
 * as a bottom sheet (margin design §7, the mobile shell's rules: 44 px
 * targets, the coin picked in its own sheet, the submit button fixed at
 * the bottom).
 */
export function MarginSheet({ kind, init, onClose }: { kind: MarginActionKind; init: MarginFormInit; onClose: () => void }) {
  const { t } = useTranslation();
  const f = useMarginForm(kind, init);
  const meta = useAssetMeta();
  const done = useDoneText();
  const [picking, setPicking] = useState(false);
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
            ? t("mMargin.dialog.insufficient")
            : undefined;
  const amountError = issueText ?? (serverError?.field === "amount" ? serverError.message : undefined);
  const title = kind === "transfer" ? t("mMargin.dialog.transferTitle") : kind === "borrow" ? t("mMargin.dialog.borrowTitle") : t("mMargin.dialog.repayTitle");
  const submitText =
    kind === "transfer" ? t("mMargin.dialog.submitTransfer") : kind === "borrow" ? t("mMargin.dialog.submitBorrow") : t("mMargin.dialog.submitRepay");
  const maxText =
    kind === "transfer"
      ? t(f.direction === "OUT" ? "mMargin.dialog.maxOut" : f.repayOnly ? "mMargin.dialog.maxInRepay" : "mMargin.dialog.maxIn", {
          amount: formatAmount(f.max, shown),
          asset: f.asset,
        })
      : kind === "borrow"
        ? t("mMargin.dialog.maxBorrow", { amount: formatAmount(f.max, shown), asset: f.asset })
        : t("mMargin.dialog.maxRepay", { amount: formatAmount(f.max, shown), asset: f.asset });

  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={title}
      closeButton
      footer={
        <Button block size="lg" className="h-tap" disabled={!f.ready} loading={f.busy} onClick={() => void submit()}>
          {submitText}
        </Button>
      }
    >
      <form data-testid={`margin-${kind}-form`} onSubmit={(e) => void submit(e)} className="flex flex-col gap-4 pb-2">
        {kind === "transfer" && (
          <Segmented
            block
            size="lg"
            value={f.direction}
            onValueChange={(v) => f.setDirection(v as "IN" | "OUT")}
            aria-label={t("mMargin.dialog.transferTitle")}
            items={[
              // While spot is closed a transfer in only repays: none when the account owes nothing (F20).
              ...(f.inward ? [{ value: "IN", label: t("mMargin.dialog.in") }] : []),
              { value: "OUT", label: t("mMargin.dialog.out") },
            ]}
          />
        )}
        {kind === "transfer" && f.direction === "IN" && f.repayOnly && !f.loading && (
          <p className="-mt-2 text-xs text-fg-3 tabular-nums">{t("mMargin.dialog.repayOnlyIn", { amount: formatAmount(owed(f.row), shown), asset: f.asset })}</p>
        )}
        <FormField label={t("mMargin.dialog.account")}>
          <Segmented
            block
            size="lg"
            value={f.account}
            onValueChange={(v) => f.setAccount(v as "MARGIN_CROSS" | "MARGIN_ISOLATED")}
            items={[
              { value: "MARGIN_CROSS", label: enumLabel("MARGIN_CROSS") },
              { value: "MARGIN_ISOLATED", label: enumLabel("MARGIN_ISOLATED") },
            ]}
          />
        </FormField>
        {f.account === "MARGIN_ISOLATED" && (
          <FormField label={t("mMargin.dialog.pair")}>
            <Select
              size="lg"
              value={f.symbol || undefined}
              onValueChange={f.setSymbol}
              placeholder={t("mMargin.dialog.selectPair")}
              aria-label={t("mMargin.dialog.pair")}
              options={f.pairs.map((p) => ({ value: p.symbol, label: `${p.base}/${p.quote}`, hint: t("mMargin.leverage", { n: p.leverage }) }))}
            />
          </FormField>
        )}
        <FormField label={t("mMargin.dialog.asset")}>
          <button
            type="button"
            disabled={f.account === "MARGIN_ISOLATED" && !f.pair}
            onClick={() => setPicking(true)}
            className="flex min-h-tap w-full items-center gap-3 rounded-2 border border-line-1 bg-bg-2 px-3 text-left disabled:opacity-50"
            aria-label={t("mMargin.dialog.asset")}
          >
            <CoinIcon symbol={f.asset} size={24} />
            <span className="font-medium text-fg-1">{f.asset}</span>
            <span className="min-w-0 flex-1 truncate text-sm text-fg-3">{meta.name(f.asset)}</span>
            <ChevronDown size={16} className="text-fg-3" />
          </button>
        </FormField>
        <FormField
          label={t("mMargin.dialog.amount")}
          error={amountError}
          extra={<TextButton onClick={f.fillMax}>{t("mMargin.dialog.max")}</TextButton>}
          hint={
            <span className="flex flex-col gap-0.5">
              <span className="tabular-nums">
                {maxText}
                {f.limitedBy && dec.sign(f.max) >= 0 && <> · {t("mMargin.dialog.limitedBy", { bound: enumLabel(f.limitedBy) })}</>}
              </span>
              {kind === "transfer" && f.direction === "OUT" && <span>{t("mMargin.dialog.outHint")}</span>}
              {kind === "borrow" && f.term && <span>{t("mMargin.dialog.rate", { rate: formatPercent(f.term.hourly_rate, 4, false) })}</span>}
              {kind === "repay" && (
                <span className="tabular-nums">
                  {t("mMargin.dialog.owed", { amount: formatAmount(owed(f.row), shown), asset: f.asset, interest: formatAmount(f.row.interest, shown) })}
                </span>
              )}
              {kind === "repay" && <span>{f.all ? t("mMargin.dialog.repayAll") : t("mMargin.dialog.repayHint")}</span>}
            </span>
          }
        >
          <NumberInput
            value={f.amount}
            onValueChange={f.setAmount}
            decimals={f.decimals}
            unit={f.asset}
            max={null}
            size="lg"
            aria-label={t("mMargin.dialog.amount")}
          />
        </FormField>
        {serverError && serverError.field !== "amount" && (
          <p role="alert" className={cn("text-sm text-danger")}>
            {serverError.message}
          </p>
        )}
      </form>
      <CoinSheet
        open={picking}
        onOpenChange={setPicking}
        title={t("mMargin.dialog.asset")}
        coins={f.choices.length > 0 ? f.choices : [f.asset]}
        value={f.asset}
        onPick={f.setAsset}
        name={meta.name}
      />
    </Sheet>
  );
}
