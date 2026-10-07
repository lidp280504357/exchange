import type { Liquidation } from "@exchange/core/futures/data";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { LiquidationTape } from "./LiquidationTape";

// The liquidation stream of the futures data board (design 2026-10-06
// §3.3): newest first, longs and shorts by colour and word, the value in
// USD. A row that arrives later slides in on top.

const at = Date.UTC(2026, 9, 6, 18, 3, 17);
const order = (i: number): Liquidation => ({
  symbol: "BTC-USDT-PERP",
  position_side: i % 3 ? "SHORT" : "LONG",
  price: (86_100 - i * 20).toFixed(1),
  average_price: (85_778 - i * 18).toFixed(1),
  quantity: (0.005 + i * 0.013).toFixed(3),
  value_usd: ((0.005 + i * 0.013) * 85_778).toFixed(4),
  traded_at: new Date(at - i * 41_000).toISOString(),
});
const items = Array.from({ length: 12 }, (_, i) => order(i));
const labels = { time: "时间", side: "方向", price: "均价", quantity: "数量(BTC)", value: "价值(USD)", long: "多单爆仓", short: "空单爆仓" };

const meta = {
  title: "Futures/LiquidationTape",
  component: LiquidationTape,
  args: { items, priceDecimals: 1, qtyDecimals: 3, labels, className: "w-[420px]" },
} satisfies Meta<typeof LiquidationTape>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Stream: Story = {};

/** A coin-margined contract: quantities in whole contracts. */
export const Contracts: Story = {
  args: {
    items: items.map((l, i) => ({ ...l, symbol: "BTC-USD-PERP", quantity: String(3 + i * 7), value_usd: String((3 + i * 7) * 100) })),
    qtyDecimals: 0,
    labels: { ...labels, quantity: "数量(张)" },
  },
};

/** New orders arrive on top every two seconds; the list keeps 12. */
export const Arriving: Story = {
  render: (args) => {
    const [list, setList] = useState(items);
    useEffect(() => {
      let n = 0;
      const id = setInterval(() => {
        n += 1;
        setList((l) => [{ ...order(n % 5), traded_at: new Date(Date.now()).toISOString(), average_price: (85_800 + n).toFixed(1) }, ...l].slice(0, 12));
      }, 2000);
      return () => clearInterval(id);
    }, []);
    return <LiquidationTape {...args} items={list} />;
  },
};

/** No liquidations in the last day: the board says so around it. */
export const Empty: Story = { args: { items: [] } };
