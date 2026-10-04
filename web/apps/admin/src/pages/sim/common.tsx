import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge, Button, Skeleton } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { DangerAction, lastFour } from "../../kit/actions";
import { Num, TimeText } from "../../kit/format";

// What the simulated market's pages share (ASTRA design §6, C5): the
// state from market-sim (asked again every few seconds), its events and
// how to say them.

export type SimStatus = AdminSchemas["SimStatus"];
export type SimEvent = AdminSchemas["SimEvent"];
export type SimBot = AdminSchemas["SimBot"];
export type SimEventType = SimEvent["type"];
export type SimImpact = AdminSchemas["SimImpact"];

/** ImpactLines says what a price would do to the perpetual's positions (ASTRA design §6.3). */
export function ImpactLines({ i }: { i: SimImpact }) {
  const { t } = useTranslation();
  return (
    <span className="flex flex-col gap-0.5" data-testid="sim-impact">
      <span>
        {i.longs === null || i.shorts === null
          ? t("admin.sim.impactOpen", { symbol: i.symbol, n: i.positions })
          : t("admin.sim.impactPositions", { symbol: i.symbol, longs: i.longs, shorts: i.shorts })}
      </span>
      <span className={i.liquidated ? "text-danger-strong" : "text-fg-2"}>
        {t("admin.sim.impactLiquidated", { n: i.liquidated, notional: i.notional, accounts: i.accounts })}
      </span>
      {Number(i.insurance_cost) > 0 && <span className="text-danger-strong">{t("admin.sim.impactShortfall", { amount: i.insurance_cost })}</span>}
      {i.unmeasured > 0 && <span className="text-warn-strong">{t("admin.sim.impactUnmeasured", { n: i.unmeasured })}</span>}
    </span>
  );
}

/**
 * SimRequestNow measures a simulated market's request for the
 * administrator deciding it (C5.5 ④): when it lapses, where it would take
 * the price now beside the move measured when it was asked for, and what
 * that would do to the perpetual.
 */
export function SimRequestNow({ id }: { id: string }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: [...simKey, "preview", id],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/approvals/{id}/sim-preview", { params: { path: { id } } })),
  });
  if (q.isPending) return <Skeleton className="h-16 w-full" />;
  if (q.isError) return <p className="text-sm text-warn-strong">{t("admin.sim.previewUnknown")}</p>;
  const pv = q.data;
  return (
    <div className="flex flex-col gap-1.5 rounded-1 border border-line-1 bg-bg-2 px-3 py-2 text-xs" data-testid="sim-request-now">
      <span className={pv.expired ? "font-medium text-danger-strong" : "text-fg-2"}>
        {t(pv.expired ? "admin.sim.lapsed" : "admin.sim.lapses")} <TimeText value={pv.expires_at} />
      </span>
      {pv.expected_price ? (
        <span>
          {t("admin.sim.nowMoves")} <span className="font-mono">{price(pv.target_price)} → {price(pv.expected_price)}</span>
          {pv.move !== null && <span className="font-mono"> ({pct(pv.move, 1)})</span>}
          {pv.requested_move && <span className="text-fg-3"> · {t("admin.sim.askedMove", { move: pct(Number(pv.requested_move), 1) })}</span>}
        </span>
      ) : (
        <span className="text-fg-3">{t("admin.sim.noDirectMove")}</span>
      )}
      {pv.impact && <ImpactLines i={pv.impact} />}
      {pv.spike_impacts?.map((s) => (
        <span key={s.price} className="flex flex-col gap-0.5 border-t border-line-1 pt-1.5" data-testid="sim-request-spike">
          <span className="text-fg-2">{t("admin.simTarget.impactSpike", { price: price(s.price) })}</span>
          <ImpactLines i={s.impact} />
        </span>
      ))}
      {!!pv.spike_impacts?.length && <span className="text-fg-3">{t("admin.simTarget.markNoteRequest")}</span>}
    </div>
  );
}

export const simKey = ["admin", "sim"];
export const simEventsKey = ["admin", "sim", "events"];

/** useSim reads the simulated market's state every few seconds. */
export function useSim(every = 3_000) {
  return useQuery({
    queryKey: simKey,
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/sim")),
    refetchInterval: every,
  });
}

/** price is a price as market-sim states it, to 4 decimal places (a tick of ASTRA-USDT). */
export const price = (v: number | string | null | undefined) =>
  v === null || v === undefined || v === "" || !Number.isFinite(Number(v)) ? "—" : Number(v).toFixed(4);

/** pct is a ratio as a signed percentage. */
export const pct = (v: number, digits = 2) => `${v > 0 ? "+" : v < 0 ? "−" : ""}${Math.abs(v * 100).toFixed(digits)}%`;

/** dailyPct is a drift a day (continuous, mu) as the price move it makes in a day. */
export const dailyPct = (mu: number) => pct(Math.exp(mu) - 1);

const statusTone = { SCHEDULED: "info", RUNNING: "brand", DONE: "neutral", CANCELED: "warn" } as const;

export function EventStatus({ e }: { e: SimEvent }) {
  const { t } = useTranslation();
  return <Badge tone={statusTone[e.status]}>{t(`admin.sim.eventStatus.${e.status}`)}</Badge>;
}

