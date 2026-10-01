import { useQuery } from "@tanstack/react-query";
import { marketApi, unwrap } from "../api/client";
import { qk } from "../query/keys";
import type { CandleData } from "../ws/types";

// The trend lines of the market list rows (design §6.2, §7.2): the PC
// table draws 7 days, the mobile rows 24 hours, from hourly closes. A row
// asks once it is on screen (see useInView); the rows that ask within a
// few milliseconds of each other share one request (GET
// /v1/market/sparklines, 60 symbols at most), so a page of rows costs one
// round trip; the server keeps each pair's closes for five minutes.

/** Points drawn: one per 3 hours is plenty for a 100 px line. */
export const SPARK_POINTS = 56;

/**
 * sparkValues takes the closes of candles (oldest first), thinned to at
 * most maxPoints evenly spaced ones, the first and the last kept.
 */
export function sparkValues(candles: readonly Pick<CandleData, "close">[], maxPoints = SPARK_POINTS): string[] {
  const closes = candles.map((c) => c.close);
  if (closes.length <= maxPoints || maxPoints < 2) return closes;
  const out: string[] = [];
  const step = (closes.length - 1) / (maxPoints - 1);
  for (let i = 0; i < maxPoints; i++) out.push(closes[Math.round(i * step)]!);
  return out;
}

/** createLimiter runs at most `max` tasks at a time, the rest in order. */
export function createLimiter(max: number): <T>(task: () => Promise<T>) => Promise<T> {
  let active = 0;
  const waiting: (() => void)[] = [];
  const next = () => {
    active--;
    waiting.shift()?.();
  };
  return <T>(task: () => Promise<T>) =>
    new Promise<T>((resolve, reject) => {
      const start = () => {
        active++;
        task().then(resolve, reject).finally(next);
      };
      if (active < max) start();
      else waiting.push(start);
    });
}

/** A line's span: the last 7 days (56 points) or the last 24 hours. */
export type SparkRange = "7d" | "24h";

type Waiter = { resolve: (closes: string[]) => void; reject: (err: unknown) => void };

const BATCH = 60;
const waiting: Record<SparkRange, Map<string, Waiter[]>> = { "7d": new Map(), "24h": new Map() };
const timers: Partial<Record<SparkRange, ReturnType<typeof setTimeout>>> = {};

/** fetchSparkline loads a market's line over range, oldest first ([] when it has none). */
export function fetchSparkline(symbol: string, range: SparkRange = "7d"): Promise<string[]> {
  return new Promise((resolve, reject) => {
    const queue = waiting[range];
    queue.set(symbol, [...(queue.get(symbol) ?? []), { resolve, reject }]);
    timers[range] ??= setTimeout(() => void flush(range), 10);
  });
}

async function flush(range: SparkRange): Promise<void> {
  delete timers[range];
  const batch = waiting[range];
  waiting[range] = new Map();
  const symbols = [...batch.keys()];
  for (let i = 0; i < symbols.length; i += BATCH) {
    const chunk = symbols.slice(i, i + BATCH);
    try {
      const r = await unwrap(marketApi.GET("/v1/market/sparklines", { params: { query: { symbols: chunk.join(","), range } } }));
      for (const s of chunk) for (const w of batch.get(s) ?? []) w.resolve(sparkValues((r.sparklines[s] ?? []).map((close) => ({ close }))));
    } catch (err) {
      for (const s of chunk) for (const w of batch.get(s) ?? []) w.reject(err);
    }
  }
}

/** useSparkline returns a market's 7-day closes once `enabled` (the row is in view). */
export function useSparkline(symbol: string, enabled = true) {
  return useQuery({
    queryKey: qk.sparkline(symbol),
    queryFn: () => fetchSparkline(symbol),
    enabled: enabled && symbol !== "",
    staleTime: 10 * 60_000,
    gcTime: 30 * 60_000,
    retry: 1,
  });
}
