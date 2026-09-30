import type { Meta, StoryObj } from "@storybook/react-vite";
import { PositionCard, type Position } from "./PositionCard";

const long: Position = {
  symbol: "BTC-USDT-PERP",
  side: "LONG",
  quantity: "0.25",
  entryPrice: "62810.5",
  markPrice: "63214.5",
  liquidationPrice: "51020",
  margin: "785.13",
  leverage: 20,
  unrealizedPnl: "101",
  roe: "0.1286",
  marginMode: "ISOLATED",
};

const short: Position = {
  symbol: "ETH-USDT-PERP",
  side: "SHORT",
  quantity: "3.2",
  entryPrice: "2580.11",
  markPrice: "2614.32",
  liquidationPrice: "3120.4",
  margin: "825.64",
  leverage: 10,
  unrealizedPnl: "-109.47",
  roe: "-0.1326",
  marginMode: "CROSS",
};

const meta = {
  title: "Trading/PositionCard",
  component: PositionCard,
  args: { position: long, priceDecimals: 1, qtyDecimals: 3, base: "BTC", onClose: () => {}, onTpSl: () => {}, onAdjustMargin: () => {} },
} satisfies Meta<typeof PositionCard>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Long: Story = { render: (args) => <PositionCard {...args} className="w-[420px]" /> };

export const Short: Story = {
  render: () => <PositionCard position={short} priceDecimals={2} qtyDecimals={3} base="ETH" onClose={() => {}} onTpSl={() => {}} className="w-[420px]" />,
};
