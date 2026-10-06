import type { FuturesDataPoint, Liquidation } from "@exchange/core/futures/data";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { FuturesMetric, type FuturesMetricProps } from "./FuturesMetric";
import { LiquidationTape } from "./LiquidationTape";

// The futures data cards (design 2026-10-06 §3.3) on numbers shaped like
// the test server's BTC-USDT-PERP: 30 points of 5 minutes.

const start = Date.UTC(2026, 9, 6, 15, 40);
const at = (i: number, minutes = 5) => new Date(start + i * minutes * 60_000).toISOString();
const n = (v: number, d: number) => v.toFixed(d);

const openInterest: FuturesDataPoint[] = Array.from({ length: 30 }, (_, i) => {
  const oi = 96_000 + 180 * Math.sin(i / 4) + i * 6;
  return { time: at(i), values: { open_interest: n(oi, 3), open_interest_value: n(oi * 85_750, 2) } };
});
const shares = (bias: number): FuturesDataPoint[] =>
  Array.from({ length: 30 }, (_, i) => {
    const long = bias + 0.03 * Math.sin(i / 3);
    return { time: at(i), values: { long: n(long, 4), short: n(1 - long, 4), long_short_ratio: n(long / (1 - long), 4) } };
  });
const taker: FuturesDataPoint[] = Array.from({ length: 30 }, (_, i) => {
  const buy = 260 + 120 * Math.sin(i / 2.5);
  const sell = 250 + 110 * Math.cos(i / 3);
  return { time: at(i), values: { buy_vol: n(buy, 3), sell_vol: n(sell, 3), buy_sell_ratio: n(buy / sell, 4) } };
});
const basis: FuturesDataPoint[] = Array.from({ length: 30 }, (_, i) => {
  const b = -45 + 12 * Math.sin(i / 4);
  return { time: at(i), values: { basis: n(b, 8), basis_rate: n(b / 85_750, 4), futures_price: n(85_750 + b, 1), index_price: "85750" } };
});
const funding: FuturesDataPoint[] = Array.from({ length: 30 }, (_, i) => ({
  time: new Date(Date.UTC(2026, 8, 27) + i * 8 * 3_600_000).toISOString(),
  values: { funding_rate: n(0.00004 * Math.sin(i / 2) + 0.00001, 8), mark_price: "85675.2" },
}));

const common: FuturesMetricProps["common"] = { chart: "图表", table: "表格", time: "时间", empty: "暂无数据", windowChange: (v) => `区间 ${v}` };
const shareLabels = { long_short_ratio: "多空比", long: "多", short: "空" };

const meta = {
  title: "Futures/FuturesMetric",
  component: FuturesMetric,
  args: {
    metric: "open_interest",
    period: "5m",
    points: openInterest,
    state: "ready",
    labels: { title: "持仓量", hint: "未平仓合约的数量与美元价值", values: { open_interest: "持仓量", open_interest_value: "持仓价值" } },
    common,
    qtyUnit: "BTC",
    priceDecimals: 1,
    qtyDecimals: 3,
    height: 150,
    className: "w-[420px]",
  },
} satisfies Meta<typeof FuturesMetric>;
export default meta;

type Story = StoryObj<typeof meta>;

/** A line with a 10% wash; the move over the window beside the value. */
export const OpenInterest: Story = {};

/** Long over short shares stacked to 100%, with the ratio as the value. */
export const LongShort: Story = {
  args: { metric: "top_long_short_account", points: shares(0.68), labels: { title: "大户账户数多空比", hint: "大户中持多与持空的账户比例", values: shareLabels } },
};

/** Buys above zero, sells mirrored below. */
export const Taker: Story = {
  args: {
    metric: "taker_ratio",
    points: taker,
    labels: { title: "主动买卖量", hint: "主动买入与卖出的成交量", values: { buy_sell_ratio: "买卖比", buy_vol: "主动买入", sell_vol: "主动卖出" } },
  },
};

/** A level below zero: the grid's zero line is outside the range. */
export const Basis: Story = {
  args: {
    metric: "basis",
    points: basis,
    labels: { title: "基差", hint: "合约价格减指数价格", values: { basis: "基差", basis_rate: "基差率", futures_price: "合约价格", index_price: "指数价格" } },
  },
};

/** Settled rates as columns coloured by sign. */
export const Funding: Story = {
  args: { metric: "funding", points: funding, labels: { title: "资金费率历史", hint: "每期结算的资金费率", values: { funding_rate: "资金费率", mark_price: "结算标记价格" } } },
};

export const Loading: Story = { args: { state: "loading", points: undefined } };
export const NoData: Story = { args: { state: "empty", points: [] } };
export const Failed: Story = { args: { state: "error", points: undefined, onRetry: () => {} } };

const liquidations: Liquidation[] = Array.from({ length: 12 }, (_, i) => ({
  symbol: "BTC-USDT-PERP",
  position_side: i % 3 ? "SHORT" : "LONG",
  price: n(86_100 - i * 20, 1),
  average_price: n(85_778 - i * 18, 1),
  quantity: n(0.005 + i * 0.013, 3),
  value_usd: n((0.005 + i * 0.013) * 85_778, 4),
  traded_at: new Date(Date.UTC(2026, 9, 6, 18, 3, 17) - i * 41_000).toISOString(),
}));

/** The liquidation stream (a story of its own component). */
export const Liquidations: StoryObj<typeof LiquidationTape> = {
  render: () => (
    <LiquidationTape
      items={liquidations}
      priceDecimals={1}
      qtyDecimals={3}
      labels={{ time: "时间", side: "方向", price: "均价", quantity: "数量(BTC)", value: "价值(USD)", long: "多单爆仓", short: "空单爆仓" }}
      className="w-[420px]"
    />
  ),
};
