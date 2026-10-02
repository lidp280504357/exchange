import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { Badge } from "@exchange/ui";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Num } from "../../kit/format";

// What the simulated market's pages share (ASTRA design §6, C5): the
// state from market-sim (asked again every few seconds), its events and
// how to say them.

export type SimStatus = AdminSchemas["SimStatus"];
export type SimEvent = AdminSchemas["SimEvent"];
export type SimBot = AdminSchemas["SimBot"];
export type SimEventType = SimEvent["type"];

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

/** useEventText says what an event does: its type and its own numbers. */
export function useEventText() {
  const { t } = useTranslation();
  return (e: Pick<SimEvent, "type" | "size" | "price" | "mu" | "factor" | "duration_seconds" | "hold_seconds">) => {
    const over = e.duration_seconds > 0 ? t("admin.sim.over", { s: e.duration_seconds }) : "";
    switch (e.type) {
      case "JUMP":
        return `${t("admin.sim.types.JUMP")} ${pct(e.size, 1)}${over || ` · ${t("admin.sim.atOnce")}`}`;
      case "TARGET":
        return `${t("admin.sim.types.TARGET")} ${price(e.price)}${over}${e.hold_seconds > 0 ? ` · ${t("admin.sim.hold", { s: e.hold_seconds })}` : ""}`;
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
