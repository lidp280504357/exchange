import { dec, errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, ErrorState, Input, Select, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction, errorToast } from "../kit/actions";
import { EnumBadge } from "../kit/enums";
import { Num } from "../kit/format";
import { FundAction } from "../kit/funds";
import { Card, Page } from "../kit/Page";
import { ModeBanner } from "./funds/ModeBanner";
import { StatusActions } from "./Instruments";
import { changesKey, ParamList } from "./instruments/changes";
import { useInstrumentConfig, type ContractConfig } from "./instruments/config";

type ContractState = AdminSchemas["ContractState"];
type InsuranceFund = AdminSchemas["InsuranceFund"];
type CoinPreview = AdminSchemas["CoinStatusPreview"];
type CoinMove = CoinPreview["to"];

const right: DataColumnMeta = { align: "right" };

/**
 * Futures (design §10.3): the contracts' states and the insurance fund.
 * Every user's positions (those near liquidation among them) and the
 * liquidation log have their own pages (2026-10-02 C3).
 */
export default function Derivatives({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [tab, setTab] = useState("contracts");
  return (
    <Page title={t("admin.nav.derivatives")}>
      <Tabs
        items={["contracts", "coins", "insurance"].map((k) => ({ value: k, label: t(`admin.derivatives.tabs.${k}`) }))}
        value={tab}
        onValueChange={setTab}
      />
      {tab === "contracts" && <Contracts admin={admin} />}
      {tab === "coins" && <Coins admin={admin} />}
      {tab === "insurance" && <Insurance admin={admin} />}
    </Page>
  );
}

function Contracts({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["admin", "derivatives", "contracts"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/derivatives/contracts")).contracts,
    refetchInterval: 15_000,
  });
  // The settlement asset is the contract's specification's (G0): USDT until a listing says otherwise.
  const cfg = useInstrumentConfig();
  const settles = useMemo(() => new Map((cfg.data?.contracts ?? []).map((c) => [c.symbol, c.settle_asset || "USDT"])), [cfg.data]);
  const columns = useMemo<ColumnDef<ContractState, unknown>[]>(
    () => [
      { accessorKey: "symbol", header: t("admin.common.symbol") },
      { id: "settle", header: t("admin.coinm.settle"), cell: ({ row }) => <span className="font-mono text-xs">{settles.get(row.original.symbol) ?? "USDT"}</span> },
      { id: "status", header: t("admin.common.status"), cell: ({ row }) => <EnumBadge group="pairStatus" code={row.original.status} /> },
      {
        id: "reduce",
        header: t("admin.derivatives.reduceOnly"),
        cell: ({ row }) =>
          row.original.reduce_only ? (
            <Badge tone="warn" title={row.original.reduce_only_reason}>
              {t("admin.derivatives.reduceOnlyOn")}
            </Badge>
          ) : (
            <span className="text-fg-3">{t("admin.common.no")}</span>
          ),
      },
      {
        id: "mark",
        header: t("admin.derivatives.mark"),
        meta: right,
        cell: ({ row }) => <Num value={row.original.mark_price} className={row.original.mark_fresh ? undefined : "text-warn-strong"} />,
      },
      { id: "oi", header: t("admin.derivatives.oi"), meta: right, cell: ({ row }) => <Num value={row.original.open_interest} /> },
      { accessorKey: "positions", header: t("admin.derivatives.positions"), meta: right },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => (
          <span className="flex items-center justify-end gap-1">
            {row.original.reduce_only && can(admin, "derivatives.write") && <Lift symbol={row.original.symbol} />}
            {can(admin, "instruments.trading") && <StatusActions kind="contract" symbol={row.original.symbol} status={row.original.status} />}
          </span>
        ),
      },
    ],
    [t, admin, settles],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  return <DataTable columns={columns} data={q.data ?? []} getRowId={(c) => c.symbol} loading={q.isPending} density="compact" />;
}

/** A coin's contracts, both margin types, delisted ones aside. */
type Coin = { coin: string; contracts: ContractConfig[] };

/** CLOSES and REOPENS are the statuses a coin's close and reopen take its contracts from (the server's coinMoves). */
const CLOSES: string[] = ["TRADING", "HALT"];
const REOPENS: string[] = ["CANCEL_ONLY"];

/**
 * Coins closes or reopens a coin's contracts at once (coin-margined design
 * 2026-10-06 §3.5, A63): closing puts its USDⓈ-M and COIN-M perpetuals in
 * CANCEL_ONLY (reduce only; HOUSE keeps quoting so that positions close),
 * reopening puts the closed ones back in TRADING. One change for all of
 * them, previewed and confirmed under the guard of a contract's status;
 * delisting stays per contract.
 */
function Coins({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const cfg = useInstrumentConfig();
  const act = can(admin, "instruments.trading");
  const [move, setMove] = useState<CoinPreview | null>(null);
  const coins = useMemo<Coin[]>(() => {
    const by = new Map<string, ContractConfig[]>();
    for (const c of cfg.data?.contracts ?? []) {
      if (c.status !== "DELISTED") by.set(c.base_asset, [...(by.get(c.base_asset) ?? []), c]);
    }
    return [...by.entries()]
      .map(([coin, contracts]) => ({ coin, contracts: contracts.sort((a, b) => a.symbol.localeCompare(b.symbol)) }))
      .sort((a, b) => a.coin.localeCompare(b.coin));
  }, [cfg.data]);
  const preview = async (coin: string, to: CoinMove) => {
    try {
      setMove(adminData(await adminApi.POST("/admin/v1/derivatives/coins/{coin}/status/preview", { params: { path: { coin } }, body: { to } })));
    } catch (err) {
      errorToast(err);
    }
  };
  const columns = useMemo<ColumnDef<Coin, unknown>[]>(
    () => [
      { id: "coin", header: t("admin.coinm.coins.coin"), cell: ({ row }) => <span className="font-mono font-medium">{row.original.coin}</span> },
      {
        id: "contracts", header: t("admin.coinm.coins.contracts"),
        cell: ({ row }) => (
          <span className="flex flex-wrap gap-x-3 gap-y-1">
            {row.original.contracts.map((c) => (
              <span key={c.symbol} className="inline-flex items-center gap-1" data-testid={`coin-contract-${c.symbol}`}>
                <span className="font-mono text-xs">{c.symbol}</span>
                <EnumBadge group="pairStatus" code={c.status} />
              </span>
            ))}
          </span>
        ),
      },
      ...(act
        ? [
            {
              id: "act", header: "", meta: right,
              cell: ({ row: { original: c } }: { row: { original: Coin } }) => (
                <span className="flex justify-end gap-1">
                  <Button
                    size="sm" variant="danger" disabled={!c.contracts.some((x) => CLOSES.includes(x.status))}
                    onClick={() => void preview(c.coin, "CANCEL_ONLY")} data-testid={`close-coin-${c.coin}`}
                  >
                    {t("admin.coinm.coins.close")}
                  </Button>
                  <Button
                    size="sm" variant="secondary" disabled={!c.contracts.some((x) => REOPENS.includes(x.status))}
                    onClick={() => void preview(c.coin, "TRADING")} data-testid={`reopen-coin-${c.coin}`}
                  >
                    {t("admin.coinm.coins.reopen")}
                  </Button>
                </span>
              ),
            },
          ]
        : []),
    ],
    [t, act],
  );
  if (cfg.isError) return <ErrorState message={errorText(cfg.error)} onRetry={() => void cfg.refetch()} />;
  const closing = move?.to === "CANCEL_ONLY";
  return (
    <Card>
      <p className="mb-3 text-xs text-fg-3">{t("admin.coinm.coins.help")}</p>
      <DataTable columns={columns} data={coins} getRowId={(c) => c.coin} loading={cfg.isPending} density="compact" aria-label="coins" />
      {move && (
        <DangerAction
          key={`${move.coin}:${move.to}`}
          open
          onOpenChange={(o) => !o && setMove(null)}
          danger={closing}
          title={t(closing ? "admin.coinm.coins.closeTitle" : "admin.coinm.coins.reopenTitle", { coin: move.coin })}
          description={`${t(closing ? "admin.coinm.coins.closeNote" : "admin.coinm.coins.reopenNote")}${t(
            move.two_person ? "admin.changes.delayedTwoPerson" : "admin.changes.delayed",
            { minutes: Math.max(1, Math.round(move.delay_seconds / 60)) },
          )}`}
          target={<span className="font-mono">{move.coin}</span>}
          confirmWord={move.coin}
          run={async (reason) =>
            adminData(
              await adminApi.POST("/admin/v1/derivatives/coins/{coin}/status", {
                params: { path: { coin: move.coin } }, body: { to: move.to, reason, confirmation: move.confirmation.token },
              }),
            )
          }
          success={move.two_person ? t("admin.changes.pendingApproval") : t("admin.changes.scheduled", { minutes: Math.max(1, Math.round(move.delay_seconds / 60)) })}
          invalidate={[["admin", "instruments"], ["admin", "derivatives"], changesKey, ["admin", "todo"]]}
          onDone={() => setMove(null)}
        >
          <ParamList
            params={move.contracts.map((m) => ({ entity: "CONTRACT", key: m.symbol, field: "status", before: m.from, after: m.to }))}
          />
          {move.staying.length > 0 && (
            <p className="mt-2 text-xs text-fg-3">
              {t("admin.coinm.coins.staying", { list: move.staying.map((m) => `${m.symbol} (${m.from})`).join(", ") })}
            </p>
          )}
        </DangerAction>
      )}
    </Card>
  );
}

function Lift({ symbol }: { symbol: string }) {
  const { t } = useTranslation();
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant="secondary" onClick={open}>
          {t("admin.derivatives.lift")}
        </Button>
      )}
      title={t("admin.derivatives.liftTitle", { symbol })}
      target={<span className="font-mono">{symbol}</span>}
      confirmWord={symbol}
      run={async (reason) => adminData(await adminApi.POST("/admin/v1/derivatives/contracts/{symbol}/lift-reduce-only", { params: { path: { symbol } }, body: { reason } }))}
      success={t("admin.derivatives.lifted")}
      invalidate={[["admin", "derivatives"]]}
    />
  );
}

