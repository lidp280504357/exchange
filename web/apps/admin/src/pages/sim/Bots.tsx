import { dec, errorText } from "@exchange/core";
import { adminApi, adminData, can, type Admin, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, DataTable, ErrorState, Input, Segmented, Skeleton, Switch, type ColumnDef, type DataColumnMeta } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction, FormError } from "../../kit/actions";
import { EnumBadge } from "../../kit/enums";
import { Num, TimeText, UserCell } from "../../kit/format";
import { FundAction, type Approval } from "../../kit/funds";
import { stagger } from "../../kit/motion";
import { Card, Page } from "../../kit/Page";
import { MintShares, pct, useSim, type SimBot, type SimStatus } from "./common";

// The bots (ASTRA design §6.1): the cluster's switches (the flags
// sim.enabled, sim.perp, sim.events, sim.halt_on_loss), each bot's
// balances and last refusal, and more of the coin or USDT for them as a
// fund operation (user decision 2026-10-03: no transfers between bots;
// a single bot's balance is adjusted like any user's).

type Flag = AdminSchemas["Flag"];

const ROLES = ["MAKER", "TAKER", "TREND", "EXECUTOR"] as const;
const SIM_FLAGS = ["sim.enabled", "sim.perp", "sim.events", "sim.halt_on_loss"];
const right: DataColumnMeta = { align: "right" };

