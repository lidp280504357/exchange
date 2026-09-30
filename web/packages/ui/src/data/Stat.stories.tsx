import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "../components/Button";
import { AmountText } from "./AmountText";
import { CountUp } from "./CountUp";
import { Stat } from "./Stat";

const week = ["61200", "61850", "61010", "62400", "62950", "62300", "63214"];
const down = ["3.1", "3.05", "2.98", "3.02", "2.91", "2.88", "2.84"];

const meta = { title: "Data/Stat", component: Stat, args: { label: "总资产估值", value: "12,345.67" } } satisfies Meta<typeof Stat>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The assets page's cards: CountUp values, today's change, sparklines. */
export const Cards: Story = {
  render: () => {
    const [total, setTotal] = useState("12345.67");
    return (
      <div className="flex flex-col gap-4">
        <div className="grid w-[900px] grid-cols-3 gap-4">
          <div className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Stat label="总资产估值" size="lg" value={<CountUp value={total} decimals={2} />} unit="USDT" change="0.0213" changeLabel="今日" sparkline={week} />
          </div>
          <div className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Stat label="合约账户" value={<AmountText value="2310.4" decimals={2} />} unit="USDT" change="-0.0121" changeLabel="今日" sparkline={down} hint="含未实现盈亏" />
          </div>
          <div className="rounded-3 border border-line-1 bg-bg-1 p-4">
            <Stat label="今日盈亏" value={<AmountText value="263.2" decimals={2} sign tone="auto" />} unit="USDT" />
          </div>
        </div>
        <div>
          <Button size="sm" variant="secondary" onClick={() => setTotal((v) => (v === "12345.67" ? "15890.12" : "12345.67"))}>
            划转后刷新
          </Button>
        </div>
      </div>
    );
  },
};

export const Loading: Story = {
  render: () => (
    <div className="w-72 rounded-3 border border-line-1 bg-bg-1 p-4">
      <Stat label="总资产估值" value="—" unit="USDT" change="0" changeLabel="今日" loading />
    </div>
  ),
};
