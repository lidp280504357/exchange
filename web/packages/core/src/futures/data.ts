import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { marketApi, unwrap } from "../api/client";
import { ApiError, retryServerErrors } from "../api/errors";
import type { components } from "../api/gen/market";
import { useChannel, useWs } from "../market/hooks";
import { onReconnected } from "../query/private";
import type { MarketPush } from "../ws/types";
import { LIQUIDATIONS_KEPT, mergeLiquidations } from "./liquidations";

// The futures data of the contracts (design 2026-10-06 §3.3, batch F):
// the reference market's statistics of a contract (open interest, the
// long and short shares, the takers' volume, the basis, the settled
// funding rates) from GET /v1/market/{symbol}/futures-data, every
// contract's figures from GET /v1/market/futures/overview, and its
// liquidations from GET /v1/market/{symbol}/liquidations with the
// channel liquidations:{symbol} pushed over them. All public. A contract
// the reference market does not trade (the platform coin's: no
// reference_symbol) has none of it; the pages ask nothing for it.

export type FuturesDataPoint = components["schemas"]["FuturesDataPoint"];
export type FuturesOverviewItem = components["schemas"]["FuturesOverviewItem"];
export type Liquidation = components["schemas"]["Liquidation"];
export type ContractSpec = components["schemas"]["Contract"];

/** The statistics, in the order the panels show them. */
export const FUTURES_METRICS = [
  "open_interest",
  "top_long_short_account",
  "top_long_short_position",
  "long_short_account",
  "taker_ratio",
  "basis",
  "funding",
] as const;
export type FuturesMetric = (typeof FUTURES_METRICS)[number];

/** The spacings a series comes in (funding has one point per settlement instead). */
export const FUTURES_PERIODS = ["5m", "15m", "1h", "4h", "1d"] as const;
export type FuturesPeriod = (typeof FUTURES_PERIODS)[number];

/** isFuturesPeriod reads a period from an address parameter. */
export function isFuturesPeriod(v: string | null | undefined): v is FuturesPeriod {
  return (FUTURES_PERIODS as readonly string[]).includes(v ?? "");
}

/** Points a chart shows: as many as the reference market's own pages (30 days of 1d). */
export const FUTURES_POINTS = 30;

/** How long after a period ends its point is in: the service reads the reference market about 70 seconds after (G3b). */
export const POINT_DELAY = 90_000;
/** A settled funding rate takes longer: the service waits up to two minutes for the reference market's. */
export const FUNDING_DELAY = POINT_DELAY + 30_000;

const MINUTE = 60_000;
const PERIOD_MS: Record<FuturesPeriod, number> = { "5m": 5 * MINUTE, "15m": 15 * MINUTE, "1h": 60 * MINUTE, "4h": 240 * MINUTE, "1d": 1440 * MINUTE };

// wait is how long after the latest point the next one is in: a period
// (two for the takers' volume, stamped with the start of the period it
// was traded in; the funding interval for funding) and the service's delay.
function wait(metric: FuturesMetric, period: FuturesPeriod, fundingHours: number): number {
  if (metric === "funding") return fundingHours * 60 * MINUTE + FUNDING_DELAY;
  return (metric === "taker_ratio" ? 2 : 1) * PERIOD_MS[period] + POINT_DELAY;
}

/**
 * nextRead is how long until a shown statistic is read again (ms): until
 * its next point is due (wait), then every minute while it is late (the
 * service reads the coarser periods up to half an hour after they end);
 * without a point, in five minutes. A latest point ahead of this clock (a
 * slow clock) waits no longer than a whole wait.
 */
export function nextRead(metric: FuturesMetric, period: FuturesPeriod, latest: number | undefined, now: number, fundingHours = 8): number {
  if (latest === undefined || !Number.isFinite(latest)) return 5 * MINUTE;
  const w = wait(metric, period, fundingHours);
  const due = latest + w;
  return due > now ? Math.min(w, Math.max(MINUTE / 2, due - now)) : MINUTE;
}

/** isDue reports whether a statistic's next point should be in by now (a tab coming back reads it again then). */
export function isDue(metric: FuturesMetric, period: FuturesPeriod, latest: number | undefined, now: number, fundingHours = 8): boolean {
  return latest === undefined || !Number.isFinite(latest) || now >= latest + wait(metric, period, fundingHours);
}

/** How often the overview is read again while a list shows it. */
export const FUTURES_OVERVIEW_EVERY = 30_000;

export const futuresKeys = {
  overview: ["market", "futures", "overview"] as const,
  data: (symbol: string, metric: FuturesMetric, period: string, limit: number) => ["market", "futures", "data", symbol, metric, period, limit] as const,
  liquidations: (symbol: string) => ["market", "futures", "liquidations", symbol] as const,
};

