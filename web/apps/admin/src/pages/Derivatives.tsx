import { dec, errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, ErrorState, Input, Select, Tabs, type DataColumnMeta, type ColumnDef } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";

import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DangerAction } from "../kit/actions";
import { EnumBadge } from "../kit/enums";
import { Num } from "../kit/format";
import { FundAction } from "../kit/funds";
import { Card, Page } from "../kit/Page";
import { ModeBanner } from "./funds/ModeBanner";
import { StatusActions } from "./Instruments";
import { useInstrumentConfig } from "./instruments/config";

type ContractState = AdminSchemas["ContractState"];
type InsuranceFund = AdminSchemas["InsuranceFund"];

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
        items={["contracts", "insurance"].map((k) => ({ value: k, label: t(`admin.derivatives.tabs.${k}`) }))}
        value={tab}
        onValueChange={setTab}
      />
      {tab === "contracts" && <Contracts admin={admin} />}
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
