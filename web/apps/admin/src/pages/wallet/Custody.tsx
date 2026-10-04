import { errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, Drawer, ErrorState, KeyValue, Segmented, Stat, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import { DangerAction, lastFour } from "../../kit/actions";
import { EnumBadge, useEnum } from "../../kit/enums";
import { FilterBar, options, useFilters } from "../../kit/filters";
import { IdText, Num, TimeText, useTimeText } from "../../kit/format";
import { ListTable, pageSize, useCursorList } from "../../kit/lists";
import { Card, Page } from "../../kit/Page";
import { clean } from "../records/tables";
import { CustodyFees } from "./custodyFees";

type Overview = AdminSchemas["CustodyOverview"];
type Coin = AdminSchemas["CustodyCoin"];
type Check = AdminSchemas["ChainCheck"];
type Callback = AdminSchemas["CustodyCallback"];

const right: DataColumnMeta = { align: "right" };
const RESULTS = ["RECEIVED", "APPLIED", "IGNORED", "UNMATCHED", "REJECTED", "FAILED", "DISCREPANCY"];
const KINDS = ["DEPOSIT", "WITHDRAWAL"];
/** The custodians: UDUN, and its stand-in UDUNMOCK that serves only the hidden test asset (ADR-0017). */
const PROVIDERS = ["UDUN", "UDUNMOCK"] as const;
type Provider = (typeof PROVIDERS)[number];

/** replayable mirrors wallet-service: a verified callback that failed, found nothing or was never finished. */
const replayable = (c: Callback) => c.signature_ok && (c.result === "FAILED" || c.result === "UNMATCHED" || c.result === "RECEIVED");

/**
 * The custody wallet (ADR-0011, design §9), one custodian at a time (C6:
 * UDUN, or the stand-in UDUNMOCK): its coins with what it holds and the
 * networks using them, the latest check of every holder of an asset
 * against the ledger, the withdrawals with the custodian, and the log of
 * its callbacks; a row opens the request as received, and one that failed
 * or found nothing can be replayed. Below, the custodians' withdrawal
 * fees, those held for a person first.
 */
export default function Custody({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const provider: Provider = params.get("provider") === "UDUNMOCK" ? "UDUNMOCK" : "UDUN";
  const choose = (v: string) =>
    setParams(
      (cur) => {
        const next = new URLSearchParams(cur);
        if (v === "UDUN") next.delete("provider");
        else next.set("provider", v);
        return next;
      },
      { replace: true },
    );
  const q = useQuery({
    queryKey: ["admin", "custody", provider],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/custody", { params: { query: { provider } } })),
    refetchInterval: 30_000,
  });
  const o = q.data;
  return (
    <Page
      title={provider === "UDUNMOCK" ? t("admin.custody.titleStandIn") : t("admin.custody.title")}
      help={t("admin.custody.help")}
      actions={
        <Segmented
          aria-label={t("admin.custody.providerSwitch")}
          value={provider}
          onValueChange={choose}
          items={PROVIDERS.map((p) => ({ value: p, label: t(`admin.custody.providers.${p}`) }))}
        />
      }
    >
      {provider === "UDUNMOCK" && <p className="rounded-2 bg-info/10 px-3 py-2 text-sm text-fg-2">{t("admin.custody.standInHint")}</p>}
      {q.isError ? (
        <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : (
        <>
          <Summary o={o} />
          <Card title={t("admin.custody.coins")}>
            <Coins o={o} loading={q.isPending} />
          </Card>
          <Card title={t("admin.custody.checks")}>
            <Checks checks={o?.checks ?? []} loading={q.isPending} />
          </Card>
        </>
      )}
      <Card title={t("admin.custody.callbacks")}>
        <Callbacks admin={admin} provider={provider} />
      </Card>
      <Card title={t("admin.custodyFees.title")}>
        <CustodyFees admin={admin} provider={provider} />
      </Card>
    </Page>
  );
}

/** custodian reports whether a check's holder is a custodian rather than one of the platform's own wallets (a network). */
const custodian = (holder: string) => (PROVIDERS as readonly string[]).includes(holder);

function Summary({ o }: { o: Overview | undefined }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const status = !o ? undefined : !o.configured ? (
    <Badge tone="neutral">{t("admin.custody.notConfigured")}</Badge>
  ) : o.error ? (
    <Badge tone="danger" title={o.error}>
      {t("admin.custody.unreachable")}
    </Badge>
  ) : (
    <Badge tone="success">{t("admin.custody.reachable")}</Badge>
  );
  const box = "rounded-3 border border-line-1 bg-bg-1 p-4";
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <div className={box}>
        <Stat label={t("admin.custody.status")} value={status} loading={!o} hint={o?.error ?? o?.provider} />
      </div>
      <div className={box}>
        <Stat label={t("admin.custody.submitted")} value={o ? <span className="tabular-nums">{o.submitted.count}</span> : undefined} loading={!o} />
        {o && o.submitted.count > 0 && (
          <p className="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-fg-3">
            <span>{t("admin.custody.submittedValue", { value: o.submitted.amount_usdt })}</span>
            {o.submitted.oldest_at && <span>{t("admin.custody.oldest", { time: time(o.submitted.oldest_at) })}</span>}
            <Link className="text-brand-strong hover:underline" to="/withdrawals?status=SUBMITTED">
              {t("admin.custody.viewSubmitted")}
            </Link>
          </p>
        )}
      </div>
      <div className={box}>
        <Stat
          label={t("admin.custody.attention")}
          value={o ? <span className={o.callbacks.attention > 0 ? "text-warn-strong tabular-nums" : "tabular-nums"}>{o.callbacks.attention}</span> : undefined}
          loading={!o}
        />
      </div>
      <div className={box}>
        <Stat
          label={t("admin.custody.lastCallback")}
          value={o ? o.callbacks.last_at ? <TimeText value={o.callbacks.last_at} /> : t("admin.custody.none") : undefined}
          loading={!o}
        />
      </div>
    </div>
  );
}

function Coins({ o, loading }: { o: Overview | undefined; loading: boolean }) {
  const { t } = useTranslation();
  const columns = useMemo<ColumnDef<Coin, unknown>[]>(
    () => [
      {
        id: "symbol",
        header: t("admin.common.asset"),
        cell: ({ row }) => (
          <span className="inline-flex items-center gap-2">
            {row.original.symbol}
            {row.original.token && <Badge tone="info">{t("admin.custody.token")}</Badge>}
          </span>
        ),
      },
      { id: "coin", header: t("admin.custody.coin"), cell: ({ row }) => <IdText value={row.original.coin} chars={18} /> },
      {
        id: "networks",
        header: t("admin.custody.networks"),
        cell: ({ row }) =>
          row.original.networks.length ? (
            <span className="inline-flex flex-wrap gap-1">
              {row.original.networks.map((n) => (
                <Badge key={`${n.asset}/${n.network}`} tone="neutral">
                  {n.asset} · {n.network}
                </Badge>
              ))}
            </span>
          ) : (
            <span className="text-fg-3">{t("admin.custody.unused")}</span>
          ),
      },
      { accessorKey: "decimals", header: t("admin.instruments.decimals"), meta: right },
      { id: "balance", header: t("admin.custody.balance"), meta: right, cell: ({ row }) => <Num value={row.original.balance} /> },
    ],
    [t],
  );
  return (
    <DataTable
      columns={columns}
      data={o?.coins ?? []}
      getRowId={(c) => c.coin}
      loading={loading}
      density="compact"
      empty={<p className="py-4 text-center text-sm text-fg-3">{o?.error ?? t("admin.custody.none")}</p>}
    />
  );
}

function Checks({ checks, loading }: { checks: Check[]; loading: boolean }) {
  const { t } = useTranslation();
  // The stand-in's baseline shows once the real gateway replaced it (㉑ annex).
  const baseline = checks.some((c) => Number(c.baseline ?? 0) !== 0);
  const columns = useMemo<ColumnDef<Check, unknown>[]>(
    () => [
      { accessorKey: "asset", header: t("admin.common.asset") },
      {
        id: "holder",
        header: t("admin.custody.holder"),
        cell: ({ row }) => {
          const h = row.original.holder;
          const text = h === "UDUN" ? t("admin.custody.holderUdun") : custodian(h) ? t("admin.custody.holderCustodian", { name: h }) : `${t("admin.custody.holderSelf")} · ${h}`;
          return <span title={h}>{text}</span>;
        },
      },
      { id: "held", header: t("admin.custody.held"), meta: right, cell: ({ row }) => <Num value={row.original.held} /> },
      { id: "elsewhere", header: t("admin.custody.elsewhere"), meta: right, cell: ({ row }) => <Num value={row.original.elsewhere} /> },
      { id: "inFlight", header: t("admin.custody.inFlight"), meta: right, cell: ({ row }) => <Num value={row.original.in_flight} /> },
      { id: "unbooked", header: t("admin.custody.unbooked"), meta: right, cell: ({ row }) => <Num value={row.original.unbooked} /> },
      { id: "expected", header: t("admin.custody.expected"), meta: right, cell: ({ row }) => <Num value={row.original.expected} /> },
      ...(baseline
        ? [
            {
              id: "baseline",
              header: () => <span title={t("admin.custody.baselineHint")}>{t("admin.custody.baseline")}</span>,
              meta: right,
              cell: ({ row }) => <Num value={row.original.baseline ?? "0"} />,
            } as ColumnDef<Check, unknown>,
          ]
        : []),
      {
        id: "shortfall",
        header: t("admin.custody.shortfall"),
        meta: right,
        cell: ({ row }) => {
          const short = Number(row.original.shortfall) > 0;
          return <Num value={row.original.shortfall} className={short ? "font-medium text-danger-strong" : "text-fg-3"} />;
        },
      },
      { id: "checkedAt", header: t("admin.custody.checkedAt"), cell: ({ row }) => <TimeText value={row.original.checked_at} /> },
    ],
    [t, baseline],
  );
  return (
    <DataTable
      columns={columns}
      data={checks}
      getRowId={(c) => `${c.holder}/${c.asset}`}
      loading={loading}
      density="compact"
      empty={<p className="py-4 text-center text-sm text-fg-3">{t("admin.custody.noChecks")}</p>}
    />
  );
}

type CallbackQuery = { provider: Provider; result?: Callback["result"]; kind?: "DEPOSIT" | "WITHDRAWAL"; q?: string };

function useCallbacks(q: CallbackQuery) {
  return useCursorList<Callback>(["admin", "custody", "callbacks", q], async (cursor) =>
    adminData(await adminApi.GET("/admin/v1/custody/callbacks", { params: { query: { ...clean(q), cursor, limit: pageSize() } } })),
  );
}

function Callbacks({ admin, provider }: { admin: Admin; provider: Provider }) {
  const { t } = useTranslation();
  const label = useEnum();
  const filters = useFilters(["result", "kind", "q"]);
  const f = filters.values;
  const list = useCallbacks({
    provider, result: (f.result || undefined) as CallbackQuery["result"], kind: (f.kind || undefined) as CallbackQuery["kind"], q: f.q?.trim(),
  });
  const [open, setOpen] = useState<Callback | null>(null);
  const columns = useMemo<ColumnDef<Callback, unknown>[]>(
    () => [
      { id: "received", header: t("admin.custody.receivedAt"), cell: ({ row }) => <TimeText value={row.original.received_at} /> },
      { id: "kind", header: t("admin.custody.kind"), cell: ({ row }) => <EnumBadge group="callbackKind" code={row.original.kind || null} /> },
      { id: "trade", header: t("admin.custody.tradeId"), cell: ({ row }) => <IdText value={row.original.trade_id} chars={14} /> },
      {
        id: "status",
        header: t("admin.custody.custodianStatus"),
        cell: ({ row }) => <EnumBadge group="custodyStatus" code={row.original.status == null ? null : String(row.original.status)} />,
      },
      {
        id: "amount",
        header: t("admin.common.amount"),
        meta: right,
        cell: ({ row }) => <Num value={row.original.amount ?? null} />,
      },
      { id: "address", header: t("admin.withdrawals.address"), cell: ({ row }) => <IdText value={row.original.address} chars={10} /> },
      { id: "result", header: t("admin.custody.result"), cell: ({ row }) => <EnumBadge group="callbackResult" code={row.original.result} /> },
      { accessorKey: "attempts", header: t("admin.custody.attempts"), meta: right },
    ],
    [t],
  );
  return (
    <div className="flex flex-col gap-3">
      <FilterBar
        page="custody-callbacks"
        filters={filters}
        defs={[
          { key: "result", label: t("admin.custody.result"), kind: "select", options: options(t("admin.common.all"), RESULTS, (c) => label("callbackResult", c)), width: 140 },
          { key: "kind", label: t("admin.custody.kind"), kind: "select", options: options(t("admin.common.all"), KINDS, (c) => label("callbackKind", c)), width: 120 },
          { key: "q", label: t("admin.custody.query"), kind: "text", placeholder: "tradeId / 0x… / T…" },
        ]}
      />
      <ListTable list={list} columns={columns} getRowId={(c) => c.id} onRowClick={setOpen} aria-label="custody callbacks" />
      {open && <CallbackDrawer admin={admin} id={open.id} onClose={() => setOpen(null)} />}
    </div>
  );
}

function CallbackDrawer({ admin, id, onClose }: { admin: Admin; id: string; onClose: () => void }) {
  const { t } = useTranslation();
  const label = useEnum();
  const q = useQuery({
    queryKey: ["admin", "custody", "callback", id],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/custody/callbacks/{id}", { params: { path: { id } } })),
  });
  const c = q.data;
  return (
    <Drawer
      open
      onOpenChange={(o) => !o && onClose()}
      title={c ? `${label("callbackKind", c.kind || null)} ${c.trade_id}` : t("admin.custody.callbacks")}
      description={<span className="font-mono">{id}</span>}
      actions={c && <EnumBadge group="callbackResult" code={c.result} />}
    >
      {q.isError ? (
        <ErrorState compact message={errorText(q.error)} onRetry={() => void q.refetch()} />
      ) : !c ? null : (
        <div className="flex flex-col gap-5">
          <KeyValue
            items={[
              { label: t("admin.custody.tradeId"), value: <span className="font-mono text-xs">{c.trade_id || "—"}</span>, copy: c.trade_id || undefined },
              { label: t("admin.custody.custodianStatus"), value: <EnumBadge group="custodyStatus" code={c.status == null ? null : String(c.status)} /> },
              ...(c.business_id ? [{ label: t("admin.custody.businessId"), value: <span className="font-mono text-xs">{c.business_id}</span>, copy: c.business_id }] : []),
              { label: t("admin.custody.coin"), value: <span className="font-mono text-xs">{c.coin || "—"}</span> },
              { label: t("admin.common.amount"), value: <Num value={c.amount ?? null} /> },
              ...(c.address ? [{ label: t("admin.withdrawals.address"), value: <span className="font-mono text-xs">{c.address}</span>, copy: c.address }] : []),
              ...(c.tx_hash ? [{ label: t("admin.withdrawals.txHash"), value: <span className="font-mono text-xs">{c.tx_hash}</span>, copy: c.tx_hash }] : []),
              {
                label: t("admin.custody.signature"),
                value: <Badge tone={c.signature_ok ? "success" : "danger"}>{c.signature_ok ? t("admin.custody.signatureOk") : t("admin.custody.signatureBad")}</Badge>,
              },
              { label: t("admin.custody.detail"), value: c.detail || "—" },
              { label: t("admin.custody.attempts"), value: c.attempts },
              {
                label: t("admin.custody.remoteIps"),
                value: c.remote_ips?.length ? <span className="font-mono text-xs">{c.remote_ips.join(", ")}</span> : t("admin.custody.remoteIpsNone"),
              },
              { label: t("admin.custody.receivedAt"), value: <TimeText value={c.received_at} /> },
              { label: t("admin.custody.processedAt"), value: <TimeText value={c.processed_at} /> },
            ]}
          />
          <div className="flex flex-col gap-2">
            <span className="text-sm font-medium text-fg-2">{t("admin.custody.raw")}</span>
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-2 bg-bg-2 p-3 font-mono text-xs text-fg-1">{c.raw}</pre>
          </div>
          {replayable(c) && can(admin, "withdrawals.review") && (
            <DangerAction
              trigger={(openDialog) => (
                <Button variant="secondary" onClick={openDialog}>
                  {t("admin.custody.replay")}
                </Button>
              )}
              danger={false}
              title={t("admin.custody.replayTitle")}
              description={t("admin.custody.replayHint")}
              target={<span className="font-mono text-xs">{c.trade_id}</span>}
              confirmWord={lastFour(c.id)}
              run={async (reason) =>
                adminData(await adminApi.POST("/admin/v1/custody/callbacks/{id}/replay", { params: { path: { id: c.id } }, body: { reason } }))
              }
              success={t("admin.custody.replayed", { result: "" })}
              invalidate={[["admin", "custody"]]}
              onDone={onClose}
            />
          )}
        </div>
      )}
    </Drawer>
  );
}