/** The public channel of a contract's liquidations. */
export const liquidationsChannel = (symbol: string) => `liquidations:${symbol}`;

/**
 * noFuturesData reports the answer for a contract the reference market
 * does not trade (404 MARKET_NO_FUTURES_DATA): the panels say there is no
 * data rather than that loading failed.
 */
export function noFuturesData(err: unknown): boolean {
  return err instanceof ApiError && err.code === "MARKET_NO_FUTURES_DATA";
}

/** hasFuturesData: whether a contract has the reference market's statistics (it follows one). */
export function hasFuturesData(c: Pick<ContractSpec, "reference_symbol"> | undefined): boolean {
  return Boolean(c?.reference_symbol);
}

/** useFuturesOverview follows every contract's mark, funding, open interest and day, read again every 30 seconds. */
export function useFuturesOverview({ enabled = true, every = FUTURES_OVERVIEW_EVERY }: { enabled?: boolean; every?: number | false } = {}) {
  return useQuery({
    queryKey: futuresKeys.overview,
    queryFn: () => unwrap(marketApi.GET("/v1/market/futures/overview")),
    enabled,
    staleTime: 10_000,
    refetchInterval: enabled ? every : false,
    retry: retryServerErrors,
  });
}

export type FuturesDataOptions = {
  /** Points (at most 500). */
  limit?: number;
  /** The contract's funding interval (hours), when the next settled rate is due. */
  fundingHours?: number;
  /** Off: nothing is read (a hidden panel, a contract without data). */
  enabled?: boolean;
};

/**
 * useFuturesData reads one statistic of a contract, oldest point first,
 * and again when its next point is due while enabled (nextRead). Changing
 * the period keeps the points of the one before on screen until the new
 * ones arrive (the panels dim them), so the cards keep their size;
 * another contract or statistic starts empty.
 */
export function useFuturesData(
  symbol: string,
  metric: FuturesMetric,
  period: FuturesPeriod,
  { limit = FUTURES_POINTS, enabled = true, fundingHours = 8 }: FuturesDataOptions = {},
) {
  // Funding has a point per settlement and takes no period.
  const p = metric === "funding" ? "" : period;
  const on = enabled && symbol !== "";
  return useQuery({
    queryKey: futuresKeys.data(symbol, metric, p, limit),
    queryFn: () =>
      unwrap(marketApi.GET("/v1/market/{symbol}/futures-data", { params: { path: { symbol }, query: { metric, period: p || undefined, limit } } })),
    enabled: on,
    staleTime: 30_000,
    refetchInterval: on ? (query) => nextRead(metric, period, latestOf(query.state.data), Date.now(), fundingHours) : false,
    // Polling stops while the tab is hidden: back in front, a due point is read at once.
    refetchOnWindowFocus: (query) => isDue(metric, period, latestOf(query.state.data), Date.now(), fundingHours),
    retry: retryServerErrors,
    placeholderData: (previous, query) => (query?.queryKey[3] === symbol && query.queryKey[4] === metric ? previous : undefined),
  });
}

// latestOf is the time of a response's latest point (NaN without one).
function latestOf(data: { points: readonly FuturesDataPoint[] } | undefined): number {
  return Date.parse(data?.points.at(-1)?.time ?? "");
}

/**
 * useLiquidations follows a contract's recent liquidations, newest first:
 * the REST list, then liquidations:{symbol} pushed on top of it (kept to
 * LIQUIDATIONS_KEPT). Pushes are not replayed, so the list reloads after
 * the connection comes back.
 */
export function useLiquidations(symbol: string, { enabled = true }: { enabled?: boolean } = {}) {
  const qc = useQueryClient();
  const ws = useWs();
  const on = enabled && symbol !== "";
  const key = futuresKeys.liquidations(symbol);
  const q = useQuery({
    queryKey: key,
    queryFn: async () => {
      const r = await unwrap(marketApi.GET("/v1/market/{symbol}/liquidations", { params: { path: { symbol }, query: { limit: LIQUIDATIONS_KEPT } } }));
      // A push that came while the list loaded stays.
      return mergeLiquidations(qc.getQueryData<Liquidation[]>(key) ?? [], r.liquidations);
    },
    enabled: on,
    staleTime: 60_000,
    retry: retryServerErrors,
  });
  useChannel<Liquidation>(on ? liquidationsChannel(symbol) : null, (m) => {
    const d = (m as MarketPush<Liquidation>).data;
    if (d?.symbol !== symbol) return;
    qc.setQueryData<Liquidation[]>(key, (cur) => mergeLiquidations(cur ?? [], [d]));
  });
  useEffect(() => (on ? onReconnected(ws, () => void qc.invalidateQueries({ queryKey: key })) : undefined), [ws, qc, on, symbol]);
  return q;
}
