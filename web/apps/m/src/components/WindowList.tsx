import { cn } from "@exchange/ui";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { sameRange, visibleRange, type Range } from "./virtual";

export type WindowListProps<T> = {
  items: readonly T[];
  /** Every row's height in px (fixed). */
  rowHeight: number;
  getKey: (item: T, index: number) => string;
  renderRow: (item: T, index: number) => ReactNode;
  /** Rows kept rendered above and below the screen (default 6). */
  overscan?: number;
  /** Rows drawn before the first measurement, and in a server render (default 16). */
  initialRows?: number;
  className?: string;
  "aria-label"?: string;
};

/**
 * WindowList renders only the rows near the screen of a long list that
 * scrolls with the page (the markets list from 50 rows, design §4.4).
 * Rows have one fixed height and sit at their offset by transform inside
 * a box of the list's full height, so the page keeps its native scrolling
 * (and pull to refresh); scroll, resize and renders re-measure at most
 * once per frame.
 */
export function WindowList<T>({ items, rowHeight, getKey, renderRow, overscan = 6, initialRows = 16, className, "aria-label": ariaLabel }: WindowListProps<T>) {
  const ref = useRef<HTMLDivElement>(null);
  const count = items.length;
  const [range, setRange] = useState<Range>(() => ({ start: 0, end: Math.min(count, initialRows) }));
  const frame = useRef(0);
  const measure = useRef(() => {});

  // What sits above the list may have moved it (a notice, a filter), and
  // the rows may have changed: measure after every render.
  useEffect(() => {
    measure.current = () => {
      frame.current = 0;
      const el = ref.current;
      if (!el) return;
      const next = visibleRange({
        scrollTop: window.scrollY,
        viewport: window.innerHeight,
        offset: el.getBoundingClientRect().top + window.scrollY,
        rowHeight,
        count,
        overscan,
      });
      setRange((cur) => (sameRange(cur, next) ? cur : next));
    };
    if (!frame.current) frame.current = requestAnimationFrame(() => measure.current());
  });

  useEffect(() => {
    const schedule = () => {
      if (!frame.current) frame.current = requestAnimationFrame(() => measure.current());
    };
    window.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    return () => {
      window.removeEventListener("scroll", schedule);
      window.removeEventListener("resize", schedule);
      cancelAnimationFrame(frame.current);
      frame.current = 0;
    };
  }, []);

  const end = Math.min(range.end, count);
  const start = Math.min(range.start, end);
  const rows: ReactNode[] = [];
  for (let i = start; i < end; i++) {
    const item = items[i]!;
    rows.push(
      <div key={getKey(item, i)} role="listitem" className="absolute inset-x-0 top-0" style={{ height: rowHeight, transform: `translateY(${i * rowHeight}px)` }}>
        {renderRow(item, i)}
      </div>,
    );
  }
  return (
    <div ref={ref} role="list" aria-label={ariaLabel} className={cn("relative", className)} style={{ height: count * rowHeight }}>
      {rows}
    </div>
  );
}