/**
 * EndEvent ends a running event or cancels a queued one, as text says it
 * (a HALT ends by resuming trading; a target ended before its crossing is
 * CANCELED, its queued spikes with it).
 */
export function EndEvent({ e, text }: { e: SimEvent; text: string }) {
  const { t } = useTranslation();
  const running = e.status === "RUNNING";
  return (
    <DangerAction
      trigger={(open) => (
        <Button size="sm" variant={running ? "danger" : "secondary"} onClick={open} data-testid={`sim-end-${e.id}`}>
          {t(running ? "admin.sim.end" : "admin.sim.cancel")}
        </Button>
      )}
      title={t(running ? "admin.sim.endTitle" : "admin.sim.cancelTitle")}
      description={e.type === "HALT" ? t("admin.sim.endHalt") : e.type === "TARGET" ? t("admin.simTarget.endTarget") : undefined}
      target={text}
      confirmWord={lastFour(e.id)}
      run={async (reason) => adminData(await adminApi.POST("/admin/v1/sim/events/{id}/end", { params: { path: { id: e.id } }, body: { reason } }))}
      success={t("admin.sim.ended")}
      invalidate={[simEventsKey, simKey]}
    />
  );
}

/**
 * MintShares says what a mint for the bots (a SIM_MINT fund operation)
 * books: the total and how many bots of which role share it; full lists
 * each bot's share.
 */
export function MintShares({ payload: p, full }: { payload: Record<string, string>; full?: boolean }) {
  const { t } = useTranslation();
  let shares: { label: string; amount: string }[] = [];
  try {
    shares = JSON.parse(p.bots ?? "[]") as typeof shares;
  } catch {
    // Shown without the shares.
  }
  const to = p.role ? t(`admin.sim.roles.${p.role}`) : t("admin.sim.allBots");
  return (
    <span className="flex flex-col gap-1">
      <span className="inline-flex flex-wrap items-center gap-2">
        <Num value={p.amount} unit={p.asset} signed />
        <span className="text-xs text-fg-3">{t("admin.sim.mintSplit", { n: shares.length, to })}</span>
      </span>
      {full && shares.length > 0 && (
        <span className="grid max-h-40 grid-cols-[auto_auto] justify-start gap-x-6 overflow-y-auto font-mono text-xs text-fg-2">
          {shares.map((s, i) => (
            <span key={i} className="contents">
              <span>{s.label}</span>
              <span className="text-right">{s.amount}</span>
            </span>
          ))}
        </span>
      )}
    </span>
  );
}

/** minutes says seconds in minutes, with a decimal when not whole. */
export const minutes = (s: number) => (s % 60 === 0 ? String(s / 60) : (s / 60).toFixed(1));

/**
 * EventFields are what an event's text reads, of an event or of a request
 * for one: a threshold target's side, what follows its crossing and its
 * spikes, a spike's width (A6).
 */
export type EventFields = Pick<SimEvent, "type" | "size" | "price" | "mu" | "factor" | "duration_seconds" | "hold_seconds"> &
  Partial<Pick<SimEvent, "direction" | "then" | "width_seconds">> & { spikes?: unknown[] | null };

/** useEventText says what an event does: its type and its own numbers. */
export function useEventText() {
  const { t } = useTranslation();
  return (e: EventFields) => {
    const over = e.duration_seconds > 0 ? t("admin.sim.over", { s: e.duration_seconds }) : "";
    switch (e.type) {
      case "JUMP":
        return `${t("admin.sim.types.JUMP")} ${pct(e.size, 1)}${over || ` · ${t("admin.sim.atOnce")}`}`;
      case "TARGET": {
        // A threshold target: its side of the level, its window in minutes, its hold and spikes.
        const side = e.direction === "ABOVE" ? "≥ " : e.direction === "BELOW" ? "≤ " : "";
        const within = e.duration_seconds > 0 ? ` · ${t("admin.simTarget.within", { m: minutes(e.duration_seconds) })}` : "";
        const held = e.hold_seconds > 0 ? ` · ${t("admin.simTarget.holdSummary", { m: minutes(e.hold_seconds) })}` : "";
        const spikes = e.spikes?.length ? ` · ${t("admin.simTarget.spikesCount", { n: e.spikes.length })}` : "";
        return `${t("admin.sim.types.TARGET")} ${side}${price(e.price)}${within}${held}${spikes}`;
      }
      case "SPIKE":
        return `${t("admin.sim.types.SPIKE")} ${pct(e.size, 1)} · ${t("admin.simTarget.wide", { s: e.width_seconds || 20 })}`;
      case "TREND":
        return `${t("admin.sim.types.TREND")} ${dailyPct(e.mu)}/${t("admin.sim.day")}${over}`;
      case "VOLATILITY":
        return `${t("admin.sim.types.VOLATILITY")} ×${e.factor}${over}`;
      case "PAUSE":
        return `${t("admin.sim.types.PAUSE")}${over || ` · ${t("admin.sim.untilEnded")}`}`;
      default:
        return t(`admin.sim.types.${e.type}`);
    }
  };
}
