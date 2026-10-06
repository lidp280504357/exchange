import { dec } from "@exchange/core";
import type { Admin } from "@exchange/core/api/admin";
import type { ColumnDef, DataColumnMeta } from "@exchange/ui";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { EnumBadge, useEnum } from "../../kit/enums";
import { ALL, FilterBar, options, useFilters } from "../../kit/filters";
import { IdText, Num, TimeText, UserCell } from "../../kit/format";
import { ListTable, useCursorList } from "../../kit/lists";
import { Page } from "../../kit/Page";
import { liquidationsPage, marginKey, type LiquidationQuery, type MarginLiquidation } from "./api";
import { lineText } from "./common";

const right: DataColumnMeta = { align: "right" };
const DAYS = ["1", "7", "30", "90"];

/** Amounts lists amounts of several assets, one a line ("—" for none). */
function Amounts({ items }: { items: MarginLiquidation["repaid"] }) {
  if (items.length === 0) return <span className="text-fg-3">—</span>;
  return (
    <span className="inline-flex flex-col items-end">
      {items.map((a) => (
        <Num key={a.asset} value={a.amount} unit={a.asset} />
      ))}
    </span>
  );
}

/**
 * Margin liquidations (design 2026-10-06 §4.5, §8; A55; a file name of
 * its own, so its chunk is not named as the contracts' page): one row a
 * liquidation, newest first, at the liquidation line or approved by hand:
 * the margin level that started it, the debts it repaid and what stayed
 * in the account, the fee to the insurance fund and the shortfall the
 * fund covered (the read model margin_liquidations, seconds behind). A
 * row seen completed only has no start, and one from before ClickHouse
 * 00010 no trigger: their cells say "—".
 */
export default function MarginLiquidations(_: { admin: Admin }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["account", "symbol", "trigger", "user_id", "days"]);
  const f = filters.values;
  const q: LiquidationQuery = {
    days: DAYS.includes(f.days ?? "") ? Number(f.days) : 30, account: (f.account || undefined) as LiquidationQuery["account"],
    symbol: f.symbol?.toUpperCase() || undefined, trigger: (f.trigger || undefined) as LiquidationQuery["trigger"], user_id: f.user_id || undefined,
  };
  const list = useCursorList<MarginLiquidation>([...marginKey, "liquidations", q], (cursor) => liquidationsPage(q, cursor));
  const columns = useMemo<ColumnDef<MarginLiquidation, unknown>[]>(
    () => [
      { id: "started", header: t("admin.margin.started"), cell: ({ row }) => <TimeText value={row.original.started_at} /> },
      { id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> },
      {
        id: "account", header: t("admin.margin.fields.account"),
        cell: ({ row: { original: l } }) => (
          <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
            <EnumBadge group="marginType" code={l.account} />
            {l.symbol && <span className="font-medium text-fg-1">{l.symbol}</span>}
          </span>
        ),
      },
      {
        id: "trigger", header: t("admin.margin.trigger"),
        cell: ({ row: { original: l } }) => (
          <span className="inline-flex flex-col gap-0.5">
            <EnumBadge group="marginTrigger" code={l.trigger} />
            {l.approval_id && <IdText value={l.approval_id} chars={6} />}
          </span>
        ),
      },
      {
        id: "status", header: t("admin.common.status"),
        cell: ({ row: { original: l } }) => (
          <span className="inline-flex flex-col gap-0.5">
            <EnumBadge group="marginLiquidationStatus" code={l.status} />
            {l.completed_at && (
              <span className="text-xs text-fg-3">
                <TimeText value={l.completed_at} style="timeSeconds" />
              </span>
            )}
          </span>
        ),
      },
      { id: "level", header: t("admin.margin.startLevel"), meta: right, cell: ({ row }) => <span className="font-mono">{lineText(row.original.margin_level)}</span> },
      { id: "liabilities", header: t("admin.margin.fields.liabilities"), meta: right, cell: ({ row }) => <Num value={row.original.total_liability} decimals={2} /> },
      { id: "repaid", header: t("admin.margin.repaid"), meta: right, cell: ({ row }) => <Amounts items={row.original.repaid} /> },
      { id: "fee", header: t("admin.margin.fee"), meta: right, cell: ({ row }) => <Num value={row.original.fee} decimals={2} /> },
      {
        id: "ins", header: t("admin.margin.insuranceCovered"), meta: right,
        cell: ({ row }) => (
          <Num
            value={row.original.insurance_covered}
            decimals={2}
            className={row.original.insurance_covered && dec.gt(row.original.insurance_covered, "0") ? "text-danger-strong" : undefined}
          />
        ),
      },
      { id: "remaining", header: t("admin.margin.remaining"), meta: right, cell: ({ row }) => <Amounts items={row.original.remaining} /> },
    ],
    [t],
  );
  return (
    <Page title={t("admin.nav.marginLiquidations")} help={t("admin.margin.liquidations.help")}>
      <FilterBar
        page="margin-liquidations"
        filters={filters}
        defs={[
          {
            key: "account", label: t("admin.margin.fields.type"), kind: "select", width: 120,
            options: options(t("admin.common.all"), ["MARGIN_CROSS", "MARGIN_ISOLATED"], (c) => label("marginType", c)),
          },
          { key: "symbol", label: t("admin.common.symbol"), kind: "text", placeholder: "BTC-USDT", width: 140 },
          {
            key: "trigger", label: t("admin.margin.trigger"), kind: "select", width: 120,
            options: options(t("admin.common.all"), ["AUTO", "MANUAL"], (c) => label("marginTrigger", c)),
          },
          { key: "user_id", label: t("admin.orders.userFilter"), kind: "text" },
          {
            key: "days", label: t("admin.liquidations.period"), kind: "select", width: 120,
            options: DAYS.map((d) => ({ value: d === "30" ? ALL : d, label: t("admin.reports.lastDays", { n: Number(d) }) })),
          },
        ]}
      />
      <ListTable list={list} columns={columns} getRowId={(l) => l.liquidation_id} aria-label="margin-liquidations" />
    </Page>
  );
}
