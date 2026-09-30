// Which rows of a list that scrolls with the page are near the screen (the
// markets list renders only those from 50 rows, design §4.4). Pure
// arithmetic, so it is tested without a DOM.

export type RangeInput = {
  /** The page's scroll position (window.scrollY). */
  scrollTop: number;
  /** The visible height (window.innerHeight). */
  viewport: number;
  /** The list's top in page coordinates (its rect top + scrollY). */
  offset: number;
  rowHeight: number;
  count: number;
  /** Rows kept rendered above and below the visible ones. */
  overscan: number;
};

/** Rows [start, end) to render. */
export type Range = { start: number; end: number };

/** visibleRange returns the rows on screen plus the overscan, clamped to the list. */
export function visibleRange({ scrollTop, viewport, offset, rowHeight, count, overscan }: RangeInput): Range {
  if (count <= 0 || rowHeight <= 0) return { start: 0, end: 0 };
  const top = scrollTop - offset;
  const first = Math.floor(top / rowHeight);
  const last = Math.ceil((top + Math.max(0, viewport)) / rowHeight);
  const start = Math.min(count, Math.max(0, first - overscan));
  const end = Math.min(count, Math.max(start, last + overscan));
  return { start, end };
}

/** sameRange compares two ranges (a re-render is skipped when nothing moved). */
export function sameRange(a: Range, b: Range): boolean {
  return a.start === b.start && a.end === b.end;
}
