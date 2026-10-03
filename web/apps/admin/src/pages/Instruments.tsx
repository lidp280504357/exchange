import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, DropdownMenu, ErrorState, Input, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { ChevronDown, Plus } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import { DangerAction, errorToast } from "../kit/actions";
import { EnumBadge, useEnum } from "../kit/enums";
import { Num } from "../kit/format";
import { RowActions } from "../kit/lists";
import { Page } from "../kit/Page";
import {
  feeRates, useInstrumentConfig, type AssetConfig, type ContractConfig, type FeeSchedule, type InstrumentConfig, type PairConfig,
} from "./instruments/config";
import { AssetDrawer, ContractDrawer, FeeDrawer, PairDrawer } from "./instruments/forms";
import { changesKey, ChangesTab, moveNote, type StatusPreview } from "./instruments/changes";
import { ListingWizard } from "./instruments/Wizard";
import { useTodo } from "../live";

type Status = PairConfig["status"];

/** The status machine of pairs and contracts (appendix B). */
export const NEXT: Record<Status, Status[]> = {
  PREPARE: ["TRADING"],
  TRADING: ["HALT", "CANCEL_ONLY"],
  HALT: ["TRADING", "CANCEL_ONLY"],
  CANCEL_ONLY: ["DELISTED"],
  DELISTED: [],
};

const right: DataColumnMeta = { align: "right" };
const TABS = ["pairs", "assets", "contracts", "fees", "wizard", "changes"] as const;
type Tab = (typeof TABS)[number];

/** What a drawer edits: an item, or a new one (null). */
type Editing =
  | { kind: "pair"; item: PairConfig | null }
  | { kind: "asset"; item: AssetConfig | null }
  | { kind: "contract"; item: ContractConfig | null }
  | { kind: "fee"; item: FeeSchedule | null };

/**
 * Assets, pairs, contracts and fee tiers (design §10.3, 2026-10-02 §4.4):
 * the reference data as a config document. With instruments.write a row
 * opens its editor, new items are added, and pairs come pasted in the
 * listing wizard; every change is previewed field by field and applied
 * with a reason (instrument-service versions it; the deploy's sync keeps
 * what the console changed). A pair or contract moves along its status
 * machine with a confirmation.
 */
export default function Instruments({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const tab: Tab = (TABS as readonly string[]).includes(params.get("tab") ?? "") ? (params.get("tab") as Tab) : "pairs";
  const setTab = (v: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", v);
        return next;
      },
      { replace: true },
    );
  const [q, setQ] = useState("");
  const [editing, setEditing] = useState<Editing | null>(null);
  const data = useInstrumentConfig();
  const cfg = data.data;
  const write = can(admin, "instruments.write");
  const match = (...s: (string | undefined)[]) => !q || s.some((x) => (x ?? "").toUpperCase().includes(q.trim().toUpperCase()));
  const todo = useTodo();
  const add =
    write && tab !== "wizard" && tab !== "changes" ? (
      <Button
        size="sm"
        icon={<Plus size={14} />}
        disabled={!cfg}
        onClick={() => setEditing({ kind: tab === "pairs" ? "pair" : tab === "assets" ? "asset" : tab === "contracts" ? "contract" : "fee", item: null })}
      >
        {t(`admin.listing.add.${tab}`)}
      </Button>
    ) : undefined;
  return (
    <Page title={t("admin.nav.instruments")} help={write ? t("admin.listing.help") : undefined} actions={add}>
      <Tabs
        items={[
          { value: "pairs", label: t("admin.instruments.tabs.pairs"), count: cfg?.pairs.length },
          { value: "assets", label: t("admin.instruments.tabs.assets"), count: cfg?.assets.length },
          { value: "contracts", label: t("admin.instruments.tabs.contracts"), count: cfg?.contracts.length },
          { value: "fees", label: t("admin.listing.tabs.fees"), count: cfg?.fee_schedules.length },
          ...(write ? [{ value: "wizard", label: t("admin.listing.tabs.wizard") }] : []),
          { value: "changes", label: t("admin.changes.tab"), count: todo?.instrument_changes || undefined },
        ]}
        value={tab}
        onValueChange={setTab}
        extra={
          tab !== "wizard" && tab !== "changes" && (
            <Input size="sm" value={q} onValueChange={setQ} placeholder={t("admin.common.search")} clearable onClear={() => setQ("")} containerClassName="w-56" />
          )
        }
      />
      {data.isError ? (
        <ErrorState message={errorText(data.error)} onRetry={() => void data.refetch()} />
      ) : tab === "pairs" ? (
        <Pairs admin={admin} cfg={cfg} rows={(cfg?.pairs ?? []).filter((p) => match(p.symbol, p.reference_symbol))} onOpen={write ? (item) => setEditing({ kind: "pair", item }) : undefined} />
      ) : tab === "assets" ? (
        <Assets rows={(cfg?.assets ?? []).filter((a) => match(a.asset_code, a.name))} loading={!cfg} onOpen={write ? (item) => setEditing({ kind: "asset", item }) : undefined} />
      ) : tab === "contracts" ? (
        <Contracts admin={admin} cfg={cfg} rows={(cfg?.contracts ?? []).filter((c) => match(c.symbol))} onOpen={write ? (item) => setEditing({ kind: "contract", item }) : undefined} />
      ) : tab === "fees" ? (
        <Fees cfg={cfg} rows={(cfg?.fee_schedules ?? []).filter((f) => match(f.tier))} onOpen={write ? (item) => setEditing({ kind: "fee", item }) : undefined} />
      ) : tab === "changes" ? (
        <ChangesTab admin={admin} />
      ) : (
        <ListingWizard cfg={cfg} />
      )}
      {cfg && editing?.kind === "pair" && <PairDrawer key={editing.item?.symbol ?? "new"} cfg={cfg} pair={editing.item} onClose={() => setEditing(null)} />}
      {cfg && editing?.kind === "asset" && <AssetDrawer key={editing.item?.asset_code ?? "new"} cfg={cfg} asset={editing.item} onClose={() => setEditing(null)} />}
      {cfg && editing?.kind === "contract" && (
        <ContractDrawer key={editing.item?.symbol ?? "new"} cfg={cfg} contract={editing.item} onClose={() => setEditing(null)} />
      )}
      {cfg && editing?.kind === "fee" && <FeeDrawer key={editing.item?.tier ?? "new"} cfg={cfg} fee={editing.item} onClose={() => setEditing(null)} />}
    </Page>
  );
}

