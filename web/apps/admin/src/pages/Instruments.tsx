import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, DropdownMenu, ErrorState, Input, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { ChevronDown } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../kit/actions";
import { EnumBadge, useEnum } from "../kit/enums";
import { Num } from "../kit/format";
import { Page } from "../kit/Page";

type Asset = AdminSchemas["Asset"];
type Pair = AdminSchemas["Pair"];
type Contract = AdminSchemas["Contract"];
type Status = NonNullable<Pair["status"]>;

/** The status machine of pairs and contracts (appendix B). */
export const NEXT: Record<Status, Status[]> = {
  PREPARE: ["TRADING"],
  TRADING: ["HALT", "CANCEL_ONLY"],
  HALT: ["TRADING", "CANCEL_ONLY"],
  CANCEL_ONLY: ["DELISTED"],
  DELISTED: [],
};

const right: DataColumnMeta = { align: "right" };

/**
 * Assets, pairs and contracts (design §10.3): reference data with the
 * reference mapping; a pair or contract moves along its status machine
 * with a confirmation. Changing the data itself is the reference file's
 * job (exchangectl instruments apply on deploy).
 */
export default function Instruments({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState("pairs");
  const [q, setQ] = useState("");
  const data = useQuery({ queryKey: ["admin", "instruments"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/instruments")) });
  const match = (s: string | undefined) => !q || (s ?? "").toUpperCase().includes(q.trim().toUpperCase());
  return (
    <Page title={t("admin.nav.instruments")}>
      <Tabs
        items={[
          { value: "pairs", label: t("admin.instruments.tabs.pairs"), count: data.data?.pairs.length },
          { value: "assets", label: t("admin.instruments.tabs.assets"), count: data.data?.assets.length },
          { value: "contracts", label: t("admin.instruments.tabs.contracts"), count: data.data?.contracts.length },
        ]}
        value={tab}
        onValueChange={setTab}
        extra={<Input size="sm" value={q} onValueChange={setQ} placeholder={t("admin.common.search")} clearable onClear={() => setQ("")} containerClassName="w-56" />}
      />
      {data.isError ? (
        <ErrorState message={errorText(data.error)} onRetry={() => void data.refetch()} />
      ) : tab === "pairs" ? (
        <Pairs admin={admin} rows={(data.data?.pairs ?? []).filter((p) => match(p.symbol))} loading={data.isPending} />
      ) : tab === "assets" ? (
        <Assets rows={(data.data?.assets ?? []).filter((a) => match(a.asset_code) || match(a.name))} loading={data.isPending} />
      ) : (
        <Contracts admin={admin} rows={(data.data?.contracts ?? []).filter((c) => match(c.symbol))} loading={data.isPending} />
      )}
    </Page>
  );
}

function Pairs({ admin, rows, loading }: { admin: Admin; rows: Pair[]; loading: boolean }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Pair, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="pairStatus" code={row.original.status} /> },
      {
        id: "reference",
        header: t("admin.instruments.reference"),
        cell: ({ row }) =>
          row.original.reference_symbol ? (
            <span className="font-mono text-xs">
              {row.original.reference_symbol}
              {row.original.reference_multiplier && row.original.reference_multiplier !== "1" && ` ×${row.original.reference_multiplier}`}
            </span>
          ) : (
            <span className="text-fg-3">—</span>
          ),
      },
      { id: "tick", header: t("admin.instruments.tick"), meta: right, cell: ({ row }) => <Num value={row.original.tick_size} /> },
      { id: "lot", header: t("admin.instruments.lot"), meta: right, cell: ({ row }) => <Num value={row.original.lot_size} /> },
      { id: "minNotional", header: t("admin.instruments.minNotional"), meta: right, cell: ({ row }) => <Num value={row.original.min_notional} /> },
      { id: "band", header: t("admin.instruments.band"), meta: right, cell: ({ row }) => <Num value={row.original.price_band} /> },
      {
        id: "fees",
        header: t("admin.instruments.fees"),
        cell: ({ row }) => (
          <span className="font-mono text-xs">
            {row.original.maker_fee_rate}/{row.original.taker_fee_rate}
          </span>
        ),
      },
      ...(can(admin, "instruments.write")
        ? [{ id: "actions", header: "", cell: ({ row }) => <StatusActions kind="pair" symbol={row.original.symbol ?? ""} status={row.original.status} /> } as ColumnDef<Pair, unknown>]
        : []),
    ],
    [t, admin],
  );
  return <DataTable columns={columns} data={rows} getRowId={(p) => p.symbol ?? ""} loading={loading} density="compact" aria-label="pairs" />;
}

function Assets({ rows, loading }: { rows: Asset[]; loading: boolean }) {
  const { t } = useTranslation();
  const yes = (v: boolean | undefined) => (v ? <Badge tone="success">{t("admin.common.yes")}</Badge> : <span className="text-fg-3">{t("admin.common.no")}</span>);
  const columns = useMemo<ColumnDef<Asset, unknown>[]>(
    () => [
      { accessorKey: "asset_code", header: t("admin.instruments.code") },
      { accessorKey: "name", header: t("admin.instruments.name") },
      { accessorKey: "decimals", header: t("admin.instruments.decimals"), meta: right },
      { id: "deposit", header: t("admin.instruments.deposit"), cell: ({ row }) => yes(row.original.deposit_enabled) },
      { id: "withdraw", header: t("admin.instruments.withdraw"), cell: ({ row }) => yes(row.original.withdraw_enabled) },
      { id: "trading", header: t("admin.instruments.trading"), cell: ({ row }) => yes(row.original.trading_enabled) },
      {
        id: "networks",
        header: t("admin.instruments.networks"),
        cell: ({ row }) => (
          <span className="flex flex-wrap gap-1">
            {(row.original.networks ?? []).map((n) => (
              <Badge key={n.network} tone="neutral" title={`${n.chain} · min ${n.min_deposit}/${n.min_withdraw} · fee ${n.withdraw_fee}`}>
                {n.network}
              </Badge>
            ))}
          </span>
        ),
      },
    ],
    [t],
  );
  return <DataTable columns={columns} data={rows} getRowId={(a) => a.asset_code ?? ""} loading={loading} density="compact" aria-label="assets" />;
}

function Contracts({ admin, rows, loading }: { admin: Admin; rows: Contract[]; loading: boolean }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Contract, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="pairStatus" code={row.original.status} /> },
      { accessorKey: "index_symbol", header: t("admin.instruments.index") },
      { id: "tick", header: t("admin.instruments.tick"), meta: right, cell: ({ row }) => <Num value={row.original.tick_size} /> },
      { id: "lot", header: t("admin.instruments.lot"), meta: right, cell: ({ row }) => <Num value={row.original.lot_size} /> },
      { id: "lev", header: t("admin.instruments.maxLeverage"), meta: right, cell: ({ row }) => `${row.original.risk_tiers?.[0]?.max_leverage ?? "—"}x` },
      { id: "funding", header: t("admin.instruments.funding"), cell: ({ row }) => t("admin.instruments.hours", { n: row.original.funding_interval_hours }) },
      ...(can(admin, "derivatives.write")
        ? [{ id: "actions", header: "", cell: ({ row }) => <StatusActions kind="contract" symbol={row.original.symbol ?? ""} status={row.original.status} /> } as ColumnDef<Contract, unknown>]
        : []),
    ],
    [t, admin],
  );
  return <DataTable columns={columns} data={rows} getRowId={(c) => c.symbol ?? ""} loading={loading} density="compact" aria-label="contracts" />;
}

