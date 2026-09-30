import { useSettings } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { Eye, EyeOff } from "lucide-react";
import { IconButton } from "../components/IconButton";
import { AmountText } from "./AmountText";

const meta = { title: "Data/AmountText", component: AmountText, args: { value: "1234.56789", decimals: 2, asset: "USDT" } } satisfies Meta<typeof AmountText>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {};

export const Variants: Story = {
  render: () => (
    <div className="flex flex-col gap-2 text-base">
      <AmountText value="0.123456789" decimals={8} asset="BTC" />
      <AmountText value="1234567.891" decimals={2} asset="USDT" />
      <AmountText value="98765432.1" compact asset="USDT" />
      <AmountText value="263.2" decimals={2} sign tone="auto" asset="USDT" />
      <AmountText value="-41.07" decimals={2} sign tone="auto" asset="USDT" />
      <AmountText value={null} asset="USDT" />
    </div>
  ),
};

/** The eye toggle hides every amount on the page (settings.hideAmounts). */
export const Hidden: Story = {
  render: () => {
    const hidden = useSettings((s) => s.hideAmounts);
    const set = useSettings((s) => s.set);
    return (
      <div className="flex items-center gap-3 text-lg">
        <AmountText value="12345.67" decimals={2} asset="USDT" />
        <IconButton icon={hidden ? <EyeOff /> : <Eye />} label={hidden ? "Show amounts" : "Hide amounts"} onClick={() => set({ hideAmounts: !hidden })} />
      </div>
    );
  },
};