/**
 * The insurance fund of every settlement asset (design 2026-10-06 §2.7):
 * USDT and each coin-margined contract's coin, contributed to per asset
 * under the same two-person rules.
 */
function Insurance({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [amount, setAmount] = useState("");
  const [asset, setAsset] = useState("USDT");
  const q = useQuery({
    queryKey: ["admin", "derivatives", "insurance"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/derivatives/insurance-funds")).funds,
  });
  const valid = dec.isDecimal(amount) && dec.gt(amount, "0");
  const columns = useMemo<ColumnDef<InsuranceFund, unknown>[]>(
    () => [
      { id: "asset", header: t("admin.coinm.asset"), cell: ({ row }) => <span className="font-mono">{row.original.asset}</span> },
      { id: "fund", header: t("admin.derivatives.fund"), meta: right, cell: ({ row }) => <Num value={row.original.balance} unit={row.original.asset} /> },
      {
        id: "pnl", header: t("admin.derivatives.pnlClearing"), meta: right,
        cell: ({ row }) => <Num value={row.original.pnl_clearing} unit={row.original.asset} signed />,
      },
    ],
    [t],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  return (
    <div className="flex flex-col gap-4">
      <Card title={t("admin.coinm.funds")}>
        <p className="mb-3 text-xs text-fg-3">{t("admin.coinm.fundsHint")}</p>
        <DataTable columns={columns} data={q.data ?? []} getRowId={(f) => f.asset} loading={q.isPending} density="compact" aria-label="insurance funds" />
      </Card>
      {can(admin, "ledger.adjust.request") && (
        <Card title={t("admin.derivatives.contribute")}>
          <ModeBanner className="mb-4" />
          <div className="flex flex-wrap items-end gap-2">
            <Select
              size="sm" value={asset} onValueChange={setAsset} aria-label={t("admin.coinm.asset")}
              options={(q.data ?? [{ asset: "USDT" }]).map((f) => ({ value: f.asset, label: f.asset }))}
            />
            <Input size="sm" value={amount} onValueChange={setAmount} unit={asset} inputMode="decimal" placeholder="100000" containerClassName="w-56" error={amount !== "" && !valid} />
            <FundAction
              trigger={(open) => (
                <Button size="sm" disabled={!valid} onClick={open}>
                  {t("admin.derivatives.contribute")}
                </Button>
              )}
              danger={false}
              title={t("admin.coinm.contributeAsset", { asset })}
              target={<Num value={amount} unit={asset} />}
              confirmWord={amount}
              run={async (reason, key) =>
                adminData(
                  await adminApi.POST("/admin/v1/derivatives/insurance-fund/contributions", {
                    params: { header: { "Idempotency-Key": key } },
                    body: { asset, amount, reason, direct: true },
                  }),
                )
              }
              onDone={() => setAmount("")}
            />
          </div>
        </Card>
      )}
    </div>
  );
}
