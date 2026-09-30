import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { marketApi, unwrap } from "../api/client";
import { useMarket } from "../market/hooks";
import type { TickerData, TradeData } from "../ws/types";

// A page's first paint should not wait for the first push of a quiet
// channel: the REST ticker and latest trades seed the market store, and
// the pushes take over (the store keeps whichever is newer).

/** useTickerSeed loads a symbol's ticker over REST into the market store. */
export function useTickerSeed(symbol: string): void {
  const market = useMarket();
  const q = useQuery({
    queryKey: ["market", "ticker", symbol],
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/ticker", { params: { path: { symbol } } })),
    enabled: symbol !== "",
    staleTime: 5_000,
  });
  useEffect(() => {
    if (q.data) market.seedTickers([q.data as TickerData]);
  }, [market, q.data]);
}

/** useTradesSeed loads a symbol's latest public trades over REST into the market store. */
export function useTradesSeed(symbol: string): void {
  const market = useMarket();
  const q = useQuery({
    queryKey: ["market", "trades", symbol],
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/trades", { params: { path: { symbol }, query: { limit: 50 } } })),
    enabled: symbol !== "",
    staleTime: 5_000,
  });
  useEffect(() => {
    if (q.data) market.seedTrades(symbol, q.data.trades as TradeData[]);
  }, [market, symbol, q.data]);
}
