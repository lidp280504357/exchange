import type { Meta, StoryObj } from "@storybook/react-vite";
import { useEffect, useState } from "react";
import { PriceText } from "./PriceText";

const meta = { title: "Data/PriceText", component: PriceText, args: { value: "63214.5", decimals: 2 } } satisfies Meta<typeof PriceText>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Static: Story = {};

export const Tones: Story = {
  render: () => (
    <div className="flex gap-6 text-lg">
      <PriceText value="63214.5" decimals={2} change="0.0231" />
      <PriceText value="63214.5" decimals={2} change="-0.0112" />
      <PriceText value="63214.5" decimals={2} />
    </div>
  ),
};

function useLivePrice() {
  const [p, setP] = useState(63214.5);
  useEffect(() => {
    const id = setInterval(() => setP((v) => Math.round((v + (Math.random() - 0.5) * 20) * 10) / 10), 900);
    return () => clearInterval(id);
  }, []);
  return p.toFixed(1);
}

/** A live price in a list: it flashes on every move. */
export const Live: Story = {
  render: () => <PriceText value={useLivePrice()} decimals={1} change="0.01" />,
};

/** A headline price (trade page bar, coin page): no flash, an arrow for the last move. */
export const Headline: Story = {
  render: () => <PriceText value={useLivePrice()} decimals={1} className="text-2xl font-semibold" change="0.01" flash={false} arrow />,
};
