import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { NumberInput } from "./NumberInput";

const meta = {
  title: "Base/NumberInput",
  component: NumberInput,
  args: { value: "", onValueChange: () => {}, decimals: 4, placeholder: "0.0000" },
  decorators: [(Story) => <div className="w-80"><Story /></div>],
} satisfies Meta<typeof NumberInput>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Digits past the precision are trimmed as they are typed (try 1.234567). */
export const Precision: Story = {
  render: (args) => {
    const [v, setV] = useState("");
    return (
      <div className="flex flex-col gap-2">
        <NumberInput {...args} value={v} onValueChange={setV} unit="BTC" />
        <span className="text-xs text-fg-3">value: {JSON.stringify(v)}</span>
      </div>
    );
  },
};

/** +/- and the arrow keys move along the tick grid with exact decimals. */
export const Stepper: Story = {
  render: () => {
    const [v, setV] = useState("63214.5");
    return <NumberInput value={v} onValueChange={setV} step="0.5" decimals={1} prefix="价格" unit="USDT" align="right" snap />;
  },
};

/** Max and the percent slider round down to the lot size. */
export const MaxAndSlider: Story = {
  render: () => {
    const [v, setV] = useState("");
    return (
      <div className="flex flex-col gap-2">
        <NumberInput value={v} onValueChange={setV} step="0.001" max="1.23456" slider sliderTone="up" prefix="数量" unit="BTC" align="right" />
        <span className="text-xs text-fg-3">value: {JSON.stringify(v)} (max 1.23456, lot 0.001)</span>
      </div>
    );
  },
};

export const Invalid: Story = {
  render: () => {
    const [v, setV] = useState("0.00001");
    return <NumberInput value={v} onValueChange={setV} decimals={5} unit="BTC" error="最小数量 0.0001 BTC" />;
  },
};
