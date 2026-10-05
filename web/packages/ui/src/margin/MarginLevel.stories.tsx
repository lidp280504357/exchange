import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { MarginLevel } from "./MarginLevel";

const meta = {
  title: "Margin/MarginLevel",
  component: MarginLevel,
  args: { level: "1.85", warn: "1.3", liquidation: "1.1" },
} satisfies Meta<typeof MarginLevel>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The cross account (warning 1.30, liquidation 1.10) in each zone. */
export const Zones: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-6">
      <MarginLevel level={null} warn="1.3" liquidation="1.1" />
      <MarginLevel level="2.4" warn="1.3" liquidation="1.1" />
      <MarginLevel level="1.52" warn="1.3" liquidation="1.1" />
      <MarginLevel level="1.18" warn="1.3" liquidation="1.1" />
    </div>
  ),
};

/** An isolated 10x account: the caution band is narrow (1.10 to 1.20). */
export const Isolated10x: Story = { args: { level: "1.16", warn: "1.1", liquidation: "1.05" } };

/** Small, without the thresholds: an account row. */
export const Compact: Story = { args: { level: "1.4", size: "sm", compact: true } };

/** A price falling: the level slides down through the zones. */
export const Live: Story = {
  render: () => {
    const [level, setLevel] = useState(2.6);
    useEffect(() => {
      const id = setInterval(() => setLevel((v) => (v <= 1.12 ? 2.6 : Math.round((v - 0.07) * 100) / 100)), 600);
      return () => clearInterval(id);
    }, []);
    return (
      <div className="w-80">
        <MarginLevel level={String(level)} warn="1.3" liquidation="1.1" />
      </div>
    );
  },
};
