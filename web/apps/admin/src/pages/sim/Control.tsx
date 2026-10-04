import { ApiError } from "@exchange/core";
import { adminApi, adminData, can, type Admin } from "@exchange/core/api/admin";
import { Badge, Button, ErrorState, Input, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { DangerAction, FormError, useAdminT } from "../../kit/actions";
import { TimeText } from "../../kit/format";
import { Card, Page } from "../../kit/Page";
import { EndEvent, ImpactLines, minutes, pct, price, simEventsKey, simKey, useEventText, useSim, type SimEvent, type SimEventType } from "./common";
import { ReadOnly } from "../../kit/ReadOnly";
import {
  MAX_LEAD, newTarget, SOLO_SPIKE, SPIKE_WIDTH, spikeMarkShare, spikePrices, TargetFields, TargetPlan, TargetPreview, targetBody, tryCheck,
  type TargetDraft,
} from "./target";

// Price control (ASTRA design §6.1): the model's and the bots' settings,
// and the price events. One operator moves the price by at most 30% at
// once and 50% in any hour (market-sim measures it); beyond, the change
// waits on the approvals for a second administrator with sim.control.
// A threshold target ("within N minutes, at or above X", A6) has its own
// form with its plan previewed, and a banner while one runs.

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
      <ReadOnly admin={admin} perm="sim.control" />
      {q.data && <TargetBanner events={q.data.events} control={control} />}
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
                  className={changed.includes(k) ? "font-mono text-brand-strong" : "font-mono"}
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

/** EventSpec is what each event asks for; a threshold target has its own form (target.tsx). */
type Field = "size" | "mu" | "factor" | "duration" | "width";
const SPECS: { type: SimEventType; fields: Field[] }[] = [
  { type: "JUMP", fields: ["size", "duration"] },
  { type: "TARGET", fields: [] },
  { type: "SPIKE", fields: ["size", "width"] },
  { type: "TREND", fields: ["mu", "duration"] },
  { type: "VOLATILITY", fields: ["factor", "duration"] },
  { type: "PAUSE", fields: ["duration"] },
  { type: "HALT", fields: [] },
  { type: "REANCHOR", fields: [] },
];

/** utc is a datetime-local value as an RFC 3339 time in UTC, "" when blank. */
function utc(local: string): string {
  if (!local) return "";
  const at = new Date(local);
  return Number.isNaN(at.getTime()) ? "" : at.toISOString().replace(/\.\d+Z$/, "Z");
}

function Launcher({ target, perp, control }: { target: number; perp: string; control: boolean }) {
  const { t } = useTranslation();
  const at = useAdminT();
  const [type, setType] = useState<SimEventType>("JUMP");
  const [v, setV] = useState<Record<Field, string>>({ size: "5", mu: "10", factor: "2", duration: "60", width: String(SPIKE_WIDTH) });
  const [draft, setDraft] = useState<TargetDraft>(newTarget);
  const [startsAt, setStartsAt] = useState("");
  // How far the server's clock is ahead of this browser's (the target
  // preview's `now`): a target's spikes are timed by the server (review 29).
  const skew = useRef(0);
  const onClock = useCallback((s: number) => {
    skew.current = s;
  }, []);
  const serverNow = () => Date.now() + skew.current;
  const spec = SPECS.find((s) => s.type === type)!;
  const submitted = useSubmitted();
  const eventText = useEventText();
  const num = (f: Field) => Number(v[f]);
  const checked = type === "TARGET" ? tryCheck(draft, at) : null;
  // Where the price would be: a jump from the target, a spike's tip, the level of a target.
  const expected = type === "JUMP" || type === "SPIKE" ? target * (1 + num("size") / 100) : checked ? Number(checked.level) : null;
  const body = (): Record<string, unknown> => {
    if (startsAt && !utc(startsAt)) throw new FormError(t("admin.sim.badTime"));
    if (startsAt && new Date(startsAt).getTime() > serverNow() + MAX_LEAD) throw new FormError(t("admin.simTarget.tooFar"));
    if (type === "TARGET") {
      const out = targetBody(draft, startsAt ? new Date(startsAt) : new Date(serverNow()), at);
      return startsAt ? { ...out, starts_at: utc(startsAt) } : out;
    }
    const out: Record<string, unknown> = { type };
    for (const f of spec.fields) {
      if (v[f].trim() === "" || !Number.isFinite(num(f))) throw new FormError(t("admin.sim.badNumber", { field: t(`admin.sim.fields.${f}`) }));
    }
    if (spec.fields.includes("size")) out.size = num("size") / 100;
    if (spec.fields.includes("mu")) out.mu = Math.log(1 + num("mu") / 100);
    if (spec.fields.includes("factor")) out.factor = num("factor");
    if (spec.fields.includes("duration")) out.duration_seconds = Math.round(num("duration"));
    if (spec.fields.includes("width")) out.width_seconds = Math.round(num("width"));
    if (startsAt) out.starts_at = utc(startsAt);
    return out;
  };
  const create = async (reason: string) => {
    try {
      return adminData(await adminApi.POST("/admin/v1/sim/events", { body: { ...(body() as { type: SimEventType }), reason } }));
    } catch (err) {
      // market-sim's bounds, said with the number it gave: the shortest
      // window a target reaches its level in, the largest spike the price
      // band lets the quotes reach.
      const detail = (code: string, key: string) => (err instanceof ApiError && err.code === code ? Number(err.details[key]) : NaN);
      const least = detail("SIM_TARGET_INFEASIBLE", "min_duration_seconds");
      if (Number.isFinite(least) && least > 0) throw new FormError(t("admin.simTarget.infeasible", { min: minutes(least) }));
      const most = detail("SIM_SPIKE_BEYOND_BAND", "max");
      if (Number.isFinite(most) && most > 0) throw new FormError(t("admin.simTarget.beyondBand", { max: pct(most, 1).replace("+", "") }));
      throw err;
    }
  };
  // The confirmation says what it does (a target's own words and spikes) and what it does to the perpetual.
  const what = type === "TARGET" && checked ? eventText({ type, size: 0, price: checked.level, mu: 0, factor: 0, duration_seconds: checked.window, hold_seconds: checked.hold,
    direction: draft.direction === "AUTO" ? null : draft.direction, spikes: checked.spikes }) : t(`admin.sim.types.${type}`);
  // A spike's liquidations are measured where the mark goes, about half of it (spikeMarkShare).
  const marks = type === "SPIKE" && expected !== null ? [target * (1 + (num("size") / 100) * spikeMarkShare(num("width")))] : checked && target > 0 ? spikePrices(checked, target) : [];
  const shares = type === "SPIKE" ? [spikeMarkShare(num("width"))] : (checked?.spikes ?? []).map((s) => spikeMarkShare(s.width));
  const share = shares.length ? pct(Math.max(...shares), 0).replace("+", "") : null;
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
            data-testid={`sim-type-${s.type}`}
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
      {type === "TARGET" && <TargetFields draft={draft} onChange={setDraft} />}
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
            danger={
              type === "HALT" ||
              (type === "SPIKE" && Math.abs(num("size") / 100) > SOLO_SPIKE) ||
              (expected !== null && target > 0 && Math.abs(expected / target - 1) > 0.1)
            }
            title={t("admin.sim.startTitle", { type: t(`admin.sim.types.${type}`) })}
            description={t(`admin.sim.typeHelp.${type}`)}
            target={
              <span className="flex flex-col gap-1">
                <span>
                  {what}
                  {expected !== null && expected > 0 && target > 0 && (
                    <span className="font-mono">
                      {" "}
                      {price(target)} → {price(expected)} ({pct(expected / target - 1)})
                    </span>
                  )}
                </span>
                {checked?.spikes.map((s, i) => (
                  <span key={i} className="text-xs text-fg-2">
                    {t("admin.simTarget.spikeLine", { n: i + 1, m: minutes(s.offset), size: pct(s.size, 1), s: s.width })}
                  </span>
                ))}
                {perp && type !== "SPIKE" && expected !== null && expected > 0 && (
                  <Impact price={expected} label={type === "TARGET" ? t("admin.simTarget.impactLevel") : undefined} />
                )}
                {perp && marks.filter((p) => p > 0).map((p) => <Impact key={p} price={p} label={t("admin.simTarget.impactSpike", { price: price(p) })} />)}
                {share && <span className="text-xs text-fg-3">{t("admin.simTarget.markNote", { share })}</span>}
              </span>
            }
            confirmWord={type.toLowerCase()}
            run={create}
            success={submitted(t("admin.sim.started"))}
            invalidate={[simKey, simEventsKey]}
          />
        )}
        <Link to="/approvals" className="text-sm text-info hover:underline">
          {t("admin.sim.toApprovals")}
        </Link>
      </div>
      {type === "TARGET" && <TargetPreview draft={draft} startsAt={utc(startsAt)} target={target} onClock={onClock} />}
    </div>
  );
}

