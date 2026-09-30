import type { Meta, StoryObj } from "@storybook/react-vite";
import { ChartCandlestick, Layers } from "lucide-react";
import { useState } from "react";
import { Segmented } from "./Segmented";

const meta = {
  title: "Base/Segmented",
  component: Segmented,
  args: {
    value: "limit",
    onValueChange: () => {},
    items: [
      { value: "limit", label: "限价" },
      { value: "market", label: "市价" },
    ],
  },
} satisfies Meta<typeof Segmented>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {
  render: (args) => {
    const [v, setV] = useState("limit");
    return <Segmented {...args} value={v} onValueChange={setV} aria-label="Order type" />;
  },
};

/** The order form's buy/sell switch: the thumb takes the side's colour. */
export const BuySell: Story = {
  render: () => {
    const [v, setV] = useState("BUY");
    return (
      <div className="w-72">
        <Segmented
          block
          square
          size="md"
          value={v}
          onValueChange={setV}
          aria-label="Side"
          items={[
            { value: "BUY", label: "买入", thumbClassName: "bg-up", activeClassName: "text-white" },
            { value: "SELL", label: "卖出", thumbClassName: "bg-down", activeClassName: "text-white" },
          ]}
        />
      </div>
    );
  },
};

export const SizesAndIcons: Story = {
  render: () => {
    const [a, setA] = useState("1h");
    const [b, setB] = useState("chart");
    return (
      <div className="flex flex-col items-start gap-3">
        <Segmented
          size="xs"
          value={a}
          onValueChange={setA}
          aria-label="Interval"
          items={["1m", "15m", "1h", "4h", "1d"].map((i) => ({ value: i, label: i }))}
        />
        <Segmented
          size="md"
          value={b}
          onValueChange={setB}
          aria-label="View"
          items={[
            { value: "chart", label: "图表", icon: <ChartCandlestick size={14} /> },
            { value: "depth", label: "深度", icon: <Layers size={14} /> },
          ]}
        />
      </div>
    );
  },
};