function Pairs({ admin, cfg, rows, onOpen }: { admin: Admin; cfg: InstrumentConfig | undefined; rows: PairConfig[]; onOpen?: (p: PairConfig) => void }) {
  const { t } = useTranslation();
  const fees = useMemo(() => feeRates(cfg), [cfg]);
  const columns = useMemo<ColumnDef<PairConfig, unknown>[]>(
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
        cell: ({ row }) => {
          const f = fees.get(row.original.fee_tier);
          return (
            <span className="font-mono text-xs" title={row.original.fee_tier}>
              {f ? `${f.maker_fee_rate}/${f.taker_fee_rate}` : row.original.fee_tier}
            </span>
          );
        },
      },
      ...(can(admin, "instruments.trading")
        ? [{ id: "actions", header: "", cell: ({ row }) => <StatusActions kind="pair" symbol={row.original.symbol} status={row.original.status} /> } as ColumnDef<PairConfig, unknown>]
        : []),
    ],
    [t, admin, fees],
  );
  return <DataTable columns={columns} data={rows} getRowId={(p) => p.symbol} loading={!cfg} density="compact" aria-label="pairs" onRowClick={onOpen} />;
}

function Assets({ rows, loading, onOpen }: { rows: AssetConfig[]; loading: boolean; onOpen?: (a: AssetConfig) => void }) {
  const { t } = useTranslation();
  const yes = (v: boolean | undefined) => (v ? <Badge tone="success">{t("admin.common.yes")}</Badge> : <span className="text-fg-3">{t("admin.common.no")}</span>);
  const columns = useMemo<ColumnDef<AssetConfig, unknown>[]>(
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
              <Badge key={n.network} tone={n.provider ? "info" : "neutral"} title={`${n.chain} · min ${n.min_deposit}/${n.min_withdraw} · fee ${n.withdraw_fee}`}>
                {n.network}
              </Badge>
            ))}
          </span>
        ),
      },
    ],
    [t],
  );
  return <DataTable columns={columns} data={rows} getRowId={(a) => a.asset_code} loading={loading} density="compact" aria-label="assets" onRowClick={onOpen} />;
}

function Contracts({
  admin, cfg, rows, onOpen,
}: {
  admin: Admin;
  cfg: InstrumentConfig | undefined;
  rows: ContractConfig[];
  onOpen?: (c: ContractConfig) => void;
}) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<ContractConfig, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="pairStatus" code={row.original.status} /> },
      { accessorKey: "index_symbol", header: t("admin.instruments.index") },
      { id: "tick", header: t("admin.instruments.tick"), meta: right, cell: ({ row }) => <Num value={row.original.tick_size} /> },
      { id: "lot", header: t("admin.instruments.lot"), meta: right, cell: ({ row }) => <Num value={row.original.lot_size} /> },
      { id: "lev", header: t("admin.instruments.maxLeverage"), meta: right, cell: ({ row }) => `${row.original.risk_tiers[0]?.max_leverage ?? "—"}x` },
      { id: "tiers", header: t("admin.listing.riskTiers"), meta: right, cell: ({ row }) => row.original.risk_tiers.length },
      { id: "funding", header: t("admin.instruments.funding"), cell: ({ row }) => t("admin.instruments.hours", { n: row.original.funding_interval_hours }) },
      ...(can(admin, "instruments.trading")
        ? [{ id: "actions", header: "", cell: ({ row }) => <StatusActions kind="contract" symbol={row.original.symbol} status={row.original.status} /> } as ColumnDef<ContractConfig, unknown>]
        : []),
    ],
    [t, admin],
  );
  return <DataTable columns={columns} data={rows} getRowId={(c) => c.symbol} loading={!cfg} density="compact" aria-label="contracts" onRowClick={onOpen} />;
}

