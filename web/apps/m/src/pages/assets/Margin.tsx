import { dec, enumLabel, errorText, formatAmount, formatPercent, formatPrice, routes } from "@exchange/core";
import { preloadable, useIdleImport } from "@exchange/core/idle";
import type { MarginActionKind, MarginFormInit } from "@exchange/core/margin/form";
import { useMarginAccounts, useMarginAssets, useMarginOpen } from "@exchange/core/margin/hooks";
import { hasDebt, isEmpty, type MarginAccount, type MarginBalance } from "@exchange/core/margin/math";
import { AmountText, Badge, Button, CoinIcon, EmptyState, ErrorState, MarginLevel, cn } from "@exchange/ui";
import { ArrowLeftRight, HandCoins, Undo2 } from "lucide-react";
import { Suspense, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { usePageHeader } from "../../layout/header";
import { Appear, CardSkeleton, PRESS, RETRY, Section, TextButton } from "./parts/bits";
import { shownDecimals, useAssetMeta, type AssetMeta } from "./parts/meta";
import { Notice, reasonText } from "./parts/Notice";

// The transfer, borrow and repay sheet is not on the page's first screen
// (its form: fields, selects, number inputs, B108): it loads once the page
// is idle and opens at once after (B114).
const MarginSheet = preloadable(() => import("./parts/MarginSheet"), (m) => m.MarginSheet);

type Act = (kind: MarginActionKind, init?: MarginFormInit) => void;

const statusTones = { NORMAL: "success", WARNED: "warn", LIQUIDATING: "danger", FROZEN: "neutral" } as const;

/**
 * Margin accounts on the mobile site (margin design 2026-10-06 §7): the
 * cross account and the isolated ones as cards (the margin level gauge,
 * values in USDT, each coin with what is borrowed and owed in interest),
 * the borrowing rates, and the transfer, borrow and repay sheets from the
 * action row. While margin trading is not open to the caller the page
 * says so; repaying and transfers out stay available.
 */
export default function Margin() {
  useIdleImport(MarginSheet.preload);
  const { t } = useTranslation();
  const title = t("nav.margin");
  usePageHeader({ title, back: routes.assets }, [title]);
  const open = useMarginOpen();
  const accounts = useMarginAccounts();
  const meta = useAssetMeta();
  const [sheet, setSheet] = useState<{ kind: MarginActionKind; init: MarginFormInit } | null>(null);
  const act: Act = (kind, init = {}) => setSheet({ kind, init });

  return (
    <div className="flex flex-col gap-3 px-4 pb-6 pt-2">
      {!open.pending && !open.open && (
        <Notice tone="warn">
          <span className="font-medium">{t("mMargin.closed")}</span>
          {" · "}
          {reasonText(open.reason || "USER_NOT_ELIGIBLE")}
          {" · "}
          {t("mMargin.closedHint")}
        </Notice>
      )}
      <div className="grid grid-cols-3 gap-2">
        <ActionTile
          icon={<ArrowLeftRight size={18} />}
          label={t("mMargin.actions.transfer")}
          onClick={() => act("transfer", open.open ? {} : { direction: "OUT" })}
          testId="margin-transfer"
        />
        <ActionTile icon={<HandCoins size={18} />} label={t("mMargin.actions.borrow")} onClick={() => act("borrow")} disabled={!open.open} />
        <ActionTile icon={<Undo2 size={18} />} label={t("mMargin.actions.repay")} onClick={() => act("repay")} />
      </div>
      {accounts.isPending ? (
        <CardSkeleton rows={4} tall />
      ) : accounts.isError ? (
        <div className={RETRY}>
          <ErrorState compact message={errorText(accounts.error)} onRetry={() => void accounts.refetch()} />
        </div>
      ) : (
        <>
          <AccountCard a={accounts.data.cross} title={t("mMargin.cross")} hint={t("mMargin.crossHint")} meta={meta} act={act} open={open.open} />
          <Section
            title={t("mMargin.isolated")}
            extra={
              open.open && <TextButton onClick={() => act("transfer", { account: "MARGIN_ISOLATED" })}>{t("mMargin.actions.openIsolated")}</TextButton>
            }
          >
            {accounts.data.isolated.length === 0 ? (
              <EmptyState compact title={t("mMargin.emptyIsolated")} description={t("mMargin.emptyIsolatedHint")} />
            ) : (
              <ul className="flex flex-col gap-3">
                {accounts.data.isolated.map((a, i) => (
                  <Appear key={a.symbol} index={i}>
                    <AccountCard a={a} title={(a.symbol ?? "").replace("-", "/")} meta={meta} act={act} open={open.open} inner />
                  </Appear>
                ))}
              </ul>
            )}
          </Section>
        </>
      )}
      <Rates />
      {sheet && (
        <Suspense fallback={null}>
          <MarginSheet key={`${sheet.kind}:${JSON.stringify(sheet.init)}`} kind={sheet.kind} init={sheet.init} onClose={() => setSheet(null)} />
        </Suspense>
      )}
    </div>
  );
}

function ActionTile({
  icon, label, onClick, disabled, testId,
}: { icon: ReactNode; label: string; onClick: () => void; disabled?: boolean; testId?: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      data-testid={testId}
      className={cn("flex min-h-16 flex-col items-center justify-center gap-1.5 rounded-3 bg-bg-1 px-1 py-3 text-center disabled:opacity-40", PRESS)}
    >
      <span className="grid size-10 place-items-center rounded-full bg-brand-soft text-brand">{icon}</span>
      <span className="text-xs text-fg-1">{label}</span>
    </button>
  );
}

/** AccountCard is one margin account: its gauge, its values and its coins. */
function AccountCard({
  a, title, hint, meta, act, open, inner,
}: { a: MarginAccount; title: string; hint?: string; meta: AssetMeta; act: Act; open: boolean; inner?: boolean }) {
  const { t } = useTranslation();
  const scope: MarginFormInit = a.account === "MARGIN_ISOLATED" ? { account: a.account, symbol: a.symbol ?? "" } : { account: a.account };
  const stat = (label: string, value: ReactNode) => (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-xs text-fg-3">{label}</span>
      <span className="truncate text-sm font-semibold tabular-nums text-fg-1">{value}</span>
    </div>
  );
  return (
    <section className={cn("flex flex-col gap-4 rounded-3 p-4", inner ? "border border-line-1" : "bg-bg-1")} data-testid={`margin-account-${a.account}`}>
      <div className="flex items-center gap-2">
        <h2 className="text-md font-semibold text-fg-1">{title}</h2>
        <Badge tone="brand" size="sm">
          {t("mMargin.leverage", { n: a.leverage })}
        </Badge>
        {a.status !== "NORMAL" && (
          <Badge tone={statusTones[a.status]} size="sm">
            {enumLabel(a.status)}
          </Badge>
        )}
      </div>
      {hint && <p className="-mt-2 text-xs text-fg-3">{hint}</p>}
      <MarginLevel level={a.margin_level} warn={a.warn_level} liquidation={a.liquidation_level} />
      <div className="grid grid-cols-3 gap-3">
        {stat(t("mMargin.totalAsset"), formatAmount(a.total_asset, 2))}
        {stat(t("mMargin.totalLiability"), formatAmount(a.total_liability, 2))}
        {stat(t("mMargin.netAsset"), formatAmount(a.net_asset, 2))}
      </div>
      {a.account === "MARGIN_ISOLATED" && a.liquidation_price && (
        <p className="text-xs tabular-nums text-fg-3">
          {t("mMargin.liquidationPrice")} {formatPrice(a.liquidation_price)}
        </p>
      )}
      {isEmpty(a) ? (
        a.account === "MARGIN_CROSS" && (
          <EmptyState
            compact
            title={t("mMargin.emptyCross")}
            description={t("mMargin.emptyCrossHint")}
            action={
              open && (
                <Button className="h-tap" onClick={() => act("transfer", scope)}>
                  {t("mMargin.actions.transfer")}
                </Button>
              )
            }
          />
        )
      ) : (
        <ul className="flex flex-col divide-y divide-line-1">
          {a.balances.map((b) => (
            <CoinRow key={b.asset} b={b} meta={meta} act={act} scope={scope} open={open} />
          ))}
        </ul>
      )}
    </section>
  );
}

function CoinRow({ b, meta, act, scope, open }: { b: MarginBalance; meta: AssetMeta; act: Act; scope: MarginFormInit; open: boolean }) {
  const { t } = useTranslation();
  const decimals = shownDecimals(meta.decimals(b.asset));
  const field = (label: string, v: string, className?: string) => (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-xs text-fg-3">{label}</span>
      <AmountText value={v} decimals={decimals} className={cn("truncate text-sm", className)} />
    </div>
  );
  const owes = hasDebt(b);
  return (
    <li className="flex flex-col gap-3 py-3">
      <div className="flex items-center gap-2.5">
        <CoinIcon symbol={b.asset} size={24} />
        <span className="font-medium text-fg-1">{b.asset}</span>
        <span className="ml-auto flex items-center">
          {open && <TextButton onClick={() => act("borrow", { ...scope, asset: b.asset })}>{t("mMargin.actions.borrow")}</TextButton>}
          {owes && <TextButton onClick={() => act("repay", { ...scope, asset: b.asset })}>{t("mMargin.actions.repay")}</TextButton>}
          <TextButton onClick={() => act("transfer", { ...scope, asset: b.asset, direction: open ? "IN" : "OUT" })}>{t("mMargin.actions.transfer")}</TextButton>
        </span>
      </div>
      <div className="grid grid-cols-3 gap-3">
        {field(t("mMargin.columns.free"), b.free)}
        {field(t("mMargin.columns.borrowed"), b.borrowed, dec.sign(b.borrowed) > 0 ? "text-warn" : "text-fg-3")}
        {field(t("mMargin.columns.interest"), b.interest, dec.sign(b.interest) > 0 ? "text-warn" : "text-fg-3")}
        {field(t("mMargin.columns.locked"), b.locked, "text-fg-2")}
        {field(t("mMargin.columns.net"), b.net)}
      </div>
    </li>
  );
}

/** Rates lists what each margin coin costs to borrow by the hour. */
function Rates() {
  const { t } = useTranslation();
  const assets = useMarginAssets();
  const rows = (assets.data ?? []).filter((a) => a.borrowable);
  return (
    <Section title={t("mMargin.rates")}>
      <p className="mb-2 text-xs text-fg-3">{t("mMargin.ratesHint")}</p>
      {assets.isPending ? (
        <CardSkeleton rows={3} />
      ) : (
        <ul className="flex flex-col divide-y divide-line-1">
          {rows.map((a) => (
            <li key={a.asset} className="flex min-h-tap items-center gap-2.5 py-2">
              <CoinIcon symbol={a.asset} size={20} />
              <span className="font-medium text-fg-1">{a.asset}</span>
              <span className="ml-auto flex flex-col items-end">
                <span className="text-sm tabular-nums text-fg-1">{formatPercent(a.hourly_rate, 4, false)}</span>
                <span className="text-xs tabular-nums text-fg-3">
                  {t("mMargin.rateColumns.yearly")} {formatPercent(dec.mul(a.hourly_rate, "8760"), 2, false)}
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}
