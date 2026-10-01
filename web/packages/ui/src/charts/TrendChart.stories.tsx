import type { Meta, StoryObj } from "@storybook/react-vite";
import { TrendChart } from "./TrendChart";

const days = Array.from({ length: 30 }, (_, i) => {
  const d = new Date(Date.UTC(2026, 8, 2 + i));
  return {
    x: d.toISOString().slice(0, 10),
    label: `${d.getUTCMonth() + 1}/${d.getUTCDate()}`,
    values: { users: Math.round(20 + 15 * Math.sin(i / 3) + i), trades: Math.round(300 + 120 * Math.cos(i / 4) + i * 8), turnover: 120000 + 40000 * Math.sin(i / 5) + i * 3000 },
  };
});

const meta = {
  title: "Charts/TrendChart",
  component: TrendChart,
  args: {
    data: days,
    series: [
      { key: "users", label: "新增用户", kind: "bar", color: "chart-1" },
      { key: "trades", label: "成交笔数", kind: "bar", color: "chart-2" },
      { key: "turnover", label: "成交额（USDT）", kind: "line", color: "chart-3" },
    ],
    height: 240,
    "aria-label": "成交与新增用户",
  },
} satisfies Meta<typeof TrendChart>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The overview's 30 days: bars on the left scale, the line on the right. */
export const Overview: Story = {};

/** A week, one series. */
export const Week: Story = { args: { data: days.slice(-7), series: [{ key: "trades", label: "成交笔数", kind: "bar", color: "chart-2" }] } };
