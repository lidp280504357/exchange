/**
 * fitRows is how the order book fills a height (OrderBook's onRows, B74):
 * the rows a side that fit in `room` (the px the two sides share) at
 * `minRow` px at least, 5 to 20, each drawn at the height that shares the
 * room out exactly, up to `maxRow`. A room too low for 5 rows keeps them
 * at `minRow` and the book cuts them off; one past 20 rows of `maxRow`
 * leaves the rest blank, as designed. Null while nothing is measured.
 */
export function fitRows(room: number, minRow = 20, maxRow = 24): { levels: number; rowHeight: number } | null {
  if (!(room > 0)) return null;
  const levels = Math.max(5, Math.min(20, Math.floor(room / 2 / minRow)));
  return { levels, rowHeight: Math.max(minRow, Math.min(maxRow, room / 2 / levels)) };
}
