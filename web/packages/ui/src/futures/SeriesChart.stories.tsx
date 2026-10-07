import type { Meta, StoryObj } from "@storybook/react-vite";
import { SeriesChart, type SeriesChartProps } from "./SeriesChart";
import type { SeriesPoint } from "./scale";

// The bare chart of the futures data cards (design 2026-10-06 §3.3), one
// story per form, on 30 points of 5 minutes. Hover, touch or focus it and
// use the arrow keys to read a point; the card around it is
// Futures/FuturesMetric.

const start = Date.UTC(2026, 9, 6, 15, 40);
const series = (f: (i: number) => Record<string, number>): SeriesPoint[] => Array.from({ length: 30 }, (_, i) => ({ t: start + i * 300_000, v: f(i) }));

const hhmm = (t: number) => new Date(t).toISOString().slice(11, 16);
const tip = (points: SeriesPoint[]) => (i: number) => (
  <div className="flex flex-col gap-0.5 tabular-nums">
    <span className="text-fg-3">{hhmm(points[i]?.t ?? 0)}</span>
    {Object.entries(points[i]?.v ?? {}).map(([k, v]) => (
      <span key={k}>
        {k} {v.toFixed(4)}
      </span>
    ))}
  </div>
);

const line = series((i) => ({ open_interest: 96_000 + 180 * Math.sin(i / 4) + i * 6 }));
const columns = series((i) => ({ funding_rate: 0.00004 * Math.sin(i / 2) + 0.00001 }));
const share = series((i) => {
  const long = 0.68 + 0.03 * Math.sin(i / 3);
  return { long, short: 1 - long };
});
const mirror = series((i) => ({ buy_vol: 260 + 120 * Math.sin(i / 2.5), sell_vol: 250 + 110 * Math.cos(i / 3) }));

const meta = {
  title: "Futures/SeriesChart",
  component: SeriesChart,
  args: {
    points: line,
    form: { kind: "line", key: "open_interest", area: true },
    axis: "compact",
    height: 160,
    formatX: hhmm,
    tooltip: tip(line),
    "aria-label": "持仓量",
    describe: (i: number) => `${hhmm(line[i]?.t ?? 0)} ${line[i]?.v.open_interest?.toFixed(0)}`,
    className: "w-[420px]",
  } satisfies SeriesChartProps,
} satisfies Meta<typeof SeriesChart>;
export default meta;

type Story = StoryObj<typeof meta>;

/** A line over a 10% wash (open interest); a line alone is the basis. */
export const Line: Story = {};

/** Columns from zero, coloured by sign (funding rates). */
export const Columns: Story = {
  args: { points: columns, form: { kind: "columns", key: "funding_rate" }, axis: "percent", tooltip: tip(columns), "aria-label": "资金费率历史" },
};

/** Two shares stacked to 100%: long below, short above (long/short ratios). */
export const Share: Story = {
  args: { points: share, form: { kind: "share", up: "long", down: "short" }, axis: "percent", tooltip: tip(share), "aria-label": "多空账户数比" },
};

/** Two volumes mirrored about zero: buys up, sells down (taker volume). */
export const Mirror: Story = {
  args: { points: mirror, form: { kind: "mirror", up: "buy_vol", down: "sell_vol" }, axis: "compact", tooltip: tip(mirror), "aria-label": "主动买卖量" },
};

/** The previous period's points while the asked one loads: dimmed, nothing jumps. */
export const Stale: Story = { args: { stale: true } };

/** One point: a dot where a line would be. */
export const OnePoint: Story = { args: { points: line.slice(-1), tooltip: tip(line.slice(-1)) } };

/** No points: an empty plot (the card above it says there is no data). */
export const Empty: Story = { args: { points: [] } };
