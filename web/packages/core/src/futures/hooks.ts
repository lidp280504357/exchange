import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { coinProfile } from "../coins";
import { buildRows, type MarketRow } from "../markets/list";
import type { MarketRows } from "../markets/hooks";
import { isOpen, useOpenProducts } from "../platform/products";
import { qk } from "../query/keys";
import { fetchContracts, usePairs } from "../trading/pairs";
import { useFuturesOverview, type ContractSpec, type FuturesOverviewItem } from "./data";
import { groupOf, overviewRows, type MarginGroup, type OverviewOf, type OverviewRow } from "./list";

// Hooks over ./list for the pages: the market rows with the contracts the
// futures terminal opens (both margin types), and the overview's rows.

/** How often the contracts are read again while a list shows them: contracts open in batches. */
export const CONTRACTS_EVERY = 60_000;

/**
 * useOpenContracts lists the contracts the futures terminal opens: its own
 * list (useContracts' query: both margin types, none still PREPARE), so a
 * list never links to a contract the terminal does not know; read again
 * every minute while a list shows them. A closed product line's contracts
 * are left out (design 2026-10-07, product line switches §1 #2).
 */
export function useOpenContracts(): { contracts: ContractSpec[]; loading: boolean; error: unknown; refetch: () => void } {
  const terminal = useQuery({ queryKey: qk.contracts, queryFn: fetchContracts, staleTime: 60_000, refetchInterval: CONTRACTS_EVERY });
  const products = useOpenProducts();
  const contracts = useMemo(() => (terminal.data?.contracts ?? []).filter((c) => isOpen(c.symbol, products)), [terminal.data, products]);
  const { refetch: refetchTerminal } = terminal;
  const refetch = useCallback(() => void refetchTerminal(), [refetchTerminal]);
  return { contracts, loading: terminal.isPending, error: terminal.error, refetch };
}

export type FuturesMarketRows = MarketRows & {
  /** The group of each contract row. */
  groupOf: (symbol: string) => MarginGroup;
};

/**
 * useFuturesMarketRows is the market list's rows (useMarketRows) with
 * each contract's group (USDⓈ-M or COIN-M). It loads until both the pairs
 * and the contracts are in: a table that got its contracts later would
 * switch from scrolling with the page to its own box (200 rows) mid-way.
 */
export function useFuturesMarketRows(): FuturesMarketRows {
  const pairs = usePairs();
  const open = useOpenContracts();
  // A closed spot line takes its pairs off the list (its contracts already are).
  const spot = useOpenProducts().spot;
  const rows = useMemo<MarketRow[]>(() => buildRows(spot ? (pairs.data?.pairs ?? []) : [], open.contracts), [pairs.data, open.contracts, spot]);
  const groups = useMemo(() => new Map(open.contracts.map((c) => [c.symbol, groupOf(c)])), [open.contracts]);
  const { refetch: refetchPairs } = pairs;
  const { refetch: refetchContracts } = open;
  const refetch = useCallback(() => {
    void refetchPairs();
    refetchContracts();
  }, [refetchPairs, refetchContracts]);
  const group = useCallback((symbol: string) => groups.get(symbol) ?? "usdt", [groups]);
  return {
    rows,
    loading: pairs.isPending || open.loading,
    error: pairs.error ?? (rows.length === 0 ? open.error : null),
    refetch,
    groupOf: group,
  };
}

/** useOverviewOf follows the overview (while enabled) and looks its items up by symbol. */
export function useOverviewOf(enabled = true): OverviewOf {
  const q = useFuturesOverview({ enabled });
  const map = useMemo(() => new Map((q.data?.contracts ?? []).map((c) => [c.symbol, c])), [q.data]);
  return useCallback((symbol: string): FuturesOverviewItem | undefined => map.get(symbol), [map]);
}

function coinNames(base: string): string[] {
  const p = coinProfile(base);
  return p ? [p.name["zh-CN"], p.name["zh-TW"], p.name.en] : [];
}

/** useOverviewRows is the futures data overview's rows: every contract the terminal opens, with its figures. */
export function useOverviewRows(): { rows: OverviewRow[]; loading: boolean; error: unknown; refetch: () => void; updatedAt: number } {
  const overview = useFuturesOverview();
  const open = useOpenContracts();
  const rows = useMemo(() => overviewRows(overview.data?.contracts ?? [], open.contracts, coinNames), [overview.data, open.contracts]);
  const { refetch: refetchOverview } = overview;
  const { refetch: refetchContracts } = open;
  const refetch = useCallback(() => {
    void refetchOverview();
    refetchContracts();
  }, [refetchOverview, refetchContracts]);
  return {
    rows,
    loading: overview.isPending || (open.loading && open.contracts.length === 0),
    error: overview.error ?? (rows.length === 0 ? open.error : null),
    refetch,
    updatedAt: overview.dataUpdatedAt,
  };
}
