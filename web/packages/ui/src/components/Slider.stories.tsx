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

/**
 * The thumb's centre is the value (B173): at 0% and 100% the dot's centre
 * sits on the track's ends, as the fill's end and the marks do. The last
 * slider plays its arrival effect each time it reaches 100% (the buttons
 * replay it): the dot's pulse and halo, two sparks running back on sine
 * paths with trails, the marks flashing as they pass, a band over the fill;
 * a still halo under reduced motion.
 */
export const EndsAndPeak: Story = {
  render: () => {
    const [v, setV] = useState(75);
    return (
      <div className="flex flex-col gap-6">
        {[0, 50, 100].map((at) => (
          <Slider key={at} value={at} onValueChange={() => {}} pulseAtMax={false} marks={[0, 25, 50, 75, 100]} markLabels formatMark={(m) => `${m}%`} aria-label={`${at}%`} />
        ))}
        <Slider value={v} onValueChange={setV} tone="up" marks={[0, 25, 50, 75, 100]} markLabels formatMark={(m) => `${m}%`} aria-label="Pulse at 100%" />
        <div className="flex gap-2 text-xs">
          <button type="button" className="rounded border border-line-2 px-2 py-1" onClick={() => setV(100)}>100%</button>
          <button type="button" className="rounded border border-line-2 px-2 py-1" onClick={() => setV(75)}>75%</button>
        </div>
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
