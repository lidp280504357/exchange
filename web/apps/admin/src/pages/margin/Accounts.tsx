import { dec, i18n } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import {
  Badge, Button, CopyButton, DataTable, Drawer, ErrorState, KeyValue, Skeleton, Stat, Tabs, type ColumnDef, type DataColumnMeta,
} from "@exchange/ui";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge, EnumText, useEnum } from "../../kit/enums";
import { ALL, FilterBar, options, useFilters } from "../../kit/filters";
import { useFlagCheck } from "../../kit/flags";
import { IdText, Num, TimeText, UserCell } from "../../kit/format";
import { Page } from "../../kit/Page";
import { keyOf, marginKey, useMarginAccount, useMarginAccounts, type AccountQuery, type MarginAccount, type MarginAccountDetail } from "./api";
import { Level, lineText, Rate } from "./common";

const right: DataColumnMeta = { align: "right" };
const STATUSES = ["WARNED", "LIQUIDATING", "FROZEN"];

/**
 * Margin accounts (design 2026-10-06 §8; A55, A56): every cross and
 * isolated account that holds or owes anything, the lowest margin level
 * first, by type, pair or user (asked of the server, one request) and by
 * status (warned, liquidating, frozen: the tabs, on what came back). A row
 * opens the account: its balances and debts, loans, interest and
 * liquidations; frozen or unfrozen at once and liquidated by hand with a
 * second administrator (derivatives.write).
 */
export default function Accounts({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["status", "account", "symbol", "user_id"]);
  const f = filters.values;
  const status = STATUSES.includes(f.status ?? "") ? f.status : undefined;
  const scope = useMarginAccounts({
    account: (f.account || undefined) as AccountQuery["account"], symbol: f.symbol?.toUpperCase() || undefined, user_id: f.user_id || undefined,
  });
  const [open, setOpen] = useState<MarginAccount | null>(null);
  const columns = useMemo<ColumnDef<MarginAccount, unknown>[]>(
    () => [
      { id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> },
      { id: "account", header: t("admin.margin.fields.account"), cell: ({ row }) => <AccountName a={row.original} /> },
      {
        id: "level", header: t("admin.margin.fields.level"), meta: right,
        cell: ({ row: { original: a } }) => (
          <span className="inline-flex flex-col items-end gap-0.5">
            <Level level={a.margin_level} warning={a.warn_level} liquidation={a.liquidation_level} />
            <span className="whitespace-nowrap text-xs text-fg-3">
              {lineText(a.warn_level)} / {lineText(a.liquidation_level)}
            </span>
          </span>
        ),
      },
      { id: "assets", header: t("admin.margin.fields.assets"), meta: right, cell: ({ row }) => <Num value={row.original.total_asset} decimals={2} /> },
      { id: "liabilities", header: t("admin.margin.fields.liabilities"), meta: right, cell: ({ row }) => <Num value={row.original.total_liability} decimals={2} /> },
      { id: "net", header: t("admin.margin.fields.net"), meta: right, cell: ({ row }) => <Num value={row.original.net_asset} decimals={2} signed /> },
      {
        id: "liq", header: t("admin.margin.fields.liquidationPrice"), meta: right,
        cell: ({ row }) => (row.original.liquidation_price ? <Num value={row.original.liquidation_price} className="text-warn-strong" /> : <span className="text-fg-3">—</span>),
      },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <Status a={row.original} /> },
    ],
    [t],
  );
  const rows = scope.data?.items ?? [];
  const shown = status ? rows.filter((a) => a.status === status) : rows;
  const owing = rows.filter((a) => a.margin_level !== null);
  const liabilities = owing.reduce((sum, a) => dec.add(sum, a.total_liability), "0");
  const count = (s: string) => rows.filter((a) => a.status === s).length;
  return (
    <Page title={t("admin.nav.marginAccounts")} help={t("admin.margin.accounts.help")}>
      <div className="grid gap-3 sm:grid-cols-4">
        <Stat size="sm" label={t("admin.margin.accounts.owing")} value={owing.length} loading={scope.isPending} />
        <Stat size="sm" label={t("admin.enum.marginStatus.WARNED")} value={count("WARNED")} loading={scope.isPending} />
        <Stat size="sm" label={t("admin.enum.marginStatus.LIQUIDATING")} value={count("LIQUIDATING")} loading={scope.isPending} />
        <Stat size="sm" label={t("admin.margin.accounts.liabilities")} value={<Num value={liabilities} decimals={2} />} unit="USDT" loading={scope.isPending} />
      </div>
      <Tabs
        items={[{ value: ALL, label: t("admin.common.all") }, ...STATUSES.map((s) => ({ value: s, label: label("marginStatus", s) }))]}
        value={status ?? ALL}
        onValueChange={(v) => filters.set({ status: v })}
        aria-label={t("admin.common.status")}
      />
      <FilterBar
        page="margin-accounts"
        filters={filters}
        defs={[
          {
            key: "account", label: t("admin.margin.fields.type"), kind: "select", width: 120,
            options: options(t("admin.common.all"), ["MARGIN_CROSS", "MARGIN_ISOLATED"], (c) => label("marginType", c)),
          },
          { key: "symbol", label: t("admin.common.symbol"), kind: "text", placeholder: "BTC-USDT", width: 140 },
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
        ]}
      />
      {scope.data?.truncated && <p className="text-sm text-warn-strong">{t("admin.margin.accounts.truncated", { n: rows.length })}</p>}
      {scope.isError ? (
        <ErrorState message={String(scope.error)} onRetry={() => void scope.refetch()} />
      ) : (
        <DataTable
          columns={columns}
          data={shown}
          getRowId={(a) => `${a.user_id} ${keyOf(a)}`}
          loading={scope.isPending}
          onRowClick={setOpen}
          density="compact"
          aria-label="margin-accounts"
          empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.margin.accounts.none")}</p>}
        />
      )}
      {open && <AccountDrawer admin={admin} row={open} onClose={() => setOpen(null)} />}
    </Page>
  );
}

