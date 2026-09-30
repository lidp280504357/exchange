import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { Tooltip } from "../components/Tooltip";
import { CopyButton } from "./CopyButton";

export type KeyValueItem = {
  /** A stable key (defaults to the label when it is a string). */
  key?: string;
  label: ReactNode;
  value: ReactNode;
  /** Adds a copy button: true copies the value when it is a string. */
  copy?: string | boolean;
  /** A tip on the label (what the number means). */
  hint?: ReactNode;
  /** Classes of the value (a tone such as text-up). */
  valueClassName?: string;
};

export type KeyValueProps = {
  items: KeyValueItem[];
  /** rows: label left, value right; grid: label above value, in columns. */
  layout?: "rows" | "grid";
  /** Columns of the grid layout (default 2). */
  columns?: 2 | 3 | 4;
  density?: "compact" | "normal";
  className?: string;
};

const gridCols = { 2: "grid-cols-2", 3: "grid-cols-3", 4: "grid-cols-4" };

/**
 * KeyValue lists labelled values (order details, a withdrawal summary,
 * position fields) in rows or a grid, with optional copy buttons.
 */
export function KeyValue({ items, layout = "rows", columns = 2, density = "normal", className }: KeyValueProps) {
  const gap = density === "compact" ? "gap-y-1.5" : "gap-y-2.5";
  return (
    <dl className={cn(layout === "rows" ? cn("flex flex-col", gap) : cn("grid gap-x-4", gap, gridCols[columns]), "text-sm", className)}>
      {items.map((it, i) => {
        const copyValue = typeof it.copy === "string" ? it.copy : it.copy && typeof it.value === "string" ? it.value : null;
        const label = it.hint ? (
          <Tooltip content={it.hint}>
            <span tabIndex={0} className="cursor-help border-b border-dashed border-line-2">
              {it.label}
            </span>
          </Tooltip>
        ) : (
          it.label
        );
        return (
          <div
            key={it.key ?? (typeof it.label === "string" ? it.label : `kv-${i}`)}
            className={cn("min-w-0", layout === "rows" ? "flex items-start justify-between gap-4" : "flex flex-col gap-0.5")}
          >
            <dt className="shrink-0 text-fg-3">{label}</dt>
            <dd className={cn("flex min-w-0 items-center gap-1 text-fg-1 tabular-nums", layout === "rows" && "justify-end text-right", it.valueClassName)}>
              <span className="min-w-0 break-all">{it.value}</span>
              {copyValue && <CopyButton value={copyValue} />}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}
