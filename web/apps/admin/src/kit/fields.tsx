import { cn, CopyButton, SummaryRow, SummaryTable, Tooltip } from "@exchange/ui";
import type { ReactNode } from "react";

// A record's labelled values as the console's item rows (A94, second
// batch): a drawer's deposit or withdrawal, an account's profile, an audit
// entry - the label in grey, the value, a copy button where it helps, and
// a long value (a list, a JSON) folded until the row is opened.

export type Field = {
  /** A stable key (defaults to the label when it is a string). */
  key?: string;
  label: ReactNode;
  value: ReactNode;
  /** A tip on the label (what the value means). */
  hint?: ReactNode;
  /** Adds a copy button for this text. */
  copy?: string;
  /** What opening the row shows (the whole of a long value). */
  details?: ReactNode;
  "data-testid"?: string;
};

/**
 * Fields lists a record's values, one a row; columns splits them into that
 * many tables side by side on a wide screen (in their order, top to bottom).
 */
export function Fields({ label, items, columns = 1, className }: { label: string; items: Field[]; columns?: 1 | 2 | 3; className?: string }) {
  const per = Math.ceil(items.length / columns);
  const groups = Array.from({ length: columns }, (_, i) => items.slice(i * per, (i + 1) * per)).filter((g) => g.length > 0);
  return (
    <div className={cn("grid gap-3", columns === 2 && "lg:grid-cols-2", columns === 3 && "lg:grid-cols-2 xl:grid-cols-3", className)}>
      {groups.map((g, i) => (
        <SummaryTable key={i} label={label} variant="fields">
          {g.map((f, j) => (
            <SummaryRow
              key={f.key ?? (typeof f.label === "string" ? f.label : `field-${i}-${j}`)}
              data-testid={f["data-testid"]}
              title={
                f.hint ? (
                  <Tooltip content={f.hint}>
                    <span tabIndex={0} className="cursor-help border-b border-dashed border-line-2">
                      {f.label}
                    </span>
                  </Tooltip>
                ) : (
                  f.label
                )
              }
              summary={f.value}
              details={f.details}
              action={f.copy ? <CopyButton value={f.copy} /> : undefined}
            />
          ))}
        </SummaryTable>
      ))}
    </div>
  );
}
