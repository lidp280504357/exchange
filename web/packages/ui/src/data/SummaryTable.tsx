import { ChevronDown } from "lucide-react";
import { createContext, useContext, useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Badge, type BadgeTone } from "../components/Badge";
import { cn } from "../lib/cn";

// Item tables (the admin console's launch checklist, health and summaries;
// A94): each row an item - its title in bold over where it comes from - its
// state as a badge, one readable sentence of its value, an action, and the
// raw values (switch keys, amounts, asset lists) only once the row is
// opened. Fixed columns, one row height, zebra rows that light up on hover.

export type SummaryStatus = {
  tone: Extract<BadgeTone, "success" | "warn" | "danger" | "neutral" | "info">;
  label: ReactNode;
};

export type SummaryTableProps = {
  /** The table's name, for assistive technology. */
  label: string;
  /** The column headings: the item, its state, its value (none hides the heading row). */
  headings?: { item: ReactNode; status?: ReactNode; summary: ReactNode };
  /** Without a status column (a summary of values rather than checks). */
  noStatus?: boolean;
  /** Narrower item and status columns, for a table in half a page. */
  compact?: boolean;
  /**
   * fields: a record's labelled values (a drawer's, an account's) - the
   * label in grey, no status column, lower rows; items (the default): checks
   * and summaries, the title in bold.
   */
  variant?: "items" | "fields";
  /** Fields only: a 96px label column, for a narrow card. */
  narrow?: boolean;
  /** Fields only: no row opens, so no column is kept for the arrow. */
  noArrows?: boolean;
  className?: string;
  children: ReactNode;
};

// The columns: item, status, summary, action, arrow - wide (item 280px,
// status 100px), compact (item 160px), fields (label 136px, or 96px
// narrow; without the arrow's column when no row opens). The details start
// under the status column (the item's width and the gap and padding before
// it).
const layouts = {
  wide: {
    grid: "md:grid-cols-[280px_100px_minmax(0,1fr)_auto_28px]",
    noStatus: "md:grid-cols-[280px_minmax(0,1fr)_auto_28px]",
    details: "md:pl-[calc(280px_+_2rem)]",
  },
  compact: {
    grid: "md:grid-cols-[160px_100px_minmax(0,1fr)_auto_28px]",
    noStatus: "md:grid-cols-[160px_minmax(0,1fr)_auto_28px]",
    details: "md:pl-[calc(160px_+_2rem)]",
  },
  fields: {
    grid: "md:grid-cols-[136px_minmax(0,1fr)_auto_28px]",
    noStatus: "md:grid-cols-[136px_minmax(0,1fr)_auto_28px]",
    details: "md:pl-[calc(136px_+_2rem)]",
  },
  fieldsPlain: {
    grid: "md:grid-cols-[136px_minmax(0,1fr)_auto]",
    noStatus: "md:grid-cols-[136px_minmax(0,1fr)_auto]",
    details: "md:pl-[calc(136px_+_2rem)]",
  },
  narrow: {
    grid: "md:grid-cols-[96px_minmax(0,1fr)_auto_28px]",
    noStatus: "md:grid-cols-[96px_minmax(0,1fr)_auto_28px]",
    details: "md:pl-[calc(96px_+_2rem)]",
  },
  narrowPlain: {
    grid: "md:grid-cols-[96px_minmax(0,1fr)_auto]",
    noStatus: "md:grid-cols-[96px_minmax(0,1fr)_auto]",
    details: "md:pl-[calc(96px_+_2rem)]",
  },
};

type LayoutName = keyof typeof layouts;

const Layout = createContext<{ noStatus: boolean; layout: LayoutName }>({ noStatus: false, layout: "wide" });

/** isFields is a fields layout (grey labels, lower rows); plain is one without the arrow's column. */
const isFields = (l: LayoutName) => l !== "wide" && l !== "compact";
const isPlain = (l: LayoutName) => l === "fieldsPlain" || l === "narrowPlain";

/** SummaryTable holds SummaryRows under their headings. */
export function SummaryTable({ label, headings, noStatus = false, compact = false, variant = "items", narrow = false, noArrows = false, className, children }: SummaryTableProps) {
  const { t } = useTranslation();
  const layout: LayoutName =
    variant === "fields" ? (narrow ? (noArrows ? "narrowPlain" : "narrow") : noArrows ? "fieldsPlain" : "fields") : compact ? "compact" : "wide";
  const l = layouts[layout];
  noStatus = noStatus || variant === "fields";
  return (
    <Layout.Provider value={{ noStatus, layout }}>
      <div role="table" aria-label={label} className={cn("overflow-hidden rounded-2 border border-line-1 text-sm", className)}>
        {headings && (
          <div role="row" className={cn("hidden gap-x-4 border-b border-line-1 bg-bg-2 px-4 py-2 text-xs font-medium text-fg-3 md:grid", noStatus ? l.noStatus : l.grid)}>
            <span role="columnheader">{headings.item}</span>
            {!noStatus && <span role="columnheader">{headings.status}</span>}
            <span role="columnheader">{headings.summary}</span>
            {/* The action's and the arrow's columns, named for assistive technology only. */}
            <span role="columnheader">
              <span className="sr-only">{t("ui.summary.action")}</span>
            </span>
            {!isPlain(layout) && (
              <span role="columnheader">
                <span className="sr-only">{t("ui.summary.details")}</span>
              </span>
            )}
          </div>
        )}
        <div role="rowgroup" className="divide-y divide-line-1">
          {children}
        </div>
      </div>
    </Layout.Provider>
  );
}

