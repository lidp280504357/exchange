import { useCallback, useMemo } from "react";
import { coinProfile } from "../coins";
import { buildRows, type MarketRow } from "../markets/list";
import type { MarketRows } from "../markets/hooks";
import { useContracts, usePairs } from "../trading/pairs";
import { useFuturesOverview, type ContractSpec, type FuturesOverviewItem } from "./data";
import { groupOf, overviewRows, type MarginGroup, type OverviewOf, type OverviewRow } from "./list";

// Hooks over ./list for the pages: the market rows with the contracts the
// futures terminal opens (both margin types), and the overview's rows.

/**
 * useOpenContracts lists the contracts the futures terminal opens: its own
 * list (useContracts: both margin types, none still PREPARE), so a list
 * never links to a contract the terminal does not know.
 */
export function useOpenContracts(): { contracts: ContractSpec[]; loading: boolean; error: unknown; refetch: () => void } {
  const terminal = useContracts();
  const contracts = useMemo(() => terminal.data?.contracts ?? [], [terminal.data]);
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
 * each contract's group (USDⓈ-M or COIN-M).
 */
export function useFuturesMarketRows(): FuturesMarketRows {
  const pairs = usePairs();
  const open = useOpenContracts();
  const rows = useMemo<MarketRow[]>(() => buildRows(pairs.data?.pairs ?? [], open.contracts), [pairs.data, open.contracts]);
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
    loading: pairs.isPending,
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