function Fees({ cfg, rows, onOpen }: { cfg: InstrumentConfig | undefined; rows: FeeSchedule[]; onOpen?: (f: FeeSchedule) => void }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<FeeSchedule, unknown>[]>(
    () => [
      { accessorKey: "tier", header: t("admin.listing.fields.tier") },
      { id: "maker", header: t("admin.listing.fields.maker_fee_rate"), meta: right, cell: ({ row }) => <Num value={row.original.maker_fee_rate} /> },
      { id: "taker", header: t("admin.listing.fields.taker_fee_rate"), meta: right, cell: ({ row }) => <Num value={row.original.taker_fee_rate} /> },
      {
        id: "used", header: t("admin.listing.usedBy"), meta: right,
        cell: ({ row }) =>
          t("admin.listing.usedByCount", {
            pairs: (cfg?.pairs ?? []).filter((p) => p.fee_tier === row.original.tier).length,
            contracts: (cfg?.contracts ?? []).filter((c) => c.fee_tier === row.original.tier).length,
          }),
      },
    ],
    [t, cfg],
  );
  return <DataTable columns={columns} data={rows} getRowId={(f) => f.tier} loading={!cfg} density="compact" aria-label="fee tiers" onRowClick={onOpen} />;
}

/**
 * haltedBySim reports whether a running HALT event of the simulated market
 * holds symbol (its coin's pair or perpetual): resumed, it is halted again
 * (review ㉔). Without an answer from market-sim, no warning.
 */
async function haltedBySim(symbol: string): Promise<boolean> {
  try {
    const sim = adminData(await adminApi.GET("/admin/v1/sim"));
    return (sim.symbol === symbol || sim.perp === symbol) && sim.events.some((e) => e.type === "HALT" && e.status === "RUNNING");
  } catch {
    return false;
  }
}

/**
 * StatusActions moves a pair or contract to a next status of its machine
 * (ADMIN, design 2026-10-02 §2 item 6): the server's preview first, then
 * the confirmation; a halt takes effect at once, any other move waits.
 */
export function StatusActions({ kind, symbol, status }: { kind: "pair" | "contract"; symbol: string; status: Status | undefined }) {
  const { t } = useTranslation();
  const label = useEnum();
  const [move, setMove] = useState<StatusPreview | null>(null);
  const [simHalt, setSimHalt] = useState(false);
  const next = status ? NEXT[status] : [];
  if (next.length === 0) return null;
  const path = { params: { path: { symbol } } };
  const preview = async (to: Status) => {
    try {
      setSimHalt(status === "HALT" && to === "TRADING" && (await haltedBySim(symbol)));
      setMove(
        kind === "pair"
          ? adminData(await adminApi.POST("/admin/v1/instruments/pairs/{symbol}/status/preview", { ...path, body: { to } }))
          : adminData(await adminApi.POST("/admin/v1/derivatives/contracts/{symbol}/status/preview", { ...path, body: { to } })),
      );
    } catch (err) {
      errorToast(err);
    }
  };
  const run = async (p: StatusPreview, reason: string) => {
    const body = { to: p.to as Status, reason, confirmation: p.confirmation?.token };
    return kind === "pair"
      ? adminData(await adminApi.POST("/admin/v1/instruments/pairs/{symbol}/status", { ...path, body }))
      : adminData(await adminApi.POST("/admin/v1/derivatives/contracts/{symbol}/status", { ...path, body }));
  };
  return (
    <RowActions>
      <DropdownMenu
        trigger={
          <Button size="sm" variant="ghost" className="whitespace-nowrap">
            {t("admin.common.actions")}
            <ChevronDown size={12} className="ml-1 inline-block align-middle" />
          </Button>
        }
        items={next.map((s) => ({ key: s, label: t("admin.instruments.moveTo", { to: label("pairStatus", s) }), danger: s !== "TRADING", onSelect: () => void preview(s) }))}
      />
      {move && (
        <DangerAction
          key={move.to}
          open
          onOpenChange={(o) => !o && setMove(null)}
          danger={move.to !== "TRADING"}
          title={t("admin.instruments.moveTitle", { symbol, to: label("pairStatus", move.to) })}
          description={moveNote(t, move, simHalt)}
          target={<span className="font-mono">{symbol}</span>}
          confirmWord={symbol}
          run={(reason) => run(move, reason)}
          success={
            move.immediate
              ? t("admin.instruments.moved", { symbol, from: label("pairStatus", move.from), to: label("pairStatus", move.to) })
              : move.two_person
                ? t("admin.changes.pendingApproval")
                : t("admin.changes.scheduled", { minutes: Math.max(1, Math.round(move.delay_seconds / 60)) })
          }
          invalidate={[["admin", "instruments"], ["admin", "derivatives"], changesKey, ["admin", "todo"]]}
          onDone={() => setMove(null)}
        />
      )}
    </RowActions>
  );
}