export default function SimBots({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useSim(5_000);
  const [role, setRole] = useState("all");
  const [errorsOnly, setErrorsOnly] = useState(false);
  const st = q.data;
  const coin = st?.symbol.split("-")[0] ?? "";
  const bots = st?.bots ?? [];
  const shown = bots.filter((b) => (role === "all" || b.role === role) && (!errorsOnly || b.error));
  const adjust = can(admin, "ledger.adjust.request");
  const columns = useMemo<ColumnDef<SimBot, unknown>[]>(
    () => [
      {
        id: "bot", header: t("admin.sim.botLabel"),
        cell: ({ row: { original: b } }) => (
          <span className="inline-flex items-center gap-2">
            <span className="font-mono text-sm text-fg-1">{b.label}</span>
            <Badge tone={b.role === "MAKER" ? "brand" : b.role === "EXECUTOR" ? "warn" : "neutral"}>{t(`admin.sim.roles.${b.role}`)}</Badge>
          </span>
        ),
      },
      { id: "user", header: t("admin.common.user"), cell: ({ row }) => <UserCell id={row.original.user_id} /> },
      { id: "usdt", header: t("admin.sim.spotOf", { asset: "USDT" }), meta: right, cell: ({ row }) => <Num value={row.original.usdt} decimals={2} /> },
      { id: "coin", header: t("admin.sim.spotOf", { asset: coin }), meta: right, cell: ({ row }) => <Num value={row.original.coin} decimals={0} /> },
      ...(st?.perp
        ? ([
            { id: "perp", header: t("admin.sim.perpOf"), meta: right, cell: ({ row }) => <Num value={row.original.perp_position} signed /> },
            { id: "margin", header: t("admin.sim.futuresUsdt"), meta: right, cell: ({ row }) => <Num value={row.original.futures_usdt} decimals={2} /> },
          ] as ColumnDef<SimBot, unknown>[])
        : []),
      { id: "state", header: t("admin.common.status"), cell: ({ row }) => <BotState b={row.original} /> },
      ...(adjust
        ? ([
            {
              id: "actions", header: "", meta: right,
              cell: ({ row }) => (
                <Link to={`/adjustments?uid=${row.original.user_id}`} className="whitespace-nowrap text-sm text-info hover:underline">
                  {t("admin.sim.adjustBot")}
                </Link>
              ),
            },
          ] as ColumnDef<SimBot, unknown>[])
        : []),
    ],
    [t, coin, st?.perp, adjust],
  );
  if (q.isError) return <ErrorState message={errorText(q.error)} onRetry={() => void q.refetch()} />;
  return (
    <Page title={t("admin.nav.simBots")} help={t("admin.sim.botsHelp")}>
      <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
        <Card title={t("admin.sim.switches")} className="stagger">
          <Switches admin={admin} />
        </Card>
        <Card title={t("admin.sim.roster")} className="stagger" style={stagger(1)}>
          {st ? <Roster st={st} coin={coin} /> : <Skeleton className="h-28 w-full" />}
        </Card>
      </div>
      <Card
        title={t("admin.sim.botList")}
        className="stagger"
        style={stagger(2)}
        extra={
          <span className="flex flex-wrap items-center gap-3">
            <Segmented
              size="sm"
              value={role}
              onValueChange={setRole}
              items={[{ value: "all", label: t("admin.common.all") }, ...ROLES.map((r) => ({ value: r, label: t(`admin.sim.roles.${r}`) }))]}
            />
            <Switch size="sm" checked={errorsOnly} onCheckedChange={setErrorsOnly} label={t("admin.sim.errorsOnly")} />
          </span>
        }
      >
        <DataTable
          columns={columns}
          data={shown}
          getRowId={(b) => b.user_id}
          loading={q.isPending}
          empty={t("admin.sim.noBots")}
          density="compact"
          aria-label={t("admin.sim.botList")}
        />
      </Card>
      <div className="grid gap-3 xl:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
        {adjust && st && (
          <Card title={t("admin.sim.mint")} className="stagger" style={stagger(3)}>
            <MintForm st={st} coin={coin} />
          </Card>
        )}
        {st && (
          <Card
            title={t("admin.sim.clusterSettings")}
            className="stagger"
            style={stagger(4)}
            extra={
              <Link to="/sim/control" className="text-xs text-info hover:underline">
                {t("admin.sim.editInControl")}
              </Link>
            }
          >
            <ClusterSettings params={st.params} />
          </Card>
        )}
      </div>
    </Page>
  );
}

/** BotState is whether a bot trades: off, refused (until when), balances unknown, or fine. */
function BotState({ b }: { b: SimBot }) {
  const { t } = useTranslation();
  if (!b.enabled) return <Badge tone="neutral">{t("admin.sim.botOff")}</Badge>;
  if (b.error) {
    return (
      <span className="flex max-w-72 flex-col gap-0.5">
        <span className="inline-flex items-center gap-1.5">
          <Badge tone="danger">{t("admin.sim.botRefused")}</Badge>
          <span className="truncate font-mono text-xs text-fg-2" title={b.error}>{b.error}</span>
        </span>
        <span className="text-xs text-fg-3">
          {b.error_at && <TimeText value={b.error_at} style="timeSeconds" />}
          {b.retry_at && <> · {t("admin.sim.retryAt")} <TimeText value={b.retry_at} style="timeSeconds" /></>}
        </span>
      </span>
    );
  }
  if (!b.balances_known) return <Badge tone="warn">{t("admin.sim.balancesUnknown")}</Badge>;
  return <Badge tone="success" dot>{t("admin.sim.botTrading")}</Badge>;
}

/** Roster counts the bots by role and sums what they hold. */
function Roster({ st, coin }: { st: SimStatus; coin: string }) {
  const { t } = useTranslation();
  const sum = (f: (b: SimBot) => string) => st.bots.reduce((a, b) => (dec.isDecimal(f(b)) ? dec.add(a, f(b)) : a), "0");
  const errors = st.bots.filter((b) => b.error).length;
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {ROLES.map((r) => (
          <div key={r} className="rounded-2 border border-line-1 px-3 py-2">
            <div className="text-xs text-fg-3">{t(`admin.sim.roles.${r}`)}</div>
            <div className="font-mono text-xl tabular-nums" data-testid={`sim-role-${r}`}>{st.bots.filter((b) => b.role === r).length}</div>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm">
        <span>{t("admin.sim.holding")} <Num value={sum((b) => b.coin)} decimals={0} unit={coin} /></span>
        <span><Num value={sum((b) => b.usdt)} decimals={2} unit="USDT" /></span>
        {st.perp && <span>{t("admin.sim.perpPosition")} <Num value={sum((b) => b.perp_position)} signed /></span>}
        <span className={errors ? "text-danger" : "text-fg-3"}>{t("admin.sim.refusedNow", { n: errors })}</span>
      </div>
    </div>
  );
}

/** Switches turns the cluster's flags on and off, each with a reason (flags.write). */
function Switches({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const [pending, setPending] = useState<Flag | null>(null);
  const flags = useQuery({ queryKey: ["admin", "flags"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/flags")).items });
  const writable = can(admin, "flags.write");
  if (flags.isPending) return <Skeleton className="h-28 w-full" />;
  if (flags.isError) return <p className="text-sm text-danger">{errorText(flags.error)}</p>;
  const list = SIM_FLAGS.map((k) => flags.data.find((f) => f.key === k)).filter((f): f is Flag => !!f);
  return (
    <div className="flex flex-col divide-y divide-line-1">
      {list.map((f) => (
        <div key={f.key} className="flex items-center gap-3 py-2">
          <Switch checked={f.enabled} disabled={!writable} onCheckedChange={() => setPending(f)} size="sm" aria-label={f.key} />
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="text-sm text-fg-1">{t(`admin.sim.flags.${f.key.split(".")[1]}`)}</span>
            <span className="font-mono text-xs text-fg-3">{f.key}</span>
          </span>
          <span className="flex flex-col items-end text-xs text-fg-3">
            <span>{f.updated_by || "—"}</span>
            <TimeText value={f.updated_at} />
          </span>
        </div>
      ))}
      {pending && (
        <DangerAction
          open
          onOpenChange={(o) => !o && setPending(null)}
          danger={pending.enabled}
          title={t("admin.risk.switchTitle", { action: pending.enabled ? t("admin.risk.turnOff") : t("admin.risk.turnOn"), key: pending.key })}
          description={pending.description}
          target={<span className="font-mono">{pending.key}</span>}
          confirmWord={pending.key.split(".").pop() ?? pending.key}
          run={async (reason) =>
            adminData(await adminApi.PUT("/admin/v1/flags/{key}", { params: { path: { key: pending.key } }, body: { enabled: !pending.enabled, reason } }))
          }
          success={t("admin.risk.switched", { key: pending.key, state: pending.enabled ? t("admin.risk.off") : t("admin.risk.on") })}
          invalidate={[["admin", "flags"]]}
        />
      )}
    </div>
  );
}

/** The settings that drive the bots, read only here (they change in price control). */
const CLUSTER = ["levels", "spread", "level_size", "requote_ticks", "daily_volume", "order_size", "orders_per_second", "cancels_per_second", "bot_usdt", "perp_bot_cap"];

function ClusterSettings({ params }: { params: SimStatus["params"] }) {
  const { t } = useTranslation();
  const p = params as Record<string, number>;
  return (
    <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1.5 text-sm">
      {CLUSTER.filter((k) => p[k] !== undefined).map((k) => (
        <div key={k} className="contents">
          <dt className="text-fg-2">{t(`admin.sim.params.${k}`)}</dt>
          <dd className="text-right font-mono tabular-nums">{k === "spread" ? pct(p[k]!).replace("+", "") : <Num value={String(p[k])} />}</dd>
        </div>
      ))}
    </dl>
  );
}

/** MintForm books more of the coin or USDT for the bots (all, or one role's) as one fund operation. */
function MintForm({ st, coin }: { st: SimStatus; coin: string }) {
  const { t } = useTranslation();
  const [asset, setAsset] = useState(coin);
  const [amount, setAmount] = useState("");
  const [role, setRole] = useState("all");
  const [reference, setReference] = useState("");
  const [last, setLast] = useState<Approval | null>(null);
  const a = amount.trim();
  const amountOk = dec.isDecimal(a) && dec.gt(a, "0");
  const n = st.bots.filter((b) => role === "all" || b.role === role).length;
  const each = amountOk && n ? dec.normalize(dec.div(a, String(n), 2, "down")) : null;
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-fg-3">{t("admin.sim.mintHelp")}</p>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.common.asset")}
          <Segmented size="md" value={asset} onValueChange={setAsset} items={[{ value: coin, label: coin }, { value: "USDT", label: "USDT" }]} />
        </label>
        <label className="flex w-48 flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.sim.mintAmount")}
          <Input
            id="sim-mint-amount"
            value={amount}
            onValueChange={setAmount}
            inputMode="decimal"
            unit={asset}
            error={amount !== "" && !amountOk ? t("admin.funds.invalidAmount") : undefined}
          />
        </label>
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.sim.mintTo")}
          <Segmented
            size="md"
            value={role}
            onValueChange={setRole}
            items={[{ value: "all", label: t("admin.sim.allBots") }, ...ROLES.map((r) => ({ value: r, label: t(`admin.sim.roles.${r}`) }))]}
          />
        </label>
      </div>
      <label className="flex flex-col gap-1.5 text-sm text-fg-2">
        {t("admin.funds.reference")}
        <Input value={reference} onValueChange={setReference} maxLength={64} placeholder={t("admin.funds.referenceHint")} />
      </label>
      {each && <p className="text-sm text-fg-2" data-testid="sim-mint-each">{t("admin.sim.mintEach", { n, each, asset })}</p>}
      <FundAction
        trigger={(open) => (
          <Button disabled={!amountOk || n === 0} onClick={open} icon={<ArrowRight size={16} />} className="self-start" data-testid="sim-mint">
            {t("admin.sim.mintSubmit")}
          </Button>
        )}
        danger={false}
        title={t("admin.sim.mintTitle")}
        description={t("admin.funds.directHint")}
        target={
          <span className="inline-flex flex-wrap items-center gap-2">
            <Num value={a} unit={asset} signed />
            <span>{t("admin.sim.mintEach", { n, each: each ?? "—", asset })}</span>
          </span>
        }
        confirmWord="mint"
        run={async (reason, key) => {
          if (!amountOk) throw new FormError(t("admin.funds.invalidAmount"));
          return adminData(
            await adminApi.POST("/admin/v1/sim/mint", {
              params: { header: { "Idempotency-Key": key } },
              body: {
                asset, amount: a, reason, ...(role === "all" ? {} : { role: role as (typeof ROLES)[number] }),
                ...(reference.trim() ? { reference: reference.trim() } : {}),
              },
            }),
          );
        }}
        onDone={(op) => {
          setLast(op);
          setAmount("");
          setReference("");
        }}
      />
      {last && <MintOutcome a={last} />}
    </div>
  );
}

/** MintOutcome is the last mint: booked (each bot's share), refused, or waiting for a second administrator. */
function MintOutcome({ a }: { a: Approval }) {
  const { t } = useTranslation();
  const p = a.payload as Record<string, string>;
  return (
    <div className="flex flex-col gap-2 text-sm animate-rise" data-testid="sim-mint-outcome">
      <div className="flex items-center gap-2">
        <EnumBadge group="approvalStatus" code={a.status} />
        <span className="text-fg-3">{a.mode === "SINGLE" ? t("admin.funds.single") : t(`admin.funds.escalation.${a.escalation || "REQUESTED"}`)}</span>
      </div>
      {a.status === "FAILED" ? <p className="text-danger">{a.result}</p> : <MintShares payload={p} full />}
      {a.status === "EXECUTED" && <p className="text-xs text-fg-3">{a.result}</p>}
      {a.status === "PENDING" && <p className="text-fg-2">{t("admin.funds.waitingHint")}</p>}
    </div>
  );
}
