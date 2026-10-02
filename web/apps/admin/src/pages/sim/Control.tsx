import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, Input, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction, FormError } from "../../kit/actions";
import { Card, Page } from "../../kit/Page";
import { ImpactLines, pct, price, simEventsKey, simKey, useSim, type SimEventType } from "./common";

// Price control (ASTRA design §6.1): the model's and the bots' settings,
// and the price events. One operator moves the price by at most 30% at
// once and 50% in any hour (market-sim measures it); beyond, the change
// waits on the approvals for a second administrator with sim.control.

/** The settings by group, in the order they read. */
const GROUPS: { key: string; fields: string[] }[] = [
  { key: "model", fields: ["p0", "beta", "w_btc", "w_eth", "theta", "sigma", "mu"] },
  { key: "guards", fields: ["max_minute_move", "floor", "ceiling"] },
  { key: "makers", fields: ["levels", "spread", "level_ticks", "level_size", "requote_ticks"] },
  { key: "takers", fields: ["daily_volume", "order_size", "trend_minutes", "trend_strength"] },
  { key: "pace", fields: ["orders_per_second", "cancels_per_second", "bot_usdt"] },
  { key: "perp", fields: ["perp_daily_volume", "perp_bot_cap", "perp_margin"] },
];

type Approved = { approval?: { id: string } };

export default function SimControl({ admin }: { admin: Admin }) {
  const { t } = useTranslation();
  const q = useSim(5_000);
  const control = can(admin, "sim.control");
  if (q.isError) return <ErrorState message={String(q.error)} onRetry={() => void q.refetch()} />;
  return (
    <Page title={t("admin.nav.simControl")} help={t("admin.sim.controlHelp")}>
      <Card title={t("admin.sim.events")} extra={<span className="text-xs text-fg-3">{t("admin.sim.share")}</span>}>
        {q.data ? <Launcher target={Number(q.data.target_price ?? 0)} perp={q.data.perp} control={control} /> : <Skeleton className="h-40 w-full" />}
      </Card>
      {q.data ? <Params current={q.data.params} version={q.data.version} control={control} /> : <Skeleton className="h-64 w-full" />}
    </Page>
  );
}

/** submitted is a change's toast: done, or waiting for a second administrator. */
function useSubmitted() {
  const { t } = useTranslation();
  return (done: string) => (res: unknown) => ((res as Approved).approval ? t("admin.sim.awaiting") : done);
}

function Params({ current, version, control }: { current: Record<string, number>; version: number; control: boolean }) {
  const { t } = useTranslation();
  const submitted = useSubmitted();
  const [draft, setDraft] = useState<Record<string, string>>({});
  const value = (k: string) => draft[k] ?? String(current[k] ?? "");
  const changed = Object.keys(draft).filter((k) => draft[k] !== String(current[k]));
  return (
    <Card
      title={t("admin.sim.settings")}
      extra={
        <span className="flex items-center gap-2">
          <span className="font-mono text-xs text-fg-3">v{version}</span>
          {control && (
            <DangerAction
              trigger={(open) => (
                <Button size="sm" disabled={!changed.length} onClick={open} data-testid="sim-params-save">
                  {t("admin.sim.saveSettings", { n: changed.length })}
                </Button>
              )}
              danger={false}
              title={t("admin.sim.settingsTitle")}
              description={t("admin.sim.settingsHint")}
              target={
                <span className="flex flex-col font-mono text-xs">
                  {changed.map((k) => (
                    <span key={k}>
                      {k}: {String(current[k])} → {draft[k]}
                    </span>
                  ))}
                </span>
              }
              confirmWord="settings"
              run={async (reason) => {
                const params: Record<string, number> = { ...current };
                for (const k of changed) {
                  const n = Number(draft[k]);
                  if (!Number.isFinite(n)) throw new FormError(t("admin.sim.badNumber", { field: k }));
                  params[k] = n;
                }
                return adminData(await adminApi.PUT("/admin/v1/sim/params", { body: { params, reason } }));
              }}
              success={submitted(t("admin.sim.settingsSaved"))}
              invalidate={[simKey]}
              onDone={() => setDraft({})}
            />
          )}
        </span>
      }
    >
      <div className="grid gap-5 lg:grid-cols-2">
        {GROUPS.map((g) => (
          <fieldset key={g.key} className="flex flex-col gap-2">
            <legend className="mb-1 text-sm font-semibold text-fg-1">{t(`admin.sim.groups.${g.key}`)}</legend>
            {g.fields.map((k) => (
              <label key={k} className="grid grid-cols-[1fr_9rem] items-center gap-3 text-sm">
                <span className="flex flex-col">
                  <span className="text-fg-1">{t(`admin.sim.params.${k}`)}</span>
                  <span className="font-mono text-xs text-fg-3">{k}</span>
                </span>
                <Input
                  size="sm"
                  value={value(k)}
                  inputMode="decimal"
                  disabled={!control}
                  onValueChange={(v) => setDraft({ ...draft, [k]: v })}
                  className={changed.includes(k) ? "font-mono text-brand" : "font-mono"}
                  aria-label={k}
                />
              </label>
            ))}
          </fieldset>
        ))}
      </div>
    </Card>
  );
}

/** EventSpec is what each event asks for. */
type Field = "size" | "price" | "mu" | "factor" | "duration" | "hold";
const SPECS: { type: SimEventType; fields: Field[] }[] = [
  { type: "JUMP", fields: ["size", "duration"] },
  { type: "TARGET", fields: ["price", "duration", "hold"] },
  { type: "TREND", fields: ["mu", "duration"] },
  { type: "VOLATILITY", fields: ["factor", "duration"] },
  { type: "PAUSE", fields: ["duration"] },
  { type: "HALT", fields: [] },
  { type: "REANCHOR", fields: [] },
];

