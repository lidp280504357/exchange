// The pair pickers' and the ⌘K search's matching: the coin's own code
// first, so that "ETH" finds ETH before ENA ("Ethena"), ENS and ETC whose
// names mention Ethereum (the checklist flows, P4).

/** A market as a search reads it. */
export type Searchable = { base: string; symbol: string; name: string };

/**
 * matchRank says how well a market matches an upper-case query: 0 its
 * coin's code itself, 1 a code that starts with it, 2 a symbol that
 * contains it, 3 a name that does; -1 no match.
 */
export function matchRank(q: string, m: Searchable): number {
  if (m.base === q) return 0;
  if (m.base.startsWith(q)) return 1;
  if (m.symbol.includes(q)) return 2;
  if (m.name.toUpperCase().includes(q)) return 3;
  return -1;
}

/** mainFirst puts a coin's main market, its USDT pair, before its others ("ETH" and Enter open ETH-USDT). */
const mainFirst = (m: Searchable) => (m.symbol === `${m.base}-USDT` ? 0 : 1);

/**
 * searchMarkets keeps the markets a query finds, the best matches first;
 * within a rank the coins' USDT pairs first, then the list's order.
 */
export function searchMarkets<T extends Searchable>(rows: readonly T[], query: string): T[] {
  const q = query.trim().toUpperCase();
  if (!q) return [...rows];
  return rows
    .map((r, i) => ({ r, i, rank: matchRank(q, r) }))
    .filter((x) => x.rank >= 0)
    .sort((a, b) => a.rank - b.rank || mainFirst(a.r) - mainFirst(b.r) || a.i - b.i)
    .map((x) => x.r);
}
