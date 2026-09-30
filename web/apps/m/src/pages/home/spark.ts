import { marketApi, unwrap } from "@exchange/core";
import { createLimiter, sparkValues } from "@exchange/core/markets/index";
import { useQuery } from "@tanstack/react-query";

// The 24-hour line of the mobile market rows and cards (design §7.2): 24
// hourly closes, fetched once the row is on screen (core useInView)
// through a small queue and cached for five minutes. The core's
// useSparkline draws 7 days (the PC table); the mobile rows sit beside
// the 24-hour change badge, so their line covers the same day.

export const DAY_SPARK_INTERVAL = "1h";
export const DAY_SPARK_CANDLES = 24;

/** The key root of the 24-hour lines (pull to refresh invalidates it). */
export const daySparkRoot = ["market", "sparkline-24h"] as const;

export const daySparkKey = (symbol: string) => [...daySparkRoot, symbol] as const;

// A page of fifty rows must not fire fifty requests at once.
const queue = createLimiter(6);

/** fetchDaySpark loads a market's closes of the last 24 hours, oldest first. */
export function fetchDaySpark(symbol: string): Promise<string[]> {
  return queue(() =>
    unwrap(
      marketApi.GET("/v1/market/{symbol}/candles", {
        params: { path: { symbol }, query: { interval: DAY_SPARK_INTERVAL, limit: DAY_SPARK_CANDLES } },
      }),
    ),
  ).then((r) => sparkValues(r.candles));
}

/** useDaySpark returns a market's 24-hour closes once `enabled` (its row is in view). */
export function useDaySpark(symbol: string, enabled = true) {
  return useQuery({
    queryKey: daySparkKey(symbol),
    queryFn: () => fetchDaySpark(symbol),
    enabled: enabled && symbol !== "",
    staleTime: 5 * 60_000,
    gcTime: 30 * 60_000,
    retry: 1,
  });
}
