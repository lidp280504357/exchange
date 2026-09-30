import type { TradeData } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { fakeMarket, fakeTrade, fakeTrades, tick } from "../fixtures";
import { TradeTape } from "./TradeTape";

const meta = {
  title: "Trading/TradeTape",
  component: TradeTape,
  args: { trades: [], priceDecimals: 1, qtyDecimals: 4 },
} satisfies Meta<typeof TradeTape>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Five trades a second: only the newest row slides in. */
export const Live: Story = {
  render: () => {
    const [m] = useState(() => fakeMarket());
    const [trades, setTrades] = useState<TradeData[]>(() => fakeTrades(m, 30, Date.now()));
    useEffect(() => {
      const id = setInterval(() => {
        tick(m);
        setTrades((list) => [fakeTrade(m), ...list.slice(0, 59)]);
      }, 200);
      return () => clearInterval(id);
    }, [m]);
    return (
      <div className="w-80 rounded-2 border border-line-1">
        <TradeTape trades={trades} priceDecimals={1} qtyDecimals={4} base="BTC" quote="USDT" />
      </div>
    );
  },
};

export const Loading: Story = {
  render: () => (
    <div className="w-80 rounded-2 border border-line-1">
      <TradeTape trades={[]} priceDecimals={1} qtyDecimals={4} max={12} loading />
    </div>
  ),
};
