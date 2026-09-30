import { useInfiniteQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { marketApi, unwrap } from "../api/client";
import { useChannel } from "../market/hooks";
import { qk } from "../query/keys";
import { channels, type CandleData, type MarketPush } from "../ws/types";

// A chart's candles (design §6.2): the latest page over REST, older pages
// on demand (the chart asks when the view nears its left edge), and the
// open candle live from candles:{symbol}:{interval}. Pages stay in the
// query cache, so coming back to a pair draws at once.

export type CandleInterval = "1m" | "3m" | "5m" | "15m" | "30m" | "1h" | "2h" | "4h" | "6h" | "12h" | "1d" | "1w" | "1M";

type Page = { candles: CandleData[] };

/**
 * mergeLive puts a live candle into the latest page: the same open time
 * replaces the last candle, a later one is appended, an older one ignored.
 */
export function mergeLive(page: Page, c: CandleData): Page {
  const list = page.candles;
  const last = list[list.length - 1];
  if (!last) return { candles: [c] };
  const at = Date.parse(c.open_time);
  const lastAt = Date.parse(last.open_time);
  if (at === lastAt) return { candles: [...list.slice(0, -1), c] };
  if (at > lastAt) return { candles: [...list, c] };
  return page;
}

/** flatten joins the pages (latest first) into one series, oldest first. */
export function flatten(pages: Page[]): CandleData[] {
  const out: CandleData[] = [];
  for (let i = pages.length - 1; i >= 0; i--) out.push(...pages[i]!.candles);
  return out;
}

/**
 * useCandles returns a pair's candles (oldest first), whether older ones
 * remain, and loadMore for the chart's onLoadMore.
 */
export function useCandles(symbol: string, interval: CandleInterval, pageSize = 300) {
  const qc = useQueryClient();
  const key = qk.candles(symbol, interval);
  const q = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) =>
      unwrap(
        marketApi.GET("/v1/market/{symbol}/candles", {
          params: { path: { symbol }, query: { interval, limit: pageSize, to: pageParam || undefined } },
        }),
      ).then((r): Page => ({ candles: r.candles })),
    initialPageParam: "",
    // Older pages: the oldest open time received is the next `to`.
    getNextPageParam: (last) => (last.candles.length < pageSize ? undefined : last.candles[0]?.open_time),
    staleTime: 60_000,
    enabled: symbol !== "",
  });

  useChannel<CandleData>(symbol ? channels.candles(symbol, interval) : null, (m) => {
    const c = (m as MarketPush<CandleData>).data;
    qc.setQueryData<InfiniteData<Page, string>>(key, (data) => {
      if (!data || data.pages.length === 0) return data;
      const pages = [...data.pages];
      pages[0] = mergeLive(pages[0]!, c);
      return { ...data, pages };
    });
  });

  const candles = useMemo(() => flatten(q.data?.pages ?? []), [q.data]);
  const { fetchNextPage, hasNextPage, isFetchingNextPage } = q;
  const loadMore = useCallback(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [fetchNextPage, hasNextPage, isFetchingNextPage]);

  return { candles, hasMore: Boolean(hasNextPage), loading: q.isPending, error: q.error, loadMore, refetch: q.refetch };
}
