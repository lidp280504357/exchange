import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { Sparkline } from "./Sparkline";

// A 7-day hourly series built from a sine and a drift (168 points).
function series(n: number, drift: number, phase = 0): number[] {
  return Array.from({ length: n }, (_, i) => 100 + Math.sin(i / 9 + phase) * 4 + Math.sin(i / 3.1) * 1.5 + i * drift);
}

const meta = {
  title: "Data/Sparkline",
  component: Sparkline,
  args: { data: series(168, 0.05), width: 120, height: 36 },
} satisfies Meta<typeof Sparkline>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Auto: Story = {};

export const Tones: Story = {
  render: () => (
    <div className="flex items-center gap-6">
      <Sparkline data={series(168, 0.05)} />
      <Sparkline data={series(168, -0.05, 2)} />
      <Sparkline data={series(48, 0, 1)} tone="brand" />
      <Sparkline data={series(48, 0, 3)} tone="neutral" area={false} />
      <Sparkline data={["1.02", "1.05", "0.99", "1.10", "1.12"]} aria-label="7 day trend" />
    </div>
  ),
};

/** Fluid: stretches to its container; the stroke keeps its width. */
export const Fluid: Story = {
  render: () => (
    <div className="w-[480px] rounded-2 border border-line-1 p-3">
      <Sparkline data={series(168, 0.03)} fluid height={64} />
    </div>
  ),
};

/** Live: a new point every second. */
export const Live: Story = {
  render: () => {
    const [data, setData] = useState(() => series(60, 0.02));
    useEffect(() => {
      const id = setInterval(() => setData((d) => [...d.slice(1), (d[d.length - 1] ?? 100) + (Math.random() - 0.48) * 2]), 1000);
      return () => clearInterval(id);
    }, []);
    return <Sparkline data={data} width={200} height={48} />;
  },
};
