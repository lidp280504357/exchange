import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo } from "react";
import { marketApi, unwrap } from "../api/client";
import { useMarket, useTickers } from "../market/hooks";
import { isOpen, useOpenProducts } from "../platform/products";
import { qk } from "../query/keys";
import { useContracts, usePairs } from "../trading/pairs";
import type { TickerData } from "../ws/types";
import { buildRows, type MarketRow } from "./list";

// Data hooks of the market lists: every ticker through the one "tickers"
// subscription (seeded by the REST list the page preloads), and the rows
// built from the pairs and contracts.

/**
 * useMarketTickers follows every ticker: the REST list first (index.html
 * preloads it), then the tickers channel, re-rendering at most once per
 * frame. REST tickers stay as a fallback for symbols the channel has not
 * sent yet.
 */
export function useMarketTickers(): ReadonlyMap<string, TickerData> {
  const market = useMarket();
  const rest = useQuery({ queryKey: qk.tickers, queryFn: () => unwrap(marketApi.GET("/v1/market/tickers")), staleTime: 5_000 });
  useEffect(() => {
    if (rest.data) market.seedTickers(rest.data.tickers);
  }, [market, rest.data]);
  const live = useTickers();
  return useMemo(() => {
    const list = rest.data?.tickers;
    if (!list || list.every((t) => live.has(t.symbol))) return live;
    const out = new Map<string, TickerData>();
    for (const t of list) out.set(t.symbol, t);
    for (const [s, t] of live) out.set(s, t);
    return out;
  }, [live, rest.data]);
}

export type MarketRows = {
  rows: MarketRow[];
  /** True until the pairs arrive. */
  loading: boolean;
  /** The pairs' error, or the contracts' when nothing could be shown. */
  error: unknown;
  refetch: () => void;
};

/**
 * useMarketRows returns the list rows of every pair and contract of the
 * open product lines (a closed line's markets leave the home page's boards
 * and the other lists; design 2026-10-07, product line switches §1 #2).
 */
export function useMarketRows(): MarketRows {
  const pairs = usePairs();
  const contracts = useContracts();
  const products = useOpenProducts();
  const rows = useMemo(
    () =>
      buildRows(
        products.spot ? (pairs.data?.pairs ?? []) : [],
        (contracts.data?.contracts ?? []).filter((c) => isOpen(c.symbol, products)),
      ),
    [pairs.data, contracts.data, products],
  );
  const { refetch: refetchPairs } = pairs;
  const { refetch: refetchContracts } = contracts;
  const refetch = useCallback(() => {
    void refetchPairs();
    void refetchContracts();
  }, [refetchPairs, refetchContracts]);
  return {
    rows,
    loading: pairs.isPending,
    error: pairs.error ?? (rows.length === 0 ? contracts.error : null),
    refetch,
  };
}
