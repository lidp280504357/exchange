import { formatPercent } from "@exchange/core";
import type { FuturesDataPoint, FuturesMetric as Metric, FuturesPeriod } from "@exchange/core/futures/data";
import { axisTime, chartPoints, formatValue, METRIC_AXIS, METRIC_FORMS, METRIC_VALUES, pointTime, windowChange, type FormatContext } from "@exchange/core/futures/series";
import { useMemo, useState } from "react";
import { cn } from "../lib/cn";
import { useFormatContext } from "../lib/settings";
import { MetricCard, type LegendItem, type MetricCardView } from "./MetricCard";
import { SeriesChart } from "./SeriesChart";
import { SeriesTable } from "./SeriesTable";

// One statistic of a contract as a card (design 2026-10-06 §3.3): the
// latest value, the chart in its form (core METRIC_FORMS), a tooltip and a
// table with every value of each point. The page reads the data (core
// useFuturesData) and gives the words; both sites draw it the same way.

export type FuturesMetricLabels = {
  title: string;
  hint: string;
  /** The name of each value of the statistic (core METRIC_VALUES), by key. */
  values: Record<string, string>;
};

export type FuturesMetricCommon = {
  chart: string;
  table: string;
  /** The table's time column. */
  time: string;
  /** A card without points. */
  empty: string;
  /** "区间 {{value}}": the open interest's move over the points shown. */
  windowChange: (value: string) => string;
};

export type FuturesMetricProps = {
  metric: Metric;
  period: FuturesPeriod;
  points: readonly FuturesDataPoint[] | undefined;
  state: "loading" | "error" | "empty" | "ready";
  /** The points are another period's, until the right ones arrive. */
  stale?: boolean;
  onRetry?: () => void;
  labels: FuturesMetricLabels;
  common: FuturesMetricCommon;
  /** A quantity's unit: the base asset, or contracts ("张") of a coin-margined contract. */
  qtyUnit: string;
  priceDecimals: number;
  qtyDecimals: number;
  /** The chart's height, px. */
  height?: number;
  className?: string;
};

/** The values beside the headline of each statistic (the lead is the first of METRIC_VALUES). */
const DETAIL: Record<Metric, readonly string[]> = {
  open_interest: ["open_interest_value"],
  top_long_short_account: ["long", "short"],
  top_long_short_position: ["long", "short"],
  long_short_account: ["long", "short"],
  taker_ratio: ["buy_vol", "sell_vol"],
  basis: ["basis_rate"],
  funding: ["mark_price"],
};

/** The tone of each value a chart draws (the rest have none). */
const TONES: Record<string, LegendItem["tone"]> = {
  long: "up",
  short: "down",
  buy_vol: "up",
  sell_vol: "down",
  open_interest: "chart-1",
  basis: "chart-1",
};

const key: Record<LegendItem["tone"], string> = { up: "bg-up", down: "bg-down", "chart-1": "bg-chart-1" };

export function FuturesMetric({
  metric, period, points, state, stale, onRetry, labels, common, qtyUnit, priceDecimals, qtyDecimals, height = 150, className,
}: FuturesMetricProps) {
  const { locale, timeZone } = useFormatContext();
  const [view, setView] = useState<MetricCardView>("chart");
  const series = useMemo(() => chartPoints(points ?? []), [points]);
  const ctx: FormatContext = { priceDecimals, qtyDecimals, locale };
  const values = METRIC_VALUES[metric];
  const form = METRIC_FORMS[metric];
  const timeOf = metric === "funding" ? "funding" : period;
  const unitOf = (k: string) => values.find((v) => v.key === k)?.unit ?? "ratio";
  const show = (k: string, raw: string | undefined) => {
    const s = formatValue(unitOf(k), raw, ctx);
    return unitOf(k) === "qty" && s !== "—" ? `${s} ${qtyUnit}` : s;
  };
  const latest = series.at(-1);
  const lead = values[0]!.key;
  const ready = state === "ready" && series.length > 0;
  const shownState = state === "ready" && series.length === 0 ? "empty" : state;

  const drawn = form.kind === "line" || form.kind === "columns" ? [form.key] : [form.up, form.down];
  const legend: LegendItem[] = drawn.filter((k) => TONES[k]).map((k) => ({ label: labels.values[k] ?? k, tone: TONES[k]! }));

  let detail: string | undefined;
  if (latest) {
    const parts = DETAIL[metric].map((k) => `${labels.values[k] ?? k} ${show(k, latest.raw[k])}`);
    if (metric === "open_interest") {
      const change = windowChange(series, "open_interest");
      if (change !== null) parts.push(common.windowChange(formatPercent(change)));
    }
    detail = parts.join(" · ");
  }

  const tooltip = (i: number) => {
    const p = series[i]!;
    return (
      <div className="flex flex-col gap-1">
        <div className="text-fg-3">{pointTime(p.t, timeOf, locale, timeZone)}</div>
        {values.map((v) => {
          const tone = drawn.includes(v.key) ? TONES[v.key] : undefined;
          return (
            <div key={v.key} className="flex items-center gap-2 whitespace-nowrap">
              <span aria-hidden className={cn("h-0.5 w-2.5 shrink-0 rounded-full", tone ? key[tone] : "bg-transparent")} />
              <span className="font-medium tabular-nums text-fg-1">{show(v.key, p.raw[v.key])}</span>
              <span className="text-fg-3">{labels.values[v.key] ?? v.key}</span>
            </div>
          );
        })}
      </div>
    );
  };

  const summary = latest ? `${labels.title} ${show(lead, latest.raw[lead])}` : labels.title;
  const tableRows = useMemo(
    () =>
      [...series].reverse().map((p) => ({
        key: p.t,
        cells: [pointTime(p.t, timeOf, locale, timeZone), ...values.map((v) => show(v.key, p.raw[v.key]))],
      })),
    // show and values follow from the inputs listed.
    [series, timeOf, locale, timeZone, metric, qtyUnit, priceDecimals, qtyDecimals],
  );

  return (
    <MetricCard
      title={labels.title}
      hint={labels.hint}
      value={latest ? show(lead, latest.raw[lead]) : undefined}
      detail={detail}
      legend={legend}
      view={view}
      onViewChange={setView}
      viewLabels={{ chart: common.chart, table: common.table }}
      state={shownState}
      emptyText={common.empty}
      onRetry={onRetry}
      bodyHeight={height}
      className={className}
    >
      {ready &&
        (view === "chart" ? (
          <SeriesChart
            points={series}
            form={form}
            axis={METRIC_AXIS[metric]}
            height={height}
            formatX={(t) => axisTime(t, timeOf, locale, timeZone)}
            tooltip={tooltip}
            aria-label={summary}
            stale={stale}
          />
        ) : (
          <SeriesTable
            columns={[common.time, ...values.map((v) => labels.values[v.key] ?? v.key)]}
            rows={tableRows}
            height={height}
            aria-label={labels.title}
            className={cn(stale && "opacity-50")}
          />
        ))}
    </MetricCard>
  );
}