/** AccountName is an account's type, and an isolated account's pair, with its leverage. */
function AccountName({ a }: { a: Pick<MarginAccount, "account" | "symbol" | "leverage"> }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      <EnumBadge group="marginType" code={a.account} />
      {a.symbol && <span className="font-medium text-fg-1">{a.symbol}</span>}
      <span className="text-xs text-fg-3">{a.leverage}x</span>
    </span>
  );
}

/** Status is an account's status, with the assets it holds or owes without a fresh price. */
function Status({ a }: { a: MarginAccount }) {
  const { t } = useTranslation();
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      <EnumBadge group="marginStatus" code={a.status} />
      {a.unpriced.length > 0 && <Badge tone="warn" title={a.unpriced.join(", ")}>{t("admin.margin.unpriced")}</Badge>}
    </span>
  );
}

function AccountDrawer({ admin, row, onClose }: { admin: Admin; row: MarginAccount; onClose: () => void }) {
  const { t } = useTranslation();
  const q = useMarginAccount(row.user_id, keyOf(row));
  const [tab, setTab] = useState("balances");
  const act = can(admin, "derivatives.write");
  const d = q.data;
  // The account as read now once it is (frozen, unfrozen or asked to be liquidated since the list was), the list's row until then.
  const a: MarginAccount = d ?? row;
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={<AccountName a={a} />}
      description={<Status a={a} />}
      width={760}
      actions={act ? <Actions a={a} /> : undefined}
    >
      <div className="flex flex-col gap-4" data-testid="margin-account">
        <KeyValue
          layout="grid"
          columns={3}
          density="compact"
          items={[
            { key: "user", label: t("admin.common.user"), value: <UserCell id={a.user_id} /> },
            { key: "key", label: t("admin.margin.accountKey"), value: <span className="font-mono text-xs">{keyOf(a)}</span> },
            { key: "level", label: t("admin.margin.fields.level"), value: <Level level={a.margin_level} warning={a.warn_level} liquidation={a.liquidation_level} /> },
            { key: "lines", label: t("admin.margin.lines"), value: <span className="font-mono">{lineText(a.warn_level)} / {lineText(a.liquidation_level)}</span> },
            { key: "assets", label: t("admin.margin.fields.assets"), value: <Num value={a.total_asset} decimals={2} unit="USDT" /> },
            { key: "liabilities", label: t("admin.margin.fields.liabilities"), value: <Num value={a.total_liability} decimals={2} unit="USDT" /> },
            { key: "net", label: t("admin.margin.fields.net"), value: <Num value={a.net_asset} decimals={2} unit="USDT" signed /> },
            { key: "liq", label: t("admin.margin.fields.liquidationPrice"), value: <Num value={a.liquidation_price} /> },
            { key: "updated", label: t("admin.margin.valuedAt"), value: <TimeText value={a.updated_at} /> },
          ]}
        />
        {a.frozen_by && (
          <p className="rounded-2 border border-danger bg-danger/10 px-3 py-2 text-sm text-danger-strong">
            {t("admin.margin.frozenBy", { by: a.frozen_by, reason: a.frozen_reason })} <TimeText value={a.frozen_at} />
          </p>
        )}
        {a.pending_approval_id && (
          <p className="text-sm text-warn-strong" data-testid="margin-liquidation-pending">
            {t("admin.margin.liquidationPending")}{" "}
            <Link to="/approvals" className="text-info-strong hover:underline">
              {t("admin.margin.toApprovals")}
            </Link>
          </p>
        )}
        {a.unpriced.length > 0 && <p className="text-sm text-warn-strong">{t("admin.margin.unpricedHint", { assets: a.unpriced.join(", ") })}</p>}
        <Tabs
          items={[
            { value: "balances", label: t("admin.margin.accounts.balances") },
            { value: "loans", label: t("admin.margin.accounts.loans") },
            { value: "interest", label: t("admin.margin.accounts.interest") },
            { value: "liquidations", label: t("admin.nav.marginLiquidations") },
          ]}
          value={tab}
          onValueChange={setTab}
          aria-label={t("admin.margin.accounts.detail")}
        />
        {q.isError ? (
          <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />
        ) : !d ? (
          <Skeleton className="h-40 w-full" />
        ) : (
          <Detail d={d} tab={tab} />
        )}
      </div>
    </Drawer>
  );
}

