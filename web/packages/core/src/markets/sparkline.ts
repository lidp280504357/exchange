import { useQuery } from "@tanstack/react-query";
import { marketApi, unwrap } from "../api/client";
import { qk } from "../query/keys";
import type { CandleData } from "../ws/types";

// The 7-day trend of a market list row (design §6.2): 168 hourly candles,
// fetched only once the row is on screen (see useInView), cached for ten
// minutes and shared by every list that shows the same market. A small
// queue keeps a page of fifty rows from firing fifty requests at once.

export const SPARK_INTERVAL = "1h";
export const SPARK_CANDLES = 168;
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

const limit = createLimiter(6);

/** fetchSparkline loads a market's last 7 days of hourly closes. */
export function fetchSparkline(symbol: string): Promise<string[]> {
  return limit(() =>
    unwrap(
      marketApi.GET("/v1/market/{symbol}/candles", {
        params: { path: { symbol }, query: { interval: SPARK_INTERVAL, limit: SPARK_CANDLES } },
      }),
    ),
  ).then((r) => sparkValues(r.candles));
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
