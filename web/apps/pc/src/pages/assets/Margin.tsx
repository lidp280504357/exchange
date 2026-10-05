import { dec, enumLabel, errorText, formatAmount, formatPercent, formatPrice } from "@exchange/core";
import { useMarginAccounts, useMarginAssets, useMarginOpen } from "@exchange/core/margin/hooks";
import type { MarginActionKind, MarginFormInit } from "@exchange/core/margin/form";
import { hasDebt, isEmpty, type MarginAccount, type MarginAsset, type MarginBalance } from "@exchange/core/margin/math";
import {
  AmountText,
  Badge,
  Button,
  CoinIcon,
  DataTable,
  EmptyState,
  ErrorState,
  MarginLevel,
  Skeleton,
  listItem,
  sortDecimal,
  type ColumnDef,
  type DataColumnMeta,
} from "@exchange/ui";
import { ArrowLeftRight, HandCoins, Plus, Undo2 } from "lucide-react";
import { motion } from "motion/react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AssetsLayout, Card } from "./parts/AssetsLayout";
import { MarginDialog } from "./parts/MarginDialog";
import { shownDecimals, useAssetMeta, type AssetMeta } from "./parts/meta";
import { Notice, reasonText } from "./parts/Notice";

type Act = (kind: MarginActionKind, init?: MarginFormInit) => void;

const statusTones = { NORMAL: "success", WARNED: "warn", LIQUIDATING: "danger", FROZEN: "neutral" } as const;

/**
 * Margin accounts (margin design 2026-10-06 §7): the cross account and the
 * isolated ones, each with its margin level gauge, values in USDT and its
 * coins (available, in orders, borrowed, interest, net), the borrowing
 * rates, and the transfer, borrow and repay dialogs. While margin trading
 * is not open to the caller the page says so; repaying and transfers out
 * stay available. Accounts refresh every few seconds.
 */
export default function Margin() {
  const { t } = useTranslation();
  const open = useMarginOpen();
  const accounts = useMarginAccounts();
  const meta = useAssetMeta();
  const [dialog, setDialog] = useState<{ kind: MarginActionKind; init: MarginFormInit } | null>(null);
  const act: Act = (kind, init = {}) => setDialog({ kind, init });

  return (
    <AssetsLayout
      title={t("pcMargin.title")}
      subtitle={t("pcMargin.subtitle")}
      actions={
        <>
          <Button icon={<ArrowLeftRight size={16} />} onClick={() => act("transfer")} data-testid="margin-transfer">
            {t("pcMargin.actions.transfer")}
          </Button>
          <Button variant="secondary" icon={<HandCoins size={16} />} onClick={() => act("borrow")} disabled={!open.open}>
            {t("pcMargin.actions.borrow")}
          </Button>
          <Button variant="secondary" icon={<Undo2 size={16} />} onClick={() => act("repay")}>
            {t("pcMargin.actions.repay")}
          </Button>
        </>
      }
    >
      {!open.pending && !open.open && (
        <Notice tone="warn" role="alert" className="mb-4">
          <span className="font-medium">{t("pcMargin.closed")}</span>
          {" · "}
          {reasonText(open.reason || "USER_NOT_ELIGIBLE")}
          {" · "}
          {t("pcMargin.closedHint")}
        </Notice>
      )}
      <div className="grid grid-cols-12 gap-4">
        <motion.div variants={listItem} initial="initial" animate="animate" custom={0} className="col-span-12 2xl:col-span-8">
          <Card title={<AccountTitle label={t("pcMargin.cross")} a={accounts.data?.cross} />} extra={<span className="text-xs text-fg-3">{t("pcMargin.crossHint")}</span>}>
            {accounts.isPending ? (
              <AccountSkeleton />
            ) : accounts.isError ? (
              <ErrorState message={errorText(accounts.error)} onRetry={() => void accounts.refetch()} />
            ) : (
              <AccountBody a={accounts.data.cross} meta={meta} act={act} open={open.open} />
            )}
          </Card>
        </motion.div>
        <motion.div variants={listItem} initial="initial" animate="animate" custom={1} className="col-span-12 2xl:col-span-4">
          <RatesCard meta={meta} />
        </motion.div>
        <motion.div variants={listItem} initial="initial" animate="animate" custom={2} className="col-span-12">
          <Card
            title={t("pcMargin.isolated")}
            extra={
              <Button size="sm" variant="secondary" icon={<Plus size={14} />} disabled={!open.open} onClick={() => act("transfer", { account: "MARGIN_ISOLATED" })}>
                {t("pcMargin.actions.openIsolated")}
              </Button>
            }
            bodyClassName="flex flex-col gap-4 p-5"
          >
            <p className="text-xs text-fg-3">{t("pcMargin.isolatedHint")}</p>
            {accounts.isPending ? (
              <AccountSkeleton />
            ) : accounts.isError ? null : accounts.data.isolated.length === 0 ? (
              <EmptyState compact title={t("pcMargin.emptyIsolated")} description={t("pcMargin.emptyIsolatedHint")} />
            ) : (
              accounts.data.isolated.map((a) => (
                <section key={a.symbol} className="rounded-3 border border-line-1" data-testid={`margin-isolated-${a.symbol}`}>
                  <div className="flex min-h-11 items-center justify-between gap-3 border-b border-line-1 px-4 py-2">
                    <AccountTitle label={(a.symbol ?? "").replace("-", "/")} a={a} />
                  </div>
                  <div className="p-4">
                    <AccountBody a={a} meta={meta} act={act} open={open.open} />
                  </div>
                </section>
              ))
            )}
          </Card>
        </motion.div>
      </div>
      {dialog && <MarginDialog key={`${dialog.kind}:${JSON.stringify(dialog.init)}`} kind={dialog.kind} init={dialog.init} onClose={() => setDialog(null)} />}
    </AssetsLayout>
  );
}