/**
 * TargetBanner is the running threshold target over the price control (A6):
 * where it is against its plan, its plan beside where the price went, and
 * ending it; the queued ones under it.
 */
function TargetBanner({ events, control }: { events: SimEvent[]; control: boolean }) {
  const { t } = useTranslation();
  const eventText = useEventText();
  const targets = events.filter((e) => e.type === "TARGET" && (e.status === "RUNNING" || e.status === "SCHEDULED"));
  if (!targets.length) return null;
  const running = targets.find((e) => e.status === "RUNNING");
  return (
    <div className="flex flex-col gap-3 rounded-2 border border-brand bg-brand-soft px-4 py-3" data-testid="sim-target-banner">
      {targets.map((e) => (
        <div key={e.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-fg-1">
          <Badge tone={e.status === "RUNNING" ? "brand" : "info"} dot={e.status === "RUNNING"}>
            {t(e.status === "RUNNING" ? "admin.simTarget.running" : "admin.simTarget.queued")}
          </Badge>
          <span className="font-medium">{eventText(e)}</span>
          {e.status === "SCHEDULED" && <span className="text-xs text-fg-2">{t("admin.sim.begins")} <TimeText value={e.starts_at} style="datetimeSeconds" /></span>}
          <span className="ml-auto flex items-center gap-2">
            <Link to="/sim/events" className="text-xs text-info hover:underline">{t("admin.simTarget.toEvents")}</Link>
            {control && <EndEvent e={e} text={eventText(e)} />}
          </span>
        </div>
      ))}
      {running && <TargetPlan id={running.id} live height={200} />}
    </div>
  );
}

/** Impact shows what a price would do to the perpetual's positions (ASTRA design §6.3); label says which price. */
function Impact({ price: at, label }: { price: number; label?: ReactNode }) {
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
      <Badge tone="info">{t("admin.sim.impact")}</Badge> {label && <span className="text-fg-2">{label}</span>} {body}
    </span>
  );
}
