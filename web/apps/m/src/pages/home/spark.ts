import { fetchSparkline } from "@exchange/core/markets/index";
import { useQuery } from "@tanstack/react-query";

// The 24-hour line of the mobile market rows and cards (design §7.2): 24
// hourly closes, fetched once the row is on screen (core useInView) with
// the other rows' in one request, and cached for five minutes. The core's
// useSparkline draws 7 days (the PC table); the mobile rows sit beside
// the 24-hour change badge, so their line covers the same day.

/** The key root of the 24-hour lines (pull to refresh invalidates it). */
export const daySparkRoot = ["market", "sparkline-24h"] as const;

export const daySparkKey = (symbol: string) => [...daySparkRoot, symbol] as const;

/** fetchDaySpark loads a market's closes of the last 24 hours, oldest first. */
export function fetchDaySpark(symbol: string): Promise<string[]> {
  return fetchSparkline(symbol, "24h");
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