export type SummaryRowProps = {
  title: ReactNode;
  /** Where the value comes from, in grey under the title (switch keys as KeyTags). */
  source?: ReactNode;
  status?: SummaryStatus;
  /** One readable sentence of the value now. */
  summary: ReactNode;
  /** The raw values, shown once the row is opened; none, nothing to open. */
  details?: ReactNode;
  /** A link, button or time at the row's end. */
  action?: ReactNode;
  /** Open at first. */
  defaultOpen?: boolean;
  "data-testid"?: string;
  /** Marks the status badge's wrapper (tests read it). */
  statusTestId?: string;
  statusData?: string;
};

/** SummaryRow is an item of a SummaryTable: a click anywhere on it, or its arrow, opens its details. */
export function SummaryRow({ title, source, status, summary, details, action, defaultOpen = false, statusTestId, statusData, ...rest }: SummaryRowProps) {
  const { t } = useTranslation();
  const { noStatus, layout } = useContext(Layout);
  const l = layouts[layout];
  const fields = isFields(layout);
  const plain = isPlain(layout);
  const [open, setOpen] = useState(defaultOpen);
  const id = useId();
  const openable = !plain && details !== undefined && details !== null && details !== false;
  const toggle = () => openable && setOpen((o) => !o);
  return (
    <div role="row" className="even:bg-bg-2/40" data-testid={rest["data-testid"]} data-open={openable ? open : undefined}>
      <div
        className={cn(
          "grid grid-cols-1 items-center gap-x-4 gap-y-1 px-4",
          fields ? "min-h-10 py-2" : "min-h-12 py-2.5",
          noStatus ? l.noStatus : l.grid,
          openable && "cursor-pointer transition-colors hover:bg-bg-2",
        )}
        onClick={toggle}
      >
        <div role="cell" className="flex min-w-0 flex-col gap-0.5">
          <span className={fields ? "text-fg-3" : "font-semibold text-fg-1"}>{title}</span>
          {source && <span className="text-xs leading-5 text-fg-3">{source}</span>}
        </div>
        {!noStatus && (
          <div role="cell">
            {status && (
              <span data-testid={statusTestId} data-status={statusData}>
                <Badge tone={status.tone} dot>
                  {status.label}
                </Badge>
              </span>
            )}
          </div>
        )}
        <div role="cell" className="min-w-0 break-words leading-6 text-fg-1">
          {summary}
        </div>
        <div role="cell" className="whitespace-nowrap text-sm" onClick={(e) => e.stopPropagation()}>
          {action}
        </div>
        {!plain && (
          <div role="cell" className="flex justify-end">
            {openable && (
              <button
                type="button"
                aria-expanded={open}
                aria-controls={id}
                aria-label={t(open ? "ui.summary.collapse" : "ui.summary.expand")}
                title={t(open ? "ui.summary.collapse" : "ui.summary.expand")}
                onClick={(e) => {
                  e.stopPropagation();
                  toggle();
                }}
                className="grid size-7 place-items-center rounded-1 text-fg-3 hover:bg-bg-3 hover:text-fg-1"
              >
                <ChevronDown size={16} className={cn("transition-transform", open && "rotate-180")} />
              </button>
            )}
          </div>
        )}
      </div>
      {openable && open && (
        <div id={id} role="cell" aria-colspan={noStatus ? 4 : 5} className={cn("border-t border-line-1 bg-bg-1 px-4 py-3 text-sm text-fg-2", l.details)}>
          {details}
        </div>
      )}
    </div>
  );
}

/** KeyTag shows a switch key or an ID in a small monospace tag (not a whole sentence in monospace); a key too long for its column breaks. */
export function KeyTag({ children, className }: { children: ReactNode; className?: string }) {
  return <code className={cn("rounded-1 border border-line-1 bg-bg-2 px-1 py-px font-mono text-xs text-fg-2 [overflow-wrap:anywhere]", className)}>{children}</code>;
}

/** withKeys sets the keys in a text - switches (market.house_liquidity), account types (INSURANCE_FUND) - as KeyTags. */
export function withKeys(text: string): ReactNode {
  const parts = text.split(/([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+|\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b)/g);
  return parts.map((p, i) => (i % 2 === 1 ? <KeyTag key={i}>{p}</KeyTag> : p));
}

/** ShortList names a list's first max items (USDT · BTC · ETH) and how many more (+20). */
export function ShortList({ items, max = 3 }: { items: ReactNode[]; max?: number }) {
  const { t } = useTranslation();
  const shown = items.slice(0, max);
  return (
    <span>
      {shown.map((it, i) => (
        <span key={i}>
          {i > 0 && <span className="text-fg-3"> · </span>}
          <span className="whitespace-nowrap">{it}</span>
        </span>
      ))}
      {items.length > max && <span className="text-fg-3"> {t("ui.summary.more", { n: items.length - max })}</span>}
    </span>
  );
}
