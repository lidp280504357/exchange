import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "../components/Button";
import { CountUp } from "./CountUp";

const meta = { title: "Data/CountUp", component: CountUp, args: { value: "12345.67", decimals: 2 } } satisfies Meta<typeof CountUp>;
export default meta;

type Story = StoryObj<typeof meta>;

const values = ["12345.67", "15890.12", "980.5", "1234567.89"];

/** Click to roll between values; reduced motion jumps instead. */
export const Roll: Story = {
  render: () => {
    const [i, setI] = useState(0);
    return (
      <div className="flex flex-col items-start gap-4">
        <CountUp value={values[i % values.length]} decimals={2} className="text-2xl font-semibold" suffix={<span className="ml-1 text-sm text-fg-3">USDT</span>} />
        <Button size="sm" onClick={() => setI((v) => v + 1)}>
          下一个
        </Button>
      </div>
    );
  },
};

/** The home page counters. */
export const Counters: Story = {
  render: () => (
    <div className="flex gap-10">
      <div>
        <CountUp value="50" decimals={0} className="text-2xl font-semibold text-brand" suffix="+" />
        <div className="text-sm text-fg-3">主流币种</div>
      </div>
      <div>
        <CountUp value="1283456789.12" decimals={0} className="text-2xl font-semibold" prefix="$" />
        <div className="text-sm text-fg-3">24h 成交额</div>
      </div>
    </div>
  ),
};
