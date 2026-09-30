import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { RadioGroup } from "./RadioGroup";

const meta = {
  title: "Base/RadioGroup",
  component: RadioGroup,
  args: {
    options: [
      { value: "CROSS", label: "全仓" },
      { value: "ISOLATED", label: "逐仓" },
    ],
    "aria-label": "Margin mode",
  },
} satisfies Meta<typeof RadioGroup>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {
  render: (args) => {
    const [v, setV] = useState("CROSS");
    return <RadioGroup {...args} value={v} onValueChange={setV} orientation="horizontal" />;
  },
};

/** Cards: the network choice of deposits and withdrawals. */
export const Cards: Story = {
  render: () => {
    const [v, setV] = useState("TRC20");
    return (
      <RadioGroup
        variant="card"
        className="w-96"
        value={v}
        onValueChange={setV}
        aria-label="Network"
        options={[
          { value: "TRC20", label: "TRON (TRC20)", description: "19 次确认 · 最小充值 1 USDT · 约 3 分钟" },
          { value: "BEP20", label: "BNB Smart Chain (BEP20)", description: "15 次确认 · 最小充值 1 USDT · 约 1 分钟" },
          { value: "ERC20", label: "Ethereum (ERC20)", description: "12 次确认 · 最小充值 5 USDT · 约 5 分钟" },
          { value: "SOL", label: "Solana", description: "暂停充值", disabled: true },
        ]}
      />
    );
  },
};
