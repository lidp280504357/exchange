import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { FundingCountdown } from "./FundingCountdown";

const meta = {
  title: "Trading/FundingCountdown",
  component: FundingCountdown,
  args: { nextFundingTime: "2026-09-30T16:00:00Z", rate: "0.0001" },
} satisfies Meta<typeof FundingCountdown>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Only this element re-renders each second. */
export const Ticking: Story = {
  render: () => {
    const [next] = useState(() => new Date(Date.now() + 2 * 3600_000 + 13 * 60_000 + 45_000).toISOString());
    return (
      <div className="flex flex-col gap-4">
        <FundingCountdown nextFundingTime={next} rate="0.0001" />
        <FundingCountdown nextFundingTime={next} rate="-0.000253" layout="inline" />
        <FundingCountdown nextFundingTime={null} rate={null} />
      </div>
    );
  },
};