function Launcher({ target, perp, control }: { target: number; perp: string; control: boolean }) {
  const { t } = useTranslation();
  const [type, setType] = useState<SimEventType>("JUMP");
  const [v, setV] = useState<Record<Field, string>>({ size: "5", price: "", mu: "10", factor: "2", duration: "60", hold: "0" });
  const [startsAt, setStartsAt] = useState("");
  const spec = SPECS.find((s) => s.type === type)!;
  const submitted = useSubmitted();
  const num = (f: Field) => Number(v[f]);
  // Where the price would be: a jump from the target, or the target event's price.
  const expected = type === "JUMP" ? target * (1 + num("size") / 100) : type === "TARGET" ? num("price") : null;
  const body = () => {
    const out: Record<string, unknown> = { type };
    for (const f of spec.fields) {
      if (v[f].trim() === "" || !Number.isFinite(num(f))) throw new FormError(t("admin.sim.badNumber", { field: t(`admin.sim.fields.${f}`) }));
    }
    if (spec.fields.includes("size")) out.size = num("size") / 100;
    if (spec.fields.includes("price")) out.price = v.price.trim();
    if (spec.fields.includes("mu")) out.mu = Math.log(1 + num("mu") / 100);
    if (spec.fields.includes("factor")) out.factor = num("factor");
    if (spec.fields.includes("duration")) out.duration_seconds = Math.round(num("duration"));
    if (spec.fields.includes("hold")) out.hold_seconds = Math.round(num("hold"));
    if (startsAt) {
      const at = new Date(startsAt);
      if (Number.isNaN(at.getTime())) throw new FormError(t("admin.sim.badTime"));
      out.starts_at = at.toISOString().replace(/\.\d+Z$/, "Z");
    }
    return out;
  };
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-1.5" role="tablist" aria-label={t("admin.sim.eventType")}>
        {SPECS.map((s) => (
          <button
            key={s.type}
            type="button"
            role="tab"
            aria-selected={type === s.type}
            onClick={() => setType(s.type)}
            className={
              type === s.type
                ? "rounded-2 border border-brand bg-brand-soft px-3 py-1.5 text-sm text-fg-1"
                : "rounded-2 border border-line-1 px-3 py-1.5 text-sm text-fg-2 hover:border-line-2"
            }
          >
            {t(`admin.sim.types.${s.type}`)}
          </button>
        ))}
      </div>
      <p className="text-sm text-fg-3">{t(`admin.sim.typeHelp.${type}`)}</p>
      <div className="flex flex-wrap items-end gap-3">
        {spec.fields.map((f) => (
          <label key={f} className="flex w-40 flex-col gap-1.5 text-sm text-fg-2">
            {t(`admin.sim.fields.${f}`)}
            <Input id={`sim-${f}`} size="sm" value={v[f]} inputMode="decimal" onValueChange={(x) => setV({ ...v, [f]: x })} className="font-mono" />
          </label>
        ))}
        <label className="flex flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.sim.startsAt")}
          <input
            type="datetime-local"
            value={startsAt}
            onChange={(e) => setStartsAt(e.target.value)}
            className="h-8 rounded-1 border border-line-1 bg-bg-1 px-2 text-sm text-fg-1"
            aria-label={t("admin.sim.startsAt")}
          />
        </label>
        {control && (
          <DangerAction
            trigger={(open) => (
              <Button onClick={open} data-testid="sim-event-start">
                {t("admin.sim.start")}
              </Button>
            )}
            danger={type === "HALT" || (expected !== null && Math.abs(expected / target - 1) > 0.1)}
            title={t("admin.sim.startTitle", { type: t(`admin.sim.types.${type}`) })}
            description={t(`admin.sim.typeHelp.${type}`)}
            target={
              <span className="flex flex-col gap-1">
                <span>
                  {t(`admin.sim.types.${type}`)}
                  {expected !== null && target > 0 && (
                    <span className="font-mono">
                      {" "}
                      {price(target)} → {price(expected)} ({pct(expected / target - 1)})
                    </span>
                  )}
                </span>
                {expected !== null && expected > 0 && perp && <Impact price={expected} />}
              </span>
            }
            confirmWord={type.toLowerCase()}
            run={async (reason) => adminData(await adminApi.POST("/admin/v1/sim/events", { body: { ...(body() as { type: SimEventType }), reason } }))}
            success={submitted(t("admin.sim.started"))}
            invalidate={[simKey, simEventsKey]}
          />
        )}
        <Link to="/approvals" className="text-sm text-info hover:underline">
          {t("admin.sim.toApprovals")}
        </Link>
      </div>
    </div>
  );
}

/** Impact shows what a price would do to the perpetual's positions (ASTRA design §6.3). */
function Impact({ price: at }: { price: number }) {
  const { t } = useTranslation();
  const p = at.toFixed(8).replace(/0+$/, "").replace(/\.$/, "");
  const q = useQuery({
    queryKey: [...simKey, "impact", p],
    queryFn: async () => adminData(await adminApi.POST("/admin/v1/sim/impact", { body: { price: p } })),
  });
  let body: ReactNode;
  if (q.isPending) body = <Skeleton className="h-10 w-full" />;
  else if (q.isError) body = <span className="text-warn">{t("admin.sim.impactUnknown")}</span>;
  else body = <ImpactLines i={q.data} />;
  return (
    <span className="mt-1 rounded-1 border border-line-1 bg-bg-2 px-2 py-1.5 text-xs">
      <Badge tone="info">{t("admin.sim.impact")}</Badge> {body}
    </span>
  );
}