function AccountTitle({ label, a }: { label: ReactNode; a: MarginAccount | undefined }) {
  const { t } = useTranslation();
  return (
    <span className="flex items-center gap-2">
      <span className="text-md font-semibold text-fg-1">{label}</span>
      {a && (
        <>
          <Badge tone="brand" size="sm">
            {t("pcMargin.leverage", { n: a.leverage })}
          </Badge>
          {a.status !== "NORMAL" && (
            <Badge tone={statusTones[a.status]} size="sm">
              {enumLabel(a.status)}
            </Badge>
          )}
        </>
      )}
    </span>
  );
}

function AccountSkeleton() {
  return (
    <div className="flex flex-col gap-3">
      <Skeleton className="h-16 w-full" />
      <Skeleton className="h-32 w-full" />
    </div>
  );
}

/** AccountBody is one account: its gauge and values, then its coins. */
function AccountBody({ a, meta, act, open }: { a: MarginAccount; meta: AssetMeta; act: Act; open: boolean }) {
  const { t } = useTranslation();
  const scope: MarginFormInit = a.account === "MARGIN_ISOLATED" ? { account: a.account, symbol: a.symbol ?? "" } : { account: a.account };
  const stat = (label: string, value: ReactNode) => (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-xs text-fg-3">{label}</span>
      <span className="truncate text-md font-semibold tabular-nums text-fg-1">{value}</span>
    </div>
  );
  return (
    <div className="flex flex-col gap-5" data-testid={`margin-account-${a.account}`}>
      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(220px,1fr)_2fr]">
        <MarginLevel level={a.margin_level} warn={a.warn_level} liquidation={a.liquidation_level} />
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          {stat(t("pcMargin.totalAsset"), formatAmount(a.total_asset, 2))}
          {stat(t("pcMargin.totalLiability"), formatAmount(a.total_liability, 2))}
          {stat(t("pcMargin.netAsset"), formatAmount(a.net_asset, 2))}
          {a.account === "MARGIN_ISOLATED" && stat(t("pcMargin.liquidationPrice"), a.liquidation_price ? formatPrice(a.liquidation_price) : "—")}
        </div>
      </div>
      {isEmpty(a) ? (
        a.account === "MARGIN_CROSS" && (
          <EmptyState
            compact
            title={t("pcMargin.emptyCross")}
            description={t("pcMargin.emptyCrossHint")}
            action={
              open && (
                <Button size="sm" icon={<ArrowLeftRight size={14} />} onClick={() => act("transfer", scope)}>
                  {t("pcMargin.actions.transfer")}
                </Button>
              )
            }
          />
        )
      ) : (
        <BalancesTable a={a} meta={meta} act={act} scope={scope} open={open} />
      )}
    </div>
  );
}

