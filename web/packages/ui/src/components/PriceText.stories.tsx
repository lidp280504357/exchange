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

/** A live price: it flashes on every move. */
export const Live: Story = {
  render: () => {
    const [p, setP] = useState(63214.5);
    useEffect(() => {
      const id = setInterval(() => setP((v) => Math.round((v + (Math.random() - 0.5) * 20) * 10) / 10), 900);
      return () => clearInterval(id);
    }, []);
    return <PriceText value={p.toFixed(1)} decimals={1} className="text-2xl" change="0.01" />;
  },
};
