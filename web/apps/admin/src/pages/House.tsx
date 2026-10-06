import { dec, errorText } from "@exchange/core";
import { adminApi, adminData, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, cn, DataTable, ErrorState, Input, Segmented, Skeleton, Stat, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Num, TimeText } from "../kit/format";
import { Card, Page } from "../kit/Page";
import { cents, coinMargined, useQuantityUnit } from "../kit/settle";
import { SignedChart } from "../kit/SignedChart";
import { HouseCapsCard } from "./house/caps";
import { useInstrumentConfig } from "./instruments/config";

type House = AdminSchemas["House"];
type Asset = House["assets"][number];
type Pair = House["pairs"][number];
type Position = {
  symbol?: string; position_side?: string; quantity?: string; entry_price?: string; mark_price?: string | null; unrealized_pnl?: string | null;
  margin?: string; leverage?: number; settle_asset?: string; value_usd?: string | null;
};
type ContractState = AdminSchemas["ContractState"];
type PnLBucket = AdminSchemas["HousePnLBucket"];

/** HOUSE's net position on one contract: 0 when it is flat there; amounts in settle_asset, notional in USD. */
type NetPosition = {
  symbol: string; settle_asset: string; quantity: string; notional: string | null; entry: string | null; mark: string | null; upnl: string | null;
};

/**
 * netPositions lists every contract with HOUSE's net position (its positions summed; one-way mode has at most one), the
 * largest first. A coin-margined contract's quantity is whole contracts, its notional their face value in USD.
 */
function netPositions(contracts: readonly ContractState[], positions: readonly Position[], settles: ReadonlyMap<string, string>): NetPosition[] {
  const rows = contracts.map((c) => {
    const own = positions.filter((p) => p.symbol === c.symbol);
    const quantity = own.reduce((sum, p) => dec.add(sum, p.quantity ?? "0"), "0");
    const mark = own[0]?.mark_price ?? c.mark_price ?? null;
    const settle_asset = own.find((p) => p.settle_asset)?.settle_asset ?? settles.get(c.symbol) ?? "USDT";
    const faceValue = () => own.reduce((sum, p) => dec.add(sum, dec.mul(p.value_usd ?? "0", String(dec.sign(p.quantity ?? "0")))), "0");
    return {
      symbol: c.symbol,
      settle_asset,
      quantity,
      notional: coinMargined({ settle_asset }) ? faceValue() : mark ? dec.mul(quantity, mark) : null,
      entry: own.length === 1 ? (own[0]?.entry_price ?? null) : null,
      mark,
      upnl: own.length > 0 ? own.reduce((sum, p) => dec.add(sum, p.unrealized_pnl ?? "0"), "0") : null,
    };
  });
  return rows.sort((a, b) => size(b.notional ?? b.quantity) - size(a.notional ?? a.quantity) || a.symbol.localeCompare(b.symbol));
}

/** size is a decimal's magnitude, for ordering (0 when absent). */
const size = (v: string | null | undefined) => Math.abs(Number(v ?? 0)) || 0;

const right: DataColumnMeta = { align: "right" };
type AssetFilter = "all" | "backed" | "internal";

/**
 * HOUSE (ADR-0013, ADR-0015), redone in C6: its inventory and results at
 * a glance (totals, 30 days of results, where it is exposed), its caps at
 * run time with a change asked of a second administrator (A69), then its
 * inventory valued at the last prices (withdrawable assets, internal ones
 * below zero once sold), what it traded per pair with the result at those
 * prices, and its net position on every contract (0 where it is flat);
 * refreshed every 30 seconds.
 */