function BalancesTable({ a, meta, act, scope, open }: { a: MarginAccount; meta: AssetMeta; act: Act; scope: MarginFormInit; open: boolean }) {
  const { t } = useTranslation();
  const amount = (b: MarginBalance, v: string, className?: string) => (
    <AmountText value={v} decimals={shownDecimals(meta.decimals(b.asset))} className={className} />
  );
  const columns = useMemo<ColumnDef<MarginBalance, any>[]>(
    () => [
      {
        id: "asset",
        accessorKey: "asset",
        header: t("pcMargin.columns.asset"),
        cell: ({ row }) => (
          <span className="flex items-center gap-2.5">
            <CoinIcon symbol={row.original.asset} size={24} />
            <span className="font-medium text-fg-1">{row.original.asset}</span>
          </span>
        ),
        meta: { width: 140 } satisfies DataColumnMeta,
      },
      ...(["free", "locked", "borrowed", "interest", "net"] as const).map(
        (key): ColumnDef<MarginBalance, any> => ({
          id: key,
          accessorKey: key,
          header: t(`pcMargin.columns.${key}`),
          sortingFn: sortDecimal,
          cell: ({ row }) =>
            amount(
              row.original,
              row.original[key],
              key === "borrowed" || key === "interest" ? (dec.sign(row.original[key]) > 0 ? "text-warn" : "text-fg-3") : key === "locked" ? "text-fg-2" : undefined,
            ),
          meta: { align: "right" } satisfies DataColumnMeta,
        }),
      ),
      {
        id: "actions",
        header: t("common.action"),
        enableSorting: false,
        cell: ({ row }) => (
          <span className="flex justify-end gap-1">
            <Button size="sm" variant="ghost" disabled={!open} onClick={() => act("borrow", { ...scope, asset: row.original.asset })}>
              {t("pcMargin.actions.borrow")}
            </Button>
            <Button size="sm" variant="ghost" disabled={!hasDebt(row.original)} onClick={() => act("repay", { ...scope, asset: row.original.asset })}>
              {t("pcMargin.actions.repay")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => act("transfer", { ...scope, asset: row.original.asset, direction: open ? "IN" : "OUT" })}>
              {t("pcMargin.actions.transfer")}
            </Button>
          </span>
        ),
        meta: { align: "right", width: 200 } satisfies DataColumnMeta,
      },
    ],
    [t, meta, act, scope, open],
  );
  return <DataTable columns={columns} data={a.balances} getRowId={(b) => b.asset} density="compact" />;
}

/** RatesCard lists what each margin coin costs to borrow and what the platform has left to lend. */
function RatesCard({ meta }: { meta: AssetMeta }) {
  const { t } = useTranslation();
  const assets = useMarginAssets();
  const columns = useMemo<ColumnDef<MarginAsset, any>[]>(
    () => [
      {
        id: "asset",
        accessorKey: "asset",
        header: t("pcMargin.rateColumns.asset"),
        cell: ({ row }) => (
          <span className="flex items-center gap-2">
            <CoinIcon symbol={row.original.asset} size={20} />
            <span className="font-medium text-fg-1">{row.original.asset}</span>
          </span>
        ),
      },
      {
        id: "hourly",
        accessorKey: "hourly_rate",
        header: t("pcMargin.rateColumns.hourly"),
        sortingFn: sortDecimal,
        cell: ({ row }) => <span className="tabular-nums">{formatPercent(row.original.hourly_rate, 4, false)}</span>,
        meta: { align: "right" } satisfies DataColumnMeta,
      },
      {
        id: "yearly",
        header: t("pcMargin.rateColumns.yearly"),
        enableSorting: false,
        cell: ({ row }) => <span className="tabular-nums text-fg-2">{formatPercent(dec.mul(row.original.hourly_rate, "8760"), 2, false)}</span>,
        meta: { align: "right" } satisfies DataColumnMeta,
      },
      {
        id: "available",
        accessorKey: "pool_available",
        header: t("pcMargin.rateColumns.available"),
        sortingFn: sortDecimal,
        cell: ({ row }) => <AmountText value={row.original.pool_available} decimals={Math.min(shownDecimals(meta.decimals(row.original.asset)), 4)} />,
        meta: { align: "right" } satisfies DataColumnMeta,
      },
    ],
    [t, meta],
  );
  return (
    <Card title={t("pcMargin.rates")} extra={<span className="text-xs text-fg-3">{t("pcMargin.ratesHint")}</span>} bodyClassName="p-0">
      <DataTable
        columns={columns}
        data={(assets.data ?? []).filter((a) => a.borrowable)}
        getRowId={(a) => a.asset}
        loading={assets.isPending}
        error={assets.error}
        onRetry={() => void assets.refetch()}
        density="compact"
      />
    </Card>
  );
}