type Row<K extends keyof MarginAccountDetail> = MarginAccountDetail[K] extends (infer R)[] ? R : never;

function Detail({ d, tab }: { d: MarginAccountDetail; tab: string }) {
  const { t } = useTranslation();
  const balances = useMemo<ColumnDef<Row<"balances">, unknown>[]>(
    () => [
      { accessorKey: "asset", header: t("admin.common.asset"), enableSorting: false },
      { id: "free", header: t("admin.margin.free"), meta: right, cell: ({ row }) => <Num value={row.original.free} /> },
      { id: "locked", header: t("admin.margin.locked"), meta: right, cell: ({ row }) => <Num value={row.original.locked} /> },
      { id: "borrowed", header: t("admin.margin.borrowed"), meta: right, cell: ({ row }) => <Num value={row.original.borrowed} /> },
      { id: "interest", header: t("admin.margin.interest"), meta: right, cell: ({ row }) => <Num value={row.original.interest} /> },
      { id: "net", header: t("admin.margin.net"), meta: right, cell: ({ row }) => <Num value={row.original.net} signed /> },
      { id: "price", header: t("admin.margin.price"), meta: right, cell: ({ row }) => <Num value={row.original.price_usdt} /> },
      {
        id: "worth", header: t("admin.margin.worth"), meta: right,
        cell: ({ row: { original: b } }) => (
          <span className="inline-flex flex-col items-end text-xs">
            <Num value={b.asset_usdt} decimals={2} />
            {b.liability_usdt && dec.gt(b.liability_usdt, "0") && <Num value={dec.neg(b.liability_usdt)} decimals={2} signed />}
          </span>
        ),
      },
      { id: "haircut", header: t("admin.margin.fields.haircut"), meta: right, cell: ({ row }) => <span className="font-mono">{row.original.haircut}</span> },
    ],
    [t],
  );
  const loans = useMemo<ColumnDef<Row<"loans">, unknown>[]>(
    () => [
      { accessorKey: "asset", header: t("admin.common.asset"), enableSorting: false },
      { id: "principal", header: t("admin.margin.principal"), meta: right, cell: ({ row }) => <Num value={row.original.principal} /> },
      { id: "interest", header: t("admin.margin.interest"), meta: right, cell: ({ row }) => <Num value={row.original.interest} /> },
      { id: "model", header: t("admin.margin.form.interest_model"), cell: ({ row }) => <EnumBadge group="rateModel" code={row.original.interest_model} /> },
      { id: "rate", header: t("admin.margin.fields.rate"), meta: right, cell: ({ row }) => <Rate hourly={row.original.hourly_rate} /> },
      { id: "opened", header: t("admin.margin.opened"), cell: ({ row }) => <TimeText value={row.original.opened_at} /> },
    ],
    [t],
  );
  const changes = useMemo<ColumnDef<Row<"loan_changes">, unknown>[]>(
    () => [
      { id: "time", header: t("admin.common.time"), cell: ({ row }) => <TimeText value={row.original.created_at} /> },
      {
        id: "kind", header: t("admin.margin.loanKind"),
        cell: ({ row: { original: c } }) => (
          <span className="inline-flex items-center gap-1">
            <EnumBadge group="loanKind" code={c.kind} />
            {c.status !== "DONE" && <EnumBadge group="loanStatus" code={c.status} />}
          </span>
        ),
      },
      { accessorKey: "asset", header: t("admin.common.asset"), enableSorting: false },
      { id: "amount", header: t("admin.common.amount"), meta: right, cell: ({ row }) => <Num value={row.original.amount} /> },
      { id: "principal", header: t("admin.margin.principal"), meta: right, cell: ({ row }) => <Num value={row.original.principal_part} /> },
      { id: "interest", header: t("admin.margin.interest"), meta: right, cell: ({ row }) => <Num value={row.original.interest_part} /> },
      {
        id: "reason", header: t("admin.margin.reason"),
        cell: ({ row: { original: c } }) => (
          <span className="inline-flex flex-col">
            {c.reason ? <EnumText group="loanReason" code={c.reason} /> : <span className="text-fg-3">—</span>}
            {c.order_id && <IdText value={c.order_id} chars={6} />}
          </span>
        ),
      },
      { id: "journal", header: t("admin.margin.journal"), cell: ({ row }) => <JournalKey value={row.original.journal_key} /> },
    ],
    [t],
  );
  const interest = useMemo<ColumnDef<Row<"interest">, unknown>[]>(
    () => [
      { id: "hour", header: t("admin.margin.hour"), cell: ({ row }) => <TimeText value={row.original.hour} /> },
      { accessorKey: "asset", header: t("admin.common.asset"), enableSorting: false },
      { id: "principal", header: t("admin.margin.principal"), meta: right, cell: ({ row }) => <Num value={row.original.principal} /> },
      { id: "model", header: t("admin.margin.form.interest_model"), cell: ({ row }) => <EnumBadge group="rateModel" code={row.original.interest_model} /> },
      { id: "rate", header: t("admin.margin.fields.rate"), meta: right, cell: ({ row }) => <Rate hourly={row.original.hourly_rate} /> },
      { id: "interest", header: t("admin.margin.interest"), meta: right, cell: ({ row }) => <Num value={row.original.interest} /> },
      {
        id: "journal", header: t("admin.margin.journal"),
        cell: ({ row: { original: c } }) =>
          c.status === "DONE" ? <JournalKey value={c.journal_key} /> : <EnumBadge group="loanStatus" code={c.status} />,
      },
    ],
    [t],
  );
  const liquidations = useMemo<ColumnDef<Row<"liquidations">, unknown>[]>(
    () => [
      { id: "time", header: t("admin.margin.started"), cell: ({ row }) => <TimeText value={row.original.started_at} /> },
      { id: "trigger", header: t("admin.margin.trigger"), cell: ({ row }) => <EnumBadge group="marginTrigger" code={row.original.trigger} /> },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="marginLiquidationStatus" code={row.original.status} /> },
      { id: "level", header: t("admin.margin.startLevel"), meta: right, cell: ({ row }) => <span className="font-mono">{lineText(row.original.margin_level)}</span> },
      { id: "fee", header: t("admin.margin.fee"), meta: right, cell: ({ row }) => <Num value={row.original.fee} decimals={2} /> },
      { id: "ins", header: t("admin.margin.insuranceCovered"), meta: right, cell: ({ row }) => <Num value={row.original.insurance_covered} decimals={2} /> },
    ],
    [t],
  );
  const none = <p className="py-4 text-center text-sm text-fg-3">{t("admin.margin.nothing")}</p>;
  if (tab === "loans") {
    return (
      <div className="flex flex-col gap-3">
        <DataTable columns={loans} data={d.loans} getRowId={(l) => l.asset} density="compact" aria-label="margin-loans" empty={none} />
        <DataTable columns={changes} data={d.loan_changes} getRowId={(c) => c.id} density="compact" aria-label="margin-loan-changes" empty={none} />
      </div>
    );
  }
  if (tab === "interest")
    return <DataTable columns={interest} data={d.interest} getRowId={(i) => i.interest_id} density="compact" aria-label="margin-interest" empty={none} />;
  if (tab === "liquidations")
    return <DataTable columns={liquidations} data={d.liquidations} getRowId={(l) => l.liquidation_id} density="compact" aria-label="margin-account-liquidations" empty={none} />;
  return <DataTable columns={balances} data={d.balances} getRowId={(b) => b.asset} density="compact" aria-label="margin-balances" empty={none} />;
}

