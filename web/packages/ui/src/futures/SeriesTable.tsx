import { cn } from "../lib/cn";

// The table view of a SeriesChart (design 2026-10-06 §3.3): the same
// points newest first with every value of each, for reading exact figures
// and for screen readers. It takes the chart's height and scrolls, so a
// card keeps its size when it switches view.

export type SeriesTableProps = {
  /** The header: the time, then each value. */
  columns: readonly string[];
  /** Newest first: the time, then the formatted values. */
  rows: readonly { key: string | number; cells: readonly string[] }[];
  /** The box's height, px (the chart's). */
  height?: number;
  "aria-label": string;
  className?: string;
};

export function SeriesTable({ columns, rows, height = 160, "aria-label": label, className }: SeriesTableProps) {
  return (
    <div className={cn("overflow-auto overscroll-contain", className)} style={{ height }}>
      <table aria-label={label} className="w-full border-separate border-spacing-0 text-xs tabular-nums">
        <thead>
          <tr>
            {columns.map((c, i) => (
              <th key={c} scope="col" className={cn("sticky top-0 z-[1] whitespace-nowrap bg-bg-1 px-2 py-1.5 font-normal text-fg-3", i === 0 ? "text-left" : "text-right")}>
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className="hover:bg-bg-2">
              {r.cells.map((c, i) => (
                <td key={i} className={cn("whitespace-nowrap border-t border-line-1 px-2 py-1", i === 0 ? "text-left text-fg-2" : "text-right text-fg-1")}>
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
