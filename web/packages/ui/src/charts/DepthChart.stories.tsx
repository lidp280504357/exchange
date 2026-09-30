import type { BookView } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { fakeMarket, tick } from "../fixtures";
import { DepthChart } from "./DepthChart";

const empty: BookView = { bids: [], asks: [], maxTotal: "0", spread: null, seq: 0 };

const meta = {
  title: "Charts/DepthChart",
  component: DepthChart,
  args: { view: empty, priceDecimals: 1, qtyDecimals: 4 },
} satisfies Meta<typeof DepthChart>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Cumulative depth, updated twice a second; hover for price and total. */
export const Live: Story = {
  render: () => {
    const [m] = useState(() => fakeMarket(60));
    const [view, setView] = useState(() => m.book.view(40));
    useEffect(() => {
      const id = setInterval(() => {
        tick(m);
        setView(m.book.view(40));
      }, 500);
      return () => clearInterval(id);
    }, [m]);
    return (
      <div className="w-[640px] rounded-2 border border-line-1">
        <DepthChart view={view} priceDecimals={1} qtyDecimals={4} base="BTC" height={260} />
      </div>
    );
  },
};

export const Empty: Story = {
  render: () => (
    <div className="w-[640px] rounded-2 border border-line-1">
      <DepthChart view={empty} priceDecimals={1} qtyDecimals={4} />
    </div>
  ),
};