/** JournalKey is a ledger journal's idempotency key: its end, the whole of it on hover and a copy button. */
function JournalKey({ value }: { value: string }) {
  const short = value.length > 10 ? `…${value.slice(-8)}` : value;
  return (
    <span className="inline-flex items-center gap-1 font-mono text-xs" title={value}>
      <span>{short}</span>
      <CopyButton value={value} size={12} />
    </span>
  );
}

/** A freeze, an unfreeze or a request reloads the accounts and the approvals that may now list it. */
const changed = [marginKey, ["admin", "approvals"]];

/**
 * Actions are the account's operations: freeze (its open orders canceled)
 * or unfreeze at once, a liquidation by hand with a second administrator,
 * offered while margin-service liquidates (margin.liquidation) and the
 * account owes.
 */
function Actions({ a }: { a: MarginAccount }) {
  const { t } = useTranslation();
  const liquidating = useFlagCheck()("margin.liquidation") === true;
  const target = (
    <span className="inline-flex flex-wrap items-center gap-2">
      <span className="font-mono text-xs">{a.user_id}</span>
      <AccountName a={a} />
    </span>
  );
  // Frozen by an administrator: a liquidation's freeze ends with it, nobody unfreezes that.
  const frozen = a.frozen_by !== null;
  const owes = a.margin_level !== null;
  const path = { user_id: a.user_id, account: keyOf(a) };
  return (
    <span className="flex flex-wrap items-center gap-2">
      <DangerAction
        trigger={(open) => (
          <Button size="sm" variant="secondary" onClick={open} disabled={a.status === "LIQUIDATING"} data-testid="margin-freeze">
            {t(frozen ? "admin.margin.unfreeze" : "admin.margin.freeze")}
          </Button>
        )}
        danger={!frozen}
        title={t(frozen ? "admin.margin.unfreezeTitle" : "admin.margin.freezeTitle")}
        description={t(frozen ? "admin.margin.unfreezeHint" : "admin.margin.freezeHint")}
        target={target}
        confirmWord={lastFour(a.user_id)}
        run={async (reason) =>
          adminData(
            frozen
              ? await adminApi.POST("/admin/v1/margin/accounts/{user_id}/{account}/unfreeze", { params: { path }, body: { reason } })
              : await adminApi.POST("/admin/v1/margin/accounts/{user_id}/{account}/freeze", { params: { path }, body: { reason } }),
          )
        }
        success={() => i18n.t(frozen ? "admin.margin.unfrozen" : "admin.margin.frozen")}
        invalidate={changed}
      />
      <DangerAction
        trigger={(open) => (
          <Button
            size="sm"
            variant="danger"
            onClick={open}
            disabled={!liquidating || !owes || a.status === "LIQUIDATING" || !!a.pending_approval_id}
            title={liquidating ? undefined : t("admin.margin.liquidationOff")}
            data-testid="margin-liquidate"
          >
            {t("admin.margin.liquidate")}
          </Button>
        )}
        title={t("admin.margin.liquidateTitle")}
        description={t("admin.margin.liquidateHint")}
        target={target}
        confirmWord={lastFour(a.user_id)}
        run={async (reason, key) =>
          adminData(
            await adminApi.POST("/admin/v1/margin/accounts/{user_id}/{account}/liquidate", {
              params: { path, header: { "Idempotency-Key": key } }, body: { reason },
            }),
          )
        }
        success={() => i18n.t("admin.margin.liquidationRequested")}
        invalidate={changed}
      />
      {!liquidating && <span className="text-xs text-fg-3">{t("admin.margin.liquidationOff")}</span>}
    </span>
  );
}
