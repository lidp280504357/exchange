import type { Meta, StoryObj } from "@storybook/react-vite";
import { ChangeBadge } from "../components/Badge";
import { CoinIcon } from "../components/CoinIcon";
import { PriceText } from "../components/PriceText";
import { Marquee } from "./Marquee";

const tickers = [
  { s: "BTC", p: "63214.5", c: "0.0231" },
  { s: "ETH", p: "2614.32", c: "-0.0112" },
  { s: "BNB", p: "581.4", c: "0.0056" },
  { s: "SOL", p: "154.21", c: "0.0412" },
  { s: "XRP", p: "0.5832", c: "-0.0031" },
  { s: "DOGE", p: "0.1162", c: "0.0721" },
  { s: "ADA", p: "0.3551", c: "-0.0205" },
  { s: "TRX", p: "0.1561", c: "0.0012" },
  { s: "AVAX", p: "27.14", c: "0.0187" },
  { s: "LINK", p: "11.62", c: "-0.0098" },
];

const meta = { title: "Data/Marquee", component: Marquee, args: { children: null } } satisfies Meta<typeof Marquee>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The home page ticker: hover pauses it. */
export const Tickers: Story = {
  render: () => (
    <div className="w-[900px] border-y border-line-1 py-3">
      <Marquee aria-label="Top 10">
        {tickers.map((t) => (
          <a key={t.s} href={`/trade/${t.s}-USDT`} className="flex items-center gap-2 text-sm">
            <CoinIcon symbol={t.s} size={18} />
            <span className="font-medium">{t.s}</span>
            <PriceText value={t.p} />
            <ChangeBadge value={t.c} variant="soft" />
          </a>
        ))}
      </Marquee>
    </div>
  ),
};

export const Slow: Story = {
  render: () => (
    <div className="w-[600px]">
      <Marquee duration={80} gap={48}>
        {["多链充提", "永续合约", "深度流动性", "安全"].map((w) => (
          <span key={w} className="text-lg text-fg-2">
            {w}
          </span>
        ))}
      </Marquee>
    </div>
  ),
};
