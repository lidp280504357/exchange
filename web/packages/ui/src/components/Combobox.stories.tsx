import { useSettings } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { Search } from "lucide-react";
import { useState } from "react";
import { Button } from "./Button";
import { Combobox, coinItems } from "./Combobox";

const symbols = ["BTC", "ETH", "USDT", "BNB", "SOL", "XRP", "DOGE", "ADA", "TRX", "AVAX", "LINK", "DOT", "1000PEPE", "LTC", "SHIB", "UNI"];

const meta = {
  title: "Base/Combobox",
  component: Combobox,
  args: { items: [] },
} satisfies Meta<typeof Combobox>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Coin search: type a symbol or a name (比特币, Ether); ↑ ↓ Enter pick. */
export const CoinSearch: Story = {
  render: () => {
    const locale = useSettings((s) => s.locale);
    const [v, setV] = useState<string | null>("BTC");
    return (
      <div className="flex w-72 flex-col gap-2">
        <Combobox items={coinItems(symbols, locale)} value={v} onValueChange={setV} className="w-full" aria-label="Coin" />
        <span className="text-xs text-fg-3">value: {v}</span>
      </div>
    );
  },
};

/** A custom trigger and trailing content (deposits list only three coins). */
export const CustomTrigger: Story = {
  render: () => {
    const locale = useSettings((s) => s.locale);
    const items = coinItems(symbols, locale).map((i) => ({
      ...i,
      trailing: ["USDT", "BTC", "ETH"].includes(i.value) ? undefined : "仅站内交易",
      disabled: !["USDT", "BTC", "ETH"].includes(i.value),
    }));
    return (
      <Combobox
        items={items}
        onValueChange={() => {}}
        trigger={
          <Button variant="secondary" size="sm" icon={<Search size={14} />}>
            搜索币种
          </Button>
        }
      />
    );
  },
};
