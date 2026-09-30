import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { CoinIcon } from "./CoinIcon";
import { Select } from "./Select";

const networks = [
  { value: "TRC20", label: "TRON (TRC20)" },
  { value: "BEP20", label: "BNB Smart Chain (BEP20)" },
  { value: "ERC20", label: "Ethereum (ERC20)" },
  { value: "SOL", label: "Solana", disabled: true },
];

const meta = {
  title: "Base/Select",
  component: Select,
  args: { options: networks, placeholder: "选择网络", "aria-label": "Network" },
  decorators: [(Story) => <div className="w-72"><Story /></div>],
} satisfies Meta<typeof Select>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {
  render: (args) => {
    const [v, setV] = useState<string>();
    return <Select {...args} value={v} onValueChange={setV} className="w-full" />;
  },
};

export const WithIcons: Story = {
  render: () => {
    const [v, setV] = useState("USDT");
    const coins = ["USDT", "BTC", "ETH"].map((c) => ({ value: c, label: c, icon: <CoinIcon symbol={c} size={18} /> }));
    return <Select options={coins} value={v} onValueChange={setV} className="w-full" aria-label="Coin" />;
  },
};

/** The toolbar look: order book step, chart interval. */
export const Ghost: Story = {
  render: () => {
    const [v, setV] = useState("0.1");
    return (
      <div className="flex items-center gap-4">
        <Select size="xs" variant="ghost" value={v} onValueChange={setV} options={["0.01", "0.1", "1", "10"].map((s) => ({ value: s, label: s }))} aria-label="Step" />
        <Select size="sm" invalid options={networks} placeholder="Invalid" aria-label="Invalid" />
        <Select size="sm" disabled options={networks} placeholder="Disabled" aria-label="Disabled" />
      </div>
    );
  },
};
