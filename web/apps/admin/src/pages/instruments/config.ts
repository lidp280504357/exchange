import { dec } from "@exchange/core";
import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { useQuery } from "@tanstack/react-query";

// The reference data as the console edits it: a config document in the
// shape of deploy/instruments/<env>.json (design 2026-10-02 §4.4, C3).
// Every change is a patch naming whole items, previewed then applied.

export type InstrumentConfig = AdminSchemas["InstrumentConfig"];
export type ConfigPatch = AdminSchemas["InstrumentConfigPatch"];
export type FeeSchedule = AdminSchemas["FeeScheduleConfig"];
export type AssetConfig = AdminSchemas["AssetConfig"];
export type NetworkConfig = AdminSchemas["NetworkConfig"];
export type PairConfig = AdminSchemas["PairConfig"];
export type ContractConfig = AdminSchemas["ContractConfig"];
export type RiskTier = AdminSchemas["RiskTier"];
export type ConfigResult = AdminSchemas["ConfigResult"];
export type ConfigChange = ConfigResult["changes"][number];

export const configKey = ["admin", "instruments", "config"] as const;

/** useInstrumentConfig reads the reference data as a config document. */
export function useInstrumentConfig() {
  return useQuery({ queryKey: configKey, queryFn: async () => adminData(await adminApi.GET("/admin/v1/instruments/config")) });
}

/** feeRates maps each fee tier to its maker/taker rates. */
export function feeRates(cfg: InstrumentConfig | undefined): Map<string, FeeSchedule> {
  return new Map((cfg?.fee_schedules ?? []).map((f) => [f.tier, f]));
}

/** withoutVersion is an item as a patch sends it: the version is the service's. */
export function withoutVersion<T extends { version?: number }>(item: T): T {
  const { version: _, ...rest } = item;
  return rest as T;
}

/** FieldDiff is one field an update changes. */
export type FieldDiff = { field: string; before: string; after: string };

const show = (v: unknown): string => (v === undefined || v === null || v === "" ? "—" : typeof v === "object" ? JSON.stringify(v) : String(v));

/**
 * fieldDiffs lists the fields an update changes (version and timestamps
 * left out), decimals compared by value: "0.10" and "0.1" are the same.
 */
export function fieldDiffs(before: Record<string, unknown> | null, after: Record<string, unknown>): FieldDiff[] {
  const out: FieldDiff[] = [];
  const keys = new Set([...Object.keys(before ?? {}), ...Object.keys(after)]);
  for (const k of keys) {
    if (k === "version" || k === "updated_at" || k === "listed_at") continue;
    const b = before?.[k];
    const a = after[k];
    if (same(b, a)) continue;
    out.push({ field: k, before: show(b), after: show(a) });
  }
  return out;
}

function same(a: unknown, b: unknown): boolean {
  if (typeof a === "string" && typeof b === "string" && dec.isDecimal(a) && dec.isDecimal(b)) return dec.eq(a, b);
  if ((a === undefined || a === "" || a === null || a === false) && (b === undefined || b === "" || b === null || b === false)) return true;
  return JSON.stringify(a) === JSON.stringify(b);
}

/** Decimal fields of the forms: required, a non-negative decimal string. */
export const isAmount = (v: string) => dec.isDecimal(v.trim()) && !v.trim().startsWith("-");

/**
 * riskTierProblems checks a contract's ladder as instrument-service does:
 * 1-20 tiers; notionals positive and rising; leverage 1-125, never
 * rising; mmr positive, below 1/leverage, never falling. Each problem is
 * "kind:index" (the row) or "tiers" (their number).
 */
export function riskTierProblems(tiers: RiskTier[]): string[] {
  const out: string[] = [];
  if (tiers.length < 1 || tiers.length > 20) out.push("tiers");
  tiers.forEach((tier, i) => {
    const notional = isAmount(tier.max_notional) && dec.gt(tier.max_notional, "0");
    const mmr = isAmount(tier.mmr) && dec.gt(tier.mmr, "0");
    if (!notional) out.push(`notional:${i}`);
    if (!Number.isInteger(tier.max_leverage) || tier.max_leverage < 1 || tier.max_leverage > 125) out.push(`leverage:${i}`);
    if (!mmr || dec.toNumber(tier.mmr) * Math.max(tier.max_leverage, 1) >= 1) out.push(`mmr:${i}`);
    const prev = tiers[i - 1];
    if (!prev) return;
    if (notional && isAmount(prev.max_notional) && dec.lte(tier.max_notional, prev.max_notional)) out.push(`notional:${i}`);
    if (tier.max_leverage > prev.max_leverage) out.push(`leverage:${i}`);
    if (mmr && isAmount(prev.mmr) && dec.lt(tier.mmr, prev.mmr)) out.push(`mmr:${i}`);
  });
  return out;
}
