import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Slider } from "./Slider";

const meta = {
  title: "Base/Slider",
  component: Slider,
  args: { value: 25, onValueChange: () => {} },
  decorators: [(Story) => <div className="w-80 py-4"><Story /></div>],
} satisfies Meta<typeof Slider>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Percent: Story = {
  render: () => {
    const [v, setV] = useState(25);
    return (
      <Slider
        value={v}
        onValueChange={setV}
        marks={[0, 25, 50, 75, 100]}
        markLabels
        formatMark={(m) => `${m}%`}
        formatValue={(x) => `${x}%`}
        aria-label="Percent of balance"
      />
    );
  },
};

export const Tones: Story = {
  render: () => {
    const [a, setA] = useState(40);
    const [b, setB] = useState(70);
    return (
      <div className="flex flex-col gap-6">
        <Slider value={a} onValueChange={setA} tone="up" marks={[0, 25, 50, 75, 100]} aria-label="Buy" />
        <Slider value={b} onValueChange={setB} tone="down" marks={[0, 25, 50, 75, 100]} aria-label="Sell" />
      </div>
    );
  },
};

export const Leverage: Story = {
  render: () => {
    const [v, setV] = useState(10);
    return (
      <Slider
        value={v}
        onValueChange={setV}
        min={1}
        max={125}
        marks={[1, 25, 50, 75, 100, 125]}
        markLabels
        formatMark={(m) => `${m}x`}
        aria-label="Leverage"
      />
    );
  },
};
