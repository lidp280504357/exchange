import { useCallback, useMemo } from "react";
import { useBalances } from "../assets/hooks";
import { isDecimal, sign } from "../format/decimal";
import { useMarginAccounts } from "../margin/hooks";
import { isEmpty, type MarginAccount } from "../margin/math";
import { useContractOpenOrders, usePositions } from "../trading/derivatives";
import { useOpenOrders } from "../trading/orders";
import { isOpen, type OpenProducts, type ProductLine } from "./products";

// What the closed product lines still hold of the user's (design
// 2026-10-07, product line switches §1 #2), read for the assets page's
// notice and the wind-down page only: a page with every line open mounts
// none of this, and asks for nothing more.

/**
 * useWindDown reads the positions, active orders and balances, and while
 * spot is closed the margin accounts, and keeps those of the closed lines:
 * what the user may still close, cancel, repay and move out.
 */
export function useWindDown(open: OpenProducts) {
  const positions = usePositions("");
  const orders = useContractOpenOrders("");
  const spotOrders = useOpenOrders("");
  const balances = useBalances();
  // Margin trades on the spot books: its accounts are read once spot is closed (§1 #7).
  const margin = useMarginAccounts({ enabled: !open.spot, poll: false });
  const held = useMemo(
    () =>
      windDownOf(
        {
          positions: positions.data?.positions,
          orders: orders.data?.items,
          spotOrders: spotOrders.data?.items,
          balances: balances.data?.balances,
          margin: margin.data ? [margin.data.cross, ...margin.data.isolated] : undefined,
        },
        open,
      ),
    [positions.data, orders.data, spotOrders.data, balances.data, margin.data, open],
  );
  const { refetch: p } = positions;
  const { refetch: o } = orders;
  const { refetch: s } = spotOrders;
  const { refetch: b } = balances;
  const { refetch: m } = margin;
  const spotClosed = !open.spot;
  const refetch = useCallback(() => {
    void p();
    void o();
    void s();
    void b();
    if (spotClosed) void m();
  }, [p, o, s, b, m, spotClosed]);
  return {
    ...held,
    loading: positions.isPending || orders.isPending || spotOrders.isPending || balances.isPending || (spotClosed && margin.isPending),
    error: positions.error ?? orders.error ?? spotOrders.error ?? balances.error ?? (spotClosed ? margin.error : null),
    refetch,
  };
}

/** futuresLineOf is the line a FUTURES account belongs to: USDT's to the USDT-margined contracts, a coin's to the coin-margined. */
export function futuresLineOf(asset: string): ProductLine {
  return asset === "USDT" ? "usdt_m" : "coin_m";
}

/**
 * What a closed line still holds of a user's to wind down: positions,
 * active orders, futures balances, and while spot is closed the margin
 * accounts that hold or owe anything.
 */
export type WindDown<P, O, S, B, M> = { positions: P[]; orders: O[]; spotOrders: S[]; balances: B[]; margin: M[] };

/**
 * windDownOf keeps what the closed lines still hold (design 2026-10-07,
 * product line switches §1 #2: holders may cancel, reduce and close, and
 * move their funds out): the positions and active contract orders of
 * closed contracts, the active spot orders while spot is closed, the
 * futures accounts' balances of the closed lines, and while spot is closed
 * the margin accounts that are not empty: margin trades on the spot books,
 * so it closes with spot (§1 #7), and their holders repay and move out on
 * the margin page.
 */
export function windDownOf<
  P extends { symbol: string },
  O extends { symbol: string },
  S extends { symbol: string },
  B extends { account_type: string; asset: string; total: string },
  M extends Pick<MarginAccount, "balances">,
>(
  x: { positions?: readonly P[]; orders?: readonly O[]; spotOrders?: readonly S[]; balances?: readonly B[]; margin?: readonly M[] },
  open: OpenProducts,
): WindDown<P, O, S, B, M> {
  return {
    positions: (x.positions ?? []).filter((p) => !isOpen(p.symbol, open)),
    orders: (x.orders ?? []).filter((o) => !isOpen(o.symbol, open)),
    spotOrders: open.spot ? [] : [...(x.spotOrders ?? [])],
    balances: (x.balances ?? []).filter(
      (b) => b.account_type === "FUTURES" && !open[futuresLineOf(b.asset)] && isDecimal(b.total) && sign(b.total) > 0,
    ),
    margin: open.spot ? [] : (x.margin ?? []).filter((a) => !isEmpty(a)),
  };
}

/** hasWindDown is whether anything is left to wind down. */
export function hasWindDown(w: WindDown<unknown, unknown, unknown, unknown, unknown>): boolean {
  return w.positions.length + w.orders.length + w.spotOrders.length + w.balances.length + w.margin.length > 0;
}