/** StatusActions moves a pair or contract to a next status of its machine, with a confirmation. */
export function StatusActions({ kind, symbol, status }: { kind: "pair" | "contract"; symbol: string; status: Status | undefined }) {
  const { t } = useTranslation();
  const label = useEnum();
  const [to, setTo] = useState<Status | null>(null);
  const next = status ? NEXT[status] : [];
  if (next.length === 0) return null;
  const run = async (target: Status, reason: string) =>
    kind === "pair"
      ? adminData(await adminApi.POST("/admin/v1/instruments/pairs/{symbol}/status", { params: { path: { symbol } }, body: { to: target, reason } }))
      : adminData(await adminApi.POST("/admin/v1/derivatives/contracts/{symbol}/status", { params: { path: { symbol } }, body: { to: target, reason } }));
  return (
    <span onClick={(e) => e.stopPropagation()}>
      <DropdownMenu
        trigger={
          <Button size="sm" variant="ghost">
            {t("admin.common.actions")}
            <ChevronDown size={12} />
          </Button>
        }
        items={next.map((s) => ({ key: s, label: t("admin.instruments.moveTo", { to: label("pairStatus", s) }), danger: s !== "TRADING", onSelect: () => setTo(s) }))}
      />
      {to && (
        <DangerAction
          key={to}
          open
          onOpenChange={(o) => !o && setTo(null)}
          danger={to !== "TRADING"}
          title={t("admin.instruments.moveTitle", { symbol, to: label("pairStatus", to) })}
          target={<span className="font-mono">{symbol}</span>}
          confirmWord={symbol}
          run={(reason) => run(to, reason)}
          success={t("admin.instruments.moved", { symbol, from: label("pairStatus", status), to: label("pairStatus", to) })}
          invalidate={[["admin", "instruments"], ["admin", "derivatives"]]}
          onDone={() => setTo(null)}
        />
      )}
    </span>
  );
}