export default function HousePage({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useQuery({ queryKey: ["admin", "house"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/house")), refetchInterval: 30_000 });
  // The contracts page's query: every contract, so flat ones show too.
  const contractsQ = useQuery({
    queryKey: ["admin", "derivatives", "contracts"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/derivatives/contracts")).contracts,
    refetchInterval: 30_000,
  });
  // The settlement asset of a contract HOUSE is flat on (its positions name theirs).
  const cfg = useInstrumentConfig();
  const settles = useMemo(() => new Map((cfg.data?.contracts ?? []).map((c) => [c.symbol, c.settle_asset || "USDT"])), [cfg.data]);
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  const h = q.data;
  const contracts = netPositions(contractsQ.data ?? [], (h?.contracts ?? []) as Position[], settles);
  const open = contracts.filter((c) => !dec.isZero(c.quantity)).length;
  return (
    <Page title={t("admin.house.title")} help={t("admin.house.help")}>
      {h && h.partial.length > 0 && <div className="rounded-2 border border-warn px-4 py-2 text-sm text-fg-2">{t("admin.partial", { parts: h.partial.join(", ") })}</div>}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {(
          [
            ["inventory", h?.totals.inventory_usdt, false, h ? t("admin.house.assetsCount", { n: h.assets.length }) : undefined],
            ["backed", h?.totals.backed_usdt, false, undefined],
            ["internal", h?.totals.internal_usdt, true, undefined],
            ["pnl", h?.totals.pnl_usdt, true, h ? t("admin.house.pairsCount", { n: h.pairs.length }) : undefined],
          ] as const
        ).map(([key, value, signed, hint]) => (
          <div key={key} className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Stat label={t(`admin.house.${key}`)} value={h ? <Num value={value} decimals={2} unit="USDT" signed={signed} /> : undefined} loading={!h} />
            {hint && <p className="mt-1 text-xs text-fg-3">{hint}</p>}
          </div>
        ))}
      </div>
      <div className="grid gap-4 xl:grid-cols-5">
        <Card className="xl:col-span-3" title={t("admin.house.results")}>
          <Results />
        </Card>
        <Card className="xl:col-span-2" title={t("admin.house.exposure")}>
          <Exposure assets={h?.assets} />
        </Card>
      </div>
      <HouseCapsCard admin={admin} />
      <Assets assets={h?.assets} loading={q.isPending} />
      <Pairs pairs={h?.pairs} loading={q.isPending} />
      <Card title={t("admin.house.contracts")} extra={contractsQ.data && <span className="text-xs text-fg-3">{t("admin.house.positionsCount", { open, all: contracts.length })}</span>}>
        <Positions rows={contracts} loading={q.isPending || contractsQ.isPending} />
      </Card>
    </Page>
  );
}

/** Results charts HOUSE's results over the last 30 days, day by day (the HOUSE results report). */
function Results() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["admin", "report", "house-pnl", "house-page"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/reports/house-pnl", { params: { query: { days: 30, bucket: "day" } } })),
    refetchInterval: 300_000,
  });
  if (q.isError) return <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  if (!q.data) return <Skeleton className="h-[220px] w-full" />;
  const usdt = (v: number) => `${v >= 0 ? "" : "−"}${Math.abs(Math.round(v * 100) / 100)}`;
  const data = q.data.items.map((r: PnLBucket) => ({
    x: r.day,
    label: r.day.slice(5),
    values: { spot: Number(r.spot_pnl), contracts: Number(r.contracts_pnl), funding: Number(r.funding), cumulative: Number(r.cumulative) },
  }));
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-fg-3">{t("admin.house.resultsHint")}</p>
      {q.data.unpriced.length > 0 && (
        <p className="rounded-2 bg-warn/10 px-3 py-1.5 text-xs text-warn-strong">{t("admin.house.unpriced", { pairs: q.data.unpriced.join(", ") })}</p>
      )}
      <SignedChart
        data={data}
        height={220}
        aria-label={t("admin.house.results")}
        series={[
          { key: "spot", label: `${t("admin.house.spot")} (USDT)`, kind: "bar", color: "chart-1", format: usdt },
          { key: "contracts", label: `${t("admin.house.contractsPnl")} (USDT)`, kind: "bar", color: "chart-2", format: usdt },
          { key: "funding", label: `${t("admin.house.funding")} (USDT)`, kind: "bar", color: "chart-4", format: usdt },
          { key: "cumulative", label: `${t("admin.house.cumulative")} (USDT)`, kind: "line", color: "chart-3", format: usdt },
        ]}
      />
    </div>
  );
}

/** Exposure shows where HOUSE's inventory is: the 10 largest values either way, held to the right, sold below zero to the left. */
function Exposure({ assets }: { assets: Asset[] | undefined }) {
  const { t } = useTranslation();
  if (!assets) return <Skeleton className="h-[220px] w-full" />;
  const rows = assets
    .filter((a) => a.value_usdt != null && !dec.isZero(a.value_usdt))
    .map((a) => ({ asset: a.asset, backed: a.backed, value: a.value_usdt as string }))
    .sort((a, b) => size(b.value) - size(a.value))
    .slice(0, 10);
  const max = Math.max(...rows.map((r) => size(r.value)), 1);
  if (rows.length === 0) return <p className="py-8 text-center text-sm text-fg-3">{t("admin.house.noExposure")}</p>;
  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs text-fg-3">{t("admin.house.exposureHint")}</p>
      <ul className="flex flex-col gap-2" aria-label={t("admin.house.exposure")}>
        {rows.map((r) => {
          const held = dec.sign(r.value) > 0;
          return (
            <li key={r.asset} className="grid grid-cols-[4.5rem_1fr_7rem] items-center gap-3 text-xs">
              <span className="flex items-center gap-1 truncate font-medium" title={r.asset}>
                {r.asset}
                {r.backed && <span className="size-1.5 shrink-0 rounded-full bg-info" title={t("admin.common.backed")} />}
              </span>
              <span className="relative h-2.5 rounded-full bg-bg-2">
                <span className="absolute inset-y-0 left-1/2 w-px bg-line-2" />
                <span
                  className={cn("absolute inset-y-0 rounded-full", held ? "left-1/2 bg-up" : "right-1/2 bg-down")}
                  style={{ width: `${(size(r.value) / max) * 50}%` }}
                />
              </span>
              <Num value={r.value} decimals={2} signed className="text-right" />
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/** Assets lists HOUSE's inventory, the largest value first, filtered by kind and code. */
function Assets({ assets, loading }: { assets: Asset[] | undefined; loading: boolean }) {
  const { t } = useTranslation();
  const [kind, setKind] = useState<AssetFilter>("all");
  const [search, setSearch] = useState("");
  const columns = useMemo<ColumnDef<Asset, unknown>[]>(
    () => [
      {
        accessorKey: "asset",
        header: t("admin.common.asset"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-2">
            {row.original.asset}
            {row.original.backed && <Badge tone="info">{t("admin.common.backed")}</Badge>}
          </span>
        ),
      },
      { id: "balance", header: t("admin.house.balance"), meta: right, cell: ({ row }) => <Num value={row.original.balance} signed={!row.original.backed} /> },
      {
        id: "price",
        header: t("admin.common.price"),
        meta: right,
        cell: ({ row }) => (row.original.price ? <Num value={row.original.price} /> : <span className="text-xs text-fg-3">{t("admin.house.noPrice")}</span>),
      },
      { id: "value", header: t("admin.house.value"), meta: right, cell: ({ row }) => <Num value={row.original.value_usdt} decimals={2} signed={!row.original.backed} /> },
    ],
    [t],
  );
  const needle = search.trim().toUpperCase();
  const rows = (assets ?? [])
    .filter((a) => kind === "all" || (kind === "backed") === a.backed)
    .filter((a) => !needle || a.asset.includes(needle))
    .sort((a, b) => size(b.value_usdt) - size(a.value_usdt) || a.asset.localeCompare(b.asset));
  return (
    <Card
      title={t("admin.house.assets")}
      extra={
        <span className="flex items-center gap-2">
          <Segmented
            size="xs"
            aria-label={t("admin.house.assets")}
            value={kind}
            onValueChange={(v) => setKind(v as AssetFilter)}
            items={(["all", "backed", "internal"] as const).map((k) => ({ value: k, label: t(`admin.house.filter.${k}`) }))}
          />
          <span className="w-36">
            <Input value={search} onValueChange={setSearch} placeholder={t("admin.house.search")} aria-label={t("admin.house.search")} />
          </span>
        </span>
      }
    >
      <DataTable columns={columns} data={rows} getRowId={(a) => a.asset} loading={loading} density="compact" />
    </Card>
  );
}

/** Pairs lists what HOUSE traded per pair, the largest result either way first. */
function Pairs({ pairs, loading }: { pairs: Pair[] | undefined; loading: boolean }) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const columns = useMemo<ColumnDef<Pair, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { accessorKey: "trades", header: t("admin.house.trades"), meta: right },
      { id: "bought", header: t("admin.house.bought"), meta: right, cell: ({ row }) => <Num value={row.original.bought_base} /> },
      { id: "sold", header: t("admin.house.sold"), meta: right, cell: ({ row }) => <Num value={row.original.sold_base} /> },
      { id: "netBase", header: t("admin.house.netBase"), meta: right, cell: ({ row }) => <Num value={row.original.net_base} signed /> },
      { id: "netQuote", header: t("admin.house.netQuote"), meta: right, cell: ({ row }) => <Num value={row.original.net_quote} decimals={2} signed /> },
      { id: "price", header: t("admin.common.price"), meta: right, cell: ({ row }) => <Num value={row.original.price} /> },
      { id: "pnl", header: t("admin.house.pnl"), meta: right, cell: ({ row }) => <Num value={row.original.pnl_usdt} decimals={2} signed /> },
      { id: "last", header: t("admin.house.lastAt"), cell: ({ row }) => <TimeText value={row.original.last_at} /> },
    ],
    [t],
  );
  const needle = search.trim().toUpperCase();
  const rows = (pairs ?? [])
    .filter((p) => !needle || p.symbol.includes(needle))
    .sort((a, b) => size(b.pnl_usdt) - size(a.pnl_usdt) || a.symbol.localeCompare(b.symbol));
  return (
    <Card
      title={t("admin.house.pairs")}
      extra={
        <span className="w-36">
          <Input value={search} onValueChange={setSearch} placeholder={t("admin.house.search")} aria-label={t("admin.house.search")} />
        </span>
      }
    >
      <DataTable columns={columns} data={rows} getRowId={(p) => p.symbol} loading={loading} density="compact" />
    </Card>
  );
}

/** Positions is HOUSE's net position on every contract, flat ones included (the card kept from before C6). */
function Positions({ rows, loading }: { rows: NetPosition[]; loading: boolean }) {
  const { t } = useTranslation();
  const qtyUnit = useQuantityUnit();
  const columns = useMemo<ColumnDef<NetPosition, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      {
        id: "side",
        header: t("admin.house.side"),
        cell: ({ row }) => {
          const s = dec.sign(row.original.quantity);
          return s === 0 ? (
            <span className="text-xs text-fg-3">{t("admin.house.flat")}</span>
          ) : (
            <Badge tone={s > 0 ? "up" : "down"}>{t(s > 0 ? "admin.house.long" : "admin.house.short")}</Badge>
          );
        },
      },
      { id: "qty", header: t("admin.house.net"), meta: right, cell: ({ row }) => <Num value={row.original.quantity} unit={qtyUnit(row.original)} signed /> },
      { id: "notional", header: t("admin.house.notional"), meta: right, cell: ({ row }) => <Num value={row.original.notional} decimals={2} signed /> },
      { id: "entry", header: t("admin.house.entry"), meta: right, cell: ({ row }) => <Num value={row.original.entry} /> },
      { id: "mark", header: t("admin.derivatives.mark"), meta: right, cell: ({ row }) => <Num value={row.original.mark} /> },
      {
        id: "upnl", header: t("admin.house.unrealized"), meta: right,
        cell: ({ row }) => <Num value={row.original.upnl} decimals={cents(row.original)} unit={row.original.settle_asset} signed />,
      },
    ],
    [t, qtyUnit],
  );
  return (
    <DataTable
      columns={columns}
      data={rows}
      getRowId={(p) => p.symbol}
      loading={loading}
      density="compact"
      empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.house.noContracts")}</p>}
    />
  );
}
