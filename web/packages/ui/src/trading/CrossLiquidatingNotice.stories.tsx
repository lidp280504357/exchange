import type { Meta, StoryObj } from "@storybook/react-vite";
import { CrossLiquidatingNotice } from "./CrossLiquidatingNotice";

const meta = {
  title: "Trading/CrossLiquidatingNotice",
  component: CrossLiquidatingNotice,
  args: { asset: "USDT", stops: "orders" },
} satisfies Meta<typeof CrossLiquidatingNotice>;
export default meta;

type Story = StoryObj<typeof meta>;

/** While an account's cross positions are being liquidated (C68, F24): the order panel, the isolated margin dialog, the transfer page. */
export const Places: Story = {
  render: () => (
    <div className="flex w-[320px] flex-col gap-3">
      <CrossLiquidatingNotice asset="USDT" stops="orders" />
      <CrossLiquidatingNotice asset="BTC" stops="margin" />
      <CrossLiquidatingNotice asset="USDT" stops="transfer" />
    </div>
  ),
};
