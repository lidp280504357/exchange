import { ChartLine, Info, Table2 } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../components/Button";
import { Skeleton } from "../components/Skeleton";
import { Popover } from "../components/Popover";
import { cn } from "../lib/cn";

// A card of the futures data panels (design 2026-10-06 §3.3): the
// statistic's name with its definition, its latest value with the figures
// beside it, a legend when two series share the chart, and the chart or
// its table (a switch in the corner). Loading, failure and "no data"
// take the body's height, so the grid does not jump as cards fill in.

export type MetricCardView = "chart" | "table";

export type LegendItem = { label: string; tone: "up" | "down" | "chart-1" };

export type MetricCardProps = {
  title: string;
  /** What the statistic is (an info icon with a tooltip). */
  hint?: string;
  /** The latest value. */
  value?: ReactNode;
  /** Figures beside it. */
  detail?: ReactNode;
  legend?: readonly LegendItem[];
  view: MetricCardView;
  onViewChange: (view: MetricCardView) => void;
  /** The switch's names: chart, table. */
  viewLabels: { chart: string; table: string };
  state: "loading" | "error" | "empty" | "ready";
  /** What an empty card says ("暂无数据"). */
  emptyText: string;
  onRetry?: () => void;
  /** The body's height, px (the chart's). */
  bodyHeight: number;
  /** The chart or the table, when ready. */
  children?: ReactNode;
  className?: string;
};

const swatch: Record<LegendItem["tone"], string> = { up: "bg-up", down: "bg-down", "chart-1": "bg-chart-1" };

export function MetricCard({
  title, hint, value, detail, legend, view, onViewChange, viewLabels, state, emptyText, onRetry, bodyHeight, children, className,
}: MetricCardProps) {
  const { t } = useTranslation();
  const toggle = (v: MetricCardView, label: string, icon: ReactNode) => (
    <button
      type="button"
      aria-pressed={view === v}
      aria-label={label}
      title={label}
      onClick={() => onViewChange(v)}
      className={cn(
        "hit-area grid size-6 place-items-center rounded-1 transition-colors duration-[var(--t-fast)]",
        view === v ? "bg-bg-3 text-fg-1" : "text-fg-3 hover:text-fg-1",
      )}
    >
      {icon}
    </button>
  );
  return (
    <section aria-label={title} className={cn("flex min-w-0 flex-col rounded-2 border border-line-1 bg-bg-1 p-3", className)}>
      <header className="flex min-w-0 items-center gap-1.5">
        <h3 className="min-w-0 truncate text-sm font-medium text-fg-1" title={title}>
          {title}
        </h3>
        {hint && <InfoHint text={hint} label={hint} />}
        <div className="ml-auto flex shrink-0 items-center gap-0.5">
          {toggle("chart", viewLabels.chart, <ChartLine size={14} />)}
          {toggle("table", viewLabels.table, <Table2 size={14} />)}
        </div>
      </header>
      <div className="mt-1 flex min-h-6 min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5">
        {state === "loading" ? (
          <Skeleton className="h-4 w-28" />
        ) : (
          <>
            {value !== undefined && <span className="text-md font-semibold text-fg-1">{value}</span>}
            {detail !== undefined && <span className="text-xs text-fg-3">{detail}</span>}
          </>
        )}
      </div>
      {/* A single series needs no legend (the title names it), but keeps the row, so the charts of a grid line up. */}
      <ul aria-hidden={!legend || legend.length < 2 || undefined} className="mt-1 flex min-h-4 flex-wrap gap-x-3 gap-y-0.5 text-xs text-fg-2">
        {legend &&
          legend.length > 1 &&
          legend.map((l) => (
            <li key={l.label} className="flex items-center gap-1.5">
              <span aria-hidden className={cn("size-2 rounded-[2px]", swatch[l.tone])} />
              {l.label}
            </li>
          ))}
      </ul>
      <div className="mt-2" style={{ minHeight: bodyHeight }}>
        {state === "ready" ? (
          children
        ) : state === "loading" ? (
          <Skeleton className="w-full" style={{ height: bodyHeight }} />
        ) : (
          <div className="flex flex-col items-center justify-center gap-2 text-center text-sm text-fg-3" style={{ height: bodyHeight }}>
            {state === "error" ? t("state.errorTitle") : emptyText}
            {state === "error" && onRetry && (
              <Button size="sm" variant="secondary" onClick={onRetry} className="hit-area">
                {t("common.retry")}
              </Button>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

/**
 * InfoHint is an info icon that shows what a figure means: a popover on a
 * click or a tap (a hover tooltip never opens on a phone).
 */
export function InfoHint({ text, label }: { text: string; label: string }) {
  return (
    <Popover
      side="bottom"
      align="start"
      arrow
      aria-label={label}
      className="max-w-72 text-xs leading-relaxed text-fg-2"
      trigger={
        <button type="button" aria-label={label} className="hit-area grid size-5 shrink-0 place-items-center rounded-full text-fg-3 hover:text-fg-1">
          <Info size={13} />
        </button>
      }
    >
      {text}
    </Popover>
  );
}
