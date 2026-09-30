import type { Meta, StoryObj } from "@storybook/react-vite";
import { CoinIcon } from "./CoinIcon";

const top = [
  "BTC", "ETH", "USDT", "BNB", "SOL", "XRP", "DOGE", "ADA", "TRX", "AVAX", "LINK", "DOT", "TON", "SHIB", "LTC", "BCH", "UNI", "NEAR",
  "APT", "ICP", "1000PEPE", "ETC", "FIL", "ARB", "OP", "ATOM", "HBAR", "INJ", "SUI", "SEI",
];

const meta = { title: "Base/CoinIcon", component: CoinIcon, args: { symbol: "BTC", size: 32 } } satisfies Meta<typeof CoinIcon>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Single: Story = {};

/** Letter icons of the top coins: colours are stable per symbol. */
export const Grid: Story = {
  render: () => (
    <div className="grid grid-cols-6 gap-4">
      {top.map((s) => (
        <div key={s} className="flex items-center gap-2 text-sm">
          <CoinIcon symbol={s} size={28} label={s} />
          <span>{s}</span>
        </div>
      ))}
    </div>
  ),
};

export const Sizes: Story = {
  render: () => (
    <div className="flex items-end gap-3">
      {[16, 20, 24, 32, 40, 56].map((n) => (
        <CoinIcon key={n} symbol="ETH" size={n} />
      ))}
    </div>
  ),
};
