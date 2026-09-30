import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { Progress } from "./Progress";

const meta = { title: "Feedback/Progress", component: Progress, args: { value: 32 } } satisfies Meta<typeof Progress>;
export default meta;

type Story = StoryObj<typeof meta>;

/** A withdrawal limit: label and value text above the bar. */
export const Limit: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-4">
      <Progress value={3200} max={10000} label="今日已用额度" valueText="3,200 / 10,000 USDT" />
      <Progress value={9100} max={10000} tone="warn" label="本月已用额度" valueText="91,000 / 100,000 USDT" />
    </div>
  ),
};

/** Confirmations advancing: the fill slides with a transform. */
export const Live: Story = {
  render: () => {
    const [n, setN] = useState(3);
    useEffect(() => {
      const id = setInterval(() => setN((v) => (v >= 19 ? 0 : v + 1)), 700);
      return () => clearInterval(id);
    }, []);
    return (
      <div className="w-80">
        <Progress value={n} max={19} tone="info" size="md" label="确认中" valueText={`${n}/19`} />
      </div>
    );
  },
};

export const Indeterminate: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-4">
      <Progress value={null} aria-label="Loading" />
      <Progress value={null} tone="up" size="xs" aria-label="Submitting" />
    </div>
  ),
};
