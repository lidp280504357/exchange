import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, IconButton, Input, Segmented, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { Fragment, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { FormError, useAdminT } from "../../kit/actions";
import { TimeText, useTimeText } from "../../kit/format";
import { PriceLines, type PriceLine, type PricePoint } from "../../kit/PriceLines";
import { minutes, pct, price, simKey, type SimEvent } from "./common";

// Threshold targets and spikes (ASTRA design §3, §6.2, A6): the price
// control's form for "within N minutes, at or above (below) X", with an
// optional hold at the level and spikes, its plan previewed before it is
// asked for; a target's plan beside where the price went; the running
// target's banner. market-sim plans and checks them
// (docs/runbook/market-sim.md); the console says what it answered.

export type SimPlan = AdminSchemas["SimPlan"];
type PlanPoint = AdminSchemas["SimPlanPoint"];
type AdminT = ReturnType<typeof useAdminT>;

export type SpikeDraft = { minute: string; size: string; width: string };
export type TargetDraft = {
  direction: "AUTO" | "ABOVE" | "BELOW";
  level: string;
  minutes: string;
  then: "FOLLOW" | "HOLD";
  hold: string;
  spikes: SpikeDraft[];
};

export const newTarget = (): TargetDraft => ({ direction: "AUTO", level: "", minutes: "30", then: "FOLLOW", hold: "10", spikes: [] });

/** A spike's width by default and at most, in seconds; its size alone and approved (market-sim's domain). */
export const SPIKE_WIDTH = 20;
const MAX_SPIKE_WIDTH = 60;
export const SOLO_SPIKE = 0.05;
const MAX_SPIKE = 0.1;
/** A target's window and hold at most, in minutes (a day). */
const MAX_MINUTES = 1440;

/** closingSeconds is how long a window of seconds closes in: its last tenth, a minute at least (market-sim's ClosingAt). */
export const closingSeconds = (window: number) => Math.min(window, Math.max(window / 10, 60));

/**
 * spikeMarkShare is the share of a spike's size the perpetual's mark takes
 * at most, which its liquidations are measured at: the mark follows the
 * index, the spot's 60-second TWAP averaged with the book's middle, so at
 * the tip it has moved about half of it; the TWAP alone holds 1.5 +
 * width/2 seconds of the tip in its 60 (as the admin-service measures a
 * request).
 */
export const spikeMarkShare = (width: number) => Math.min(1, Math.max((1 + 1.5 / 60) / 2, (1.5 + (width || SPIKE_WIDTH) / 2) / 60));

const iso = (d: Date) => d.toISOString().replace(/\.\d+Z$/, "Z");

/** Spike is a draft's spike read: seconds after the start, a share of the planned price, seconds wide. */
export type Spike = { offset: number; size: number; width: number };

/** Checked is a draft read: the level, the window and the hold in seconds, the spikes. */
export type Checked = { level: string; window: number; hold: number; spikes: Spike[] };

/** checkTarget reads a draft; a FormError says what is wrong. */
export function checkTarget(d: TargetDraft, at: AdminT): Checked {
  const level = Number(d.level);
  if (d.level.trim() === "" || !Number.isFinite(level) || level <= 0) throw new FormError(at("simTarget.badLevel"));
  const mins = Number(d.minutes);
  if (d.minutes.trim() === "" || !Number.isFinite(mins) || mins < 1 || mins > MAX_MINUTES) throw new FormError(at("simTarget.badMinutes"));
  const seconds = Math.round(mins * 60);
  let hold = 0;
  if (d.then === "HOLD") {
    const h = Number(d.hold);
    if (d.hold.trim() === "" || !Number.isFinite(h) || h <= 0 || h > MAX_MINUTES) throw new FormError(at("simTarget.badHold"));
    hold = Math.round(h * 60);
  }
  const last = seconds - closingSeconds(seconds);
  const spikes = d.spikes.map((s, i) => {
    const offset = Math.round(Number(s.minute) * 60);
    const size = Number(s.size) / 100;
    const width = s.width.trim() === "" ? SPIKE_WIDTH : Number(s.width);
    const ok =
      s.minute.trim() !== "" && Number.isFinite(offset) && offset > 0 && offset < last &&
      s.size.trim() !== "" && Number.isFinite(size) && size !== 0 && Math.abs(size) <= MAX_SPIKE &&
      Number.isInteger(width) && width >= 1 && width <= MAX_SPIKE_WIDTH;
    if (!ok) throw new FormError(at("simTarget.badSpike", { n: i + 1, last: minutes(last) }));
    return { offset, size, width };
  });
  return { level: d.level.trim(), window: seconds, hold, spikes };
}

/** targetBody is a draft as the console takes it, its spikes at their times from start. */
export function targetBody(d: TargetDraft, start: Date, at: AdminT) {
  const c = checkTarget(d, at);
  const out: Record<string, unknown> = { type: "TARGET", price: c.level, duration_seconds: c.window, then: d.then };
  if (d.direction !== "AUTO") out.direction = d.direction;
  if (c.hold) out.hold_seconds = c.hold;
  if (c.spikes.length) out.spikes = c.spikes.map((s) => ({ at: iso(new Date(start.getTime() + s.offset * 1000)), size: s.size, width_seconds: s.width }));
  return out;
}

/** tryCheck is a draft read, or null while it is incomplete. */
export function tryCheck(d: TargetDraft, at: AdminT): Checked | null {
  try {
    return checkTarget(d, at);
  } catch {
    return null;
  }
}

/**
 * spikePrices are where a target's spikes would take the mark at worst,
 * each way: the deepest spike down from the lower of the target now and
 * the level, the highest up from the higher (the plan runs between them).
 */
export function spikePrices(c: Checked, now: number): number[] {
  const lo = Math.min(now, Number(c.level));
  const hi = Math.max(now, Number(c.level));
  const down = c.spikes.filter((s) => s.size < 0).map((s) => lo * (1 + s.size * spikeMarkShare(s.width)));
  const up = c.spikes.filter((s) => s.size > 0).map((s) => hi * (1 + s.size * spikeMarkShare(s.width)));
  return [...(down.length ? [Math.min(...down)] : []), ...(up.length ? [Math.max(...up)] : [])];
}

/** useDebounced is a string once it stopped changing for ms (a string: the same one again is no change). */
function useDebounced(value: string, ms: number): string {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

/** TargetFields are the form's fields: the side and the level, the window, what follows the crossing, the spikes. */
export function TargetFields({ draft: d, onChange }: { draft: TargetDraft; onChange: (d: TargetDraft) => void }) {
  const { t } = useTranslation();
  const set = (p: Partial<TargetDraft>) => onChange({ ...d, ...p });
  const setSpike = (i: number, p: Partial<SpikeDraft>) => set({ spikes: d.spikes.map((s, j) => (j === i ? { ...s, ...p } : s)) });
  const seconds = Math.round(Number(d.minutes) * 60);
  const closesAt = Number.isFinite(seconds) && seconds >= 60 ? minutes(seconds - closingSeconds(seconds)) : null;
  return (
    <div className="flex flex-col gap-4" data-testid="sim-target-form">
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1.5 text-sm text-fg-2">
          <span>{t("admin.simTarget.direction")}</span>
          <Segmented
            size="sm"
            value={d.direction}
            onValueChange={(v) => set({ direction: v as TargetDraft["direction"] })}
            items={[
              { value: "AUTO", label: t("admin.simTarget.auto") },
              { value: "ABOVE", label: `≥ ${t("admin.simTarget.above")}` },
              { value: "BELOW", label: `≤ ${t("admin.simTarget.below")}` },
            ]}
            aria-label={t("admin.simTarget.direction")}
          />
        </div>
        <label className="flex w-40 flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.simTarget.level")}
          <Input id="sim-target-level" size="sm" value={d.level} inputMode="decimal" onValueChange={(v) => set({ level: v })} className="font-mono" />
        </label>
        <label className="flex w-36 flex-col gap-1.5 text-sm text-fg-2">
          {t("admin.simTarget.minutes")}
          <Input id="sim-target-minutes" size="sm" value={d.minutes} inputMode="decimal" unit={t("admin.simTarget.min")} onValueChange={(v) => set({ minutes: v })} className="font-mono" />
        </label>
        <div className="flex flex-col gap-1.5 text-sm text-fg-2">
          <span>{t("admin.simTarget.then")}</span>
          <Segmented
            size="sm"
            value={d.then}
            onValueChange={(v) => set({ then: v as TargetDraft["then"] })}
            items={[
              { value: "FOLLOW", label: t("admin.simTarget.follow") },
              { value: "HOLD", label: t("admin.simTarget.hold") },
            ]}
            aria-label={t("admin.simTarget.then")}
          />
        </div>
        {d.then === "HOLD" && (
          <label className="flex w-36 flex-col gap-1.5 text-sm text-fg-2">
            {t("admin.simTarget.holdMinutes")}
            <Input id="sim-target-hold" size="sm" value={d.hold} inputMode="decimal" unit={t("admin.simTarget.min")} onValueChange={(v) => set({ hold: v })} className="font-mono" />
          </label>
        )}
      </div>
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <span className="font-medium text-fg-1">{t("admin.simTarget.spikes")}</span>
          {closesAt && <span className="text-xs text-fg-3">{t("admin.simTarget.closingFrom", { m: closesAt })}</span>}
          <Button
            size="sm"
            variant="secondary"
            icon={<Plus size={14} />}
            onClick={() => set({ spikes: [...d.spikes, { minute: "", size: "-3", width: String(SPIKE_WIDTH) }] })}
            data-testid="sim-spike-add"
          >
            {t("admin.simTarget.addSpike")}
          </Button>
        </div>
        {d.spikes.length === 0 ? (
          <p className="text-xs text-fg-3">{t("admin.simTarget.noSpikes")}</p>
        ) : (
          <div className="grid grid-cols-[2rem_repeat(3,8.5rem)_auto] items-center gap-x-3 gap-y-2 text-sm" data-testid="sim-spikes">
            <span />
            <span className="text-xs text-fg-3">{t("admin.simTarget.spikeAt")}</span>
            <span className="text-xs text-fg-3">{t("admin.simTarget.spikeSize")}</span>
            <span className="text-xs text-fg-3">{t("admin.simTarget.spikeWidth")}</span>
            <span />
            {d.spikes.map((s, i) => (
              <Fragment key={i}>
                <span className="font-mono text-xs text-fg-3">#{i + 1}</span>
                <Input size="sm" value={s.minute} inputMode="decimal" unit={t("admin.simTarget.min")} onValueChange={(v) => setSpike(i, { minute: v })} className="font-mono" aria-label={`${t("admin.simTarget.spikeAt")} #${i + 1}`} />
                <Input size="sm" value={s.size} inputMode="decimal" unit="%" onValueChange={(v) => setSpike(i, { size: v })} className="font-mono" aria-label={`${t("admin.simTarget.spikeSize")} #${i + 1}`} />
                <Input size="sm" value={s.width} inputMode="numeric" unit={t("admin.simTarget.sec")} onValueChange={(v) => setSpike(i, { width: v })} className="font-mono" aria-label={`${t("admin.simTarget.spikeWidth")} #${i + 1}`} />
                <IconButton icon={<X />} size="xs" label={t("admin.simTarget.remove")} onClick={() => set({ spikes: d.spikes.filter((_, j) => j !== i) })} />
              </Fragment>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

const NO_LINES: PriceLine[] = [];

/** useChartLines are a plan's lines: its band dashed, the plan, the level, and the spikes as marks. */
function useChartLines(extra: PriceLine[] = NO_LINES) {
  const { t } = useTranslation();
  return useMemo<PriceLine[]>(
    () => [
      { key: "high", label: t("admin.simTarget.high"), className: "stroke-fg-3", dotClassName: "bg-fg-3", dashed: true },
      { key: "low", label: t("admin.simTarget.low"), className: "stroke-fg-3", dotClassName: "bg-fg-3", dashed: true },
      { key: "level", label: t("admin.simTarget.level"), className: "stroke-warn", dotClassName: "bg-warn", dashed: true },
      { key: "plan", label: t("admin.simTarget.plan"), className: "stroke-chart-3", dotClassName: "bg-chart-3" },
      ...extra,
      { key: "spike", label: t("admin.simTarget.spikes"), className: "stroke-danger", dotClassName: "bg-danger", dots: true },
    ],
    [t, extra],
  );
}

/** chartPoints lays a plan's minutes out with the level, the spikes at their nearest minute, and more values by time. */
function chartPoints(
  points: PlanPoint[],
  level: number,
  spikes: { at: number; size: number }[],
  label: (at: string) => string,
  more?: (at: number) => Record<string, number | null>,
): PricePoint[] {
  const out: PricePoint[] = points.map((p) => {
    const at = Date.parse(p.at);
    return { at, label: label(p.at), values: { plan: Number(p.plan), low: Number(p.low), high: Number(p.high), level, spike: null, ...more?.(at) } };
  });
  for (const s of spikes) {
    let best = -1;
    out.forEach((p, i) => {
      if (best < 0 || Math.abs(p.at - s.at) < Math.abs(out[best]!.at - s.at)) best = i;
    });
    const p = out[best];
    if (p && p.values.plan !== null) p.values.spike = (p.values.plan as number) * (1 + s.size);
  }
  return out;
}

/** TargetPreview is a draft's plan from market-sim before it is asked for: whether it is reachable in time, the move, the envelope. */
export function TargetPreview({ draft, startsAt, target }: { draft: TargetDraft; startsAt: string; target: number }) {
  const { t } = useTranslation();
  const at = useAdminT();
  const time = useTimeText();
  // The preview depends on the level, the window, the side and the start;
  // the spikes are only drawn on it.
  const c = tryCheck({ ...draft, spikes: [] }, at);
  const asked = useDebounced(
    c ? JSON.stringify({ price: c.level, duration_seconds: c.window, ...(draft.direction !== "AUTO" ? { direction: draft.direction } : {}), ...(startsAt ? { starts_at: startsAt } : {}) }) : "",
    400,
  );
  const q = useQuery({
    queryKey: [...simKey, "target-preview", asked],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/sim/target-preview", { params: { query: JSON.parse(asked) as { price: string; duration_seconds: number } } })),
    enabled: asked !== "",
    staleTime: 10_000,
  });
  const lines = useChartLines();
  const spikes = JSON.stringify(tryCheck(draft, at)?.spikes ?? []);
  const level = c?.level;
  const points = useMemo(() => {
    if (!q.data || !level) return [];
    const start = q.data.points.length ? Date.parse(q.data.points[0]!.at) : Date.now();
    const marks = (JSON.parse(spikes) as Spike[]).map((s) => ({ at: start + s.offset * 1000, size: s.size }));
    return chartPoints(q.data.points, Number(level), marks, (v) => time(v, "time"));
  }, [q.data, level, spikes, time]);
  if (!c) return <p className="text-xs text-fg-3">{t("admin.simTarget.previewWaits")}</p>;
  if (q.isPending) return <Skeleton className="h-48 w-full" />;
  if (q.isError) return <p className="text-sm text-warn">{t("admin.simTarget.previewUnknown")}</p>;
  const pv = q.data;
  const side = pv.direction ?? (draft.direction === "AUTO" ? null : draft.direction);
  return (
    <div className="flex flex-col gap-2 rounded-2 border border-line-1 bg-bg-2 p-3" data-testid="sim-target-preview">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge tone="info">{t("admin.simTarget.preview")}</Badge>
        {pv.feasible ? (
          <Badge tone="success">{t("admin.simTarget.feasible")}</Badge>
        ) : (
          <Badge tone="danger">{t("admin.simTarget.infeasible", { min: minutes(pv.min_duration_seconds) })}</Badge>
        )}
        <span className="text-fg-2">
          {side === "ABOVE" || side === "BELOW" ? t(`admin.simTarget.side.${side}`) : t("admin.simTarget.level")}{" "}
          <span className="font-mono text-fg-1">{price(c.level)}</span>
        </span>
        {target > 0 && (
          <span className="text-fg-2">
            {t("admin.simTarget.fromNow")} <span className="font-mono text-fg-1">{price(target)}</span>{" "}
            <span className="font-mono">({pct(Number(c.level) / target - 1)})</span>
          </span>
        )}
        {pv.needs_approval && <Badge tone="warn">{t("admin.simTarget.needsApproval")}</Badge>}
      </div>
      {pv.feasible && <PriceLines data={points} lines={lines} height={200} format={(v) => v.toFixed(4)} aria-label={t("admin.simTarget.preview")} />}
      <p className="text-xs text-fg-3">{t("admin.simTarget.previewHint")}</p>
    </div>
  );
}

const resultTone = { HIT: "success", MISSED: "danger", CANCELED: "warn" } as const;

/** TargetResult is how a target ended: hit, missed, canceled. */
export function TargetResult({ result }: { result: string | null | undefined }) {
  const { t } = useTranslation();
  if (result !== "HIT" && result !== "MISSED" && result !== "CANCELED") return null;
  return <Badge tone={resultTone[result]}>{t(`admin.simTarget.result.${result}`)}</Badge>;
}

/** usePlan reads a target's plan, again every few seconds while it runs. */
export function usePlan(id: string, live: boolean) {
  return useQuery({
    queryKey: [...simKey, "plan", id],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim/events/{id}/plan", { params: { path: { id } } })),
    refetchInterval: live ? 5_000 : false,
  });
}

/** TargetNow says where a running target is against its plan: the deviation and whether it may miss. */
export function TargetNow({ plan }: { plan: SimPlan }) {
  const { t } = useTranslation();
  const now = plan.now;
  if (!now) return null;
  return (
    <span className="inline-flex flex-wrap items-center gap-2" data-testid="sim-target-now">
      {now.deviation !== null && now.deviation !== undefined && (
        <span className="font-mono">{t("admin.simTarget.deviation", { d: pct(Math.exp(now.deviation) - 1) })}</span>
      )}
      {now.crossed_at ? (
        <Badge tone="success">{t("admin.simTarget.crossedShort")}</Badge>
      ) : now.at_risk ? (
        <Badge tone="danger" dot>{t("admin.simTarget.atRisk")}</Badge>
      ) : (
        <Badge tone="success" dot>{t("admin.simTarget.onTrack")}</Badge>
      )}
    </span>
  );
}

/** TargetNowOf reads a running target's plan and says where it is against it. */
export function TargetNowOf({ id }: { id: string }) {
  const q = usePlan(id, true);
  return q.data ? <TargetNow plan={q.data} /> : null;
}

/**
 * TargetPlan is a target's plan beside where the price went: the envelope
 * minute by minute, its spikes, the target price and the last price over
 * the window (from the simulated market's history), and its times; once
 * it ended (no "now" in the plan), its crossing and result are event's.
 */
export function TargetPlan({ id, live, height = 220, event }: { id: string; live: boolean; height?: number; event?: SimEvent }) {
  const { t } = useTranslation();
  const time = useTimeText();
  const q = usePlan(id, live);
  const plan = q.data;
  const startMs = plan?.starts_at ? Date.parse(plan.starts_at) : null;
  const span = startMs ? Math.min(1440, Math.max(1, Math.ceil((Date.now() - startMs) / 60_000) + 1)) : 0;
  const history = useQuery({
    queryKey: [...simKey, "history", span],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim/history", { params: { query: { minutes: span } } })).items,
    enabled: span > 0 && startMs !== null && startMs <= Date.now(),
    refetchInterval: live ? 15_000 : false,
  });
  const extra = useMemo<PriceLine[]>(
    () => [
      { key: "target", label: t("admin.sim.target"), className: "stroke-chart-1", dotClassName: "bg-chart-1" },
      { key: "last", label: t("admin.sim.last"), className: "stroke-chart-2", dotClassName: "bg-chart-2" },
    ],
    [t],
  );
  const lines = useChartLines(extra);
  const points = useMemo(() => {
    if (!plan) return [];
    const samples = (history.data ?? []).map((s) => ({ at: Date.parse(s.at), target: Number(s.target_price), last: s.last_price === null ? null : Number(s.last_price) }));
    const now = Date.now();
    const at = (ms: number) => {
      if (ms > now) return { target: null, last: null };
      let pick: (typeof samples)[number] | undefined;
      for (const s of samples) {
        if (s.at > ms) break;
        pick = s;
      }
      return pick ? { target: pick.target, last: pick.last } : { target: null, last: null };
    };
    const spikes = (plan.spikes ?? []).map((s) => ({ at: Date.parse(s.started_at ?? s.starts_at), size: s.size }));
    return chartPoints(plan.points, Number(plan.level), spikes, (v) => time(v, "time"), at);
  }, [plan, history.data, time]);
  if (q.isPending) return <Skeleton className="w-full" style={{ height }} />;
  if (q.isError || !plan) return <p className="text-sm text-warn">{t("admin.simTarget.planUnknown")}</p>;
  const crossedAt = plan.now?.crossed_at ?? event?.crossed_at;
  return (
    <div className="flex flex-col gap-2" data-testid="sim-target-plan">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fg-2">
        {plan.direction && (
          <span>
            {t(`admin.simTarget.side.${plan.direction}`)} <span className="font-mono text-fg-1">{price(plan.level)}</span>
          </span>
        )}
        {plan.from_price && (
          <span>
            {t("admin.sim.fromPrice")} <span className="font-mono text-fg-1">{price(plan.from_price)}</span>
          </span>
        )}
        {plan.now?.plan && (
          <span>
            {t("admin.simTarget.planNow")} <span className="font-mono text-fg-1">{price(plan.now.plan)}</span>
          </span>
        )}
        {plan.now && <TargetNow plan={plan} />}
        {plan.closing_at && <span>{t("admin.simTarget.closing")} <TimeText value={plan.closing_at} style="timeSeconds" /></span>}
        {plan.ends_at && <span>{t("admin.simTarget.endsAt")} <TimeText value={plan.ends_at} style="timeSeconds" /></span>}
        {plan.hold_until && <span>{t("admin.simTarget.holdUntil")} <TimeText value={plan.hold_until} style="timeSeconds" /></span>}
        {crossedAt && <span>{t("admin.simTarget.crossed")} <TimeText value={crossedAt} style="timeSeconds" /></span>}
        <TargetResult result={plan.now?.result || event?.result} />
      </div>
      <PriceLines data={points} lines={lines} height={height} format={(v) => v.toFixed(4)} aria-label={t("admin.simTarget.plan")} />
    </div>
  );
}
