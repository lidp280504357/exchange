import { dec, formatPrice } from "@exchange/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "./Button";
import { NumberInput } from "./NumberInput";
import { Segmented } from "./Segmented";
import { Sheet } from "./Sheet";

const meta = {
  title: "Feedback/Sheet",
  component: Sheet,
  args: { title: "买入 BTC" },
  parameters: { viewport: { defaultViewport: "mobile1" } },
} satisfies Meta<typeof Sheet>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The mobile order sheet: drag the handle down (or flick) to close. */
export const OrderSheet: Story = {
  render: (args) => {
    const [open, setOpen] = useState(false);
    const [type, setType] = useState("limit");
    const [price, setPrice] = useState("63214.5");
    const [qty, setQty] = useState("");
    return (
      <div className="w-[390px]">
        <Button variant="buy" block onClick={() => setOpen(true)}>
          买入 BTC
        </Button>
        <Sheet
          {...args}
          open={open}
          onOpenChange={setOpen}
          closeButton
          footer={
            <Button variant="buy" size="lg" block onClick={() => setOpen(false)}>
              买入 BTC
            </Button>
          }
        >
          <div className="flex flex-col gap-3">
            <Segmented
              block
              value={type}
              onValueChange={setType}
              items={[
                { value: "limit", label: "限价" },
                { value: "market", label: "市价" },
              ]}
            />
            <NumberInput value={price} onValueChange={setPrice} step="0.1" prefix="价格" unit="USDT" align="right" size="lg" />
            <NumberInput value={qty} onValueChange={setQty} step="0.0001" max="0.0158" slider sliderTone="up" prefix="数量" unit="BTC" align="right" size="lg" />
            <div className="flex justify-between text-sm text-fg-3">
              <span>可用</span>
              <span className="tabular-nums text-fg-1">1,000.00 USDT</span>
            </div>
          </div>
        </Sheet>
      </div>
    );
  },
};

/** Tall content scrolls inside; the sheet stops at 90% of the screen. */
export const LongContent: Story = {
  render: () => (
    <Sheet trigger={<Button variant="secondary">选择交易对</Button>} title="选择交易对">
      <ul className="flex flex-col">
        {Array.from({ length: 40 }, (_, i) => (
          <li key={`pair-${i}`} className="flex h-12 items-center justify-between border-b border-line-1 text-sm">
            <span>COIN{i + 1}/USDT</span>
            <span className="text-fg-3">{formatPrice(dec.div(String(i * 137 + 100), "100", 2), 2)}</span>
          </li>
        ))}
      </ul>
    </Sheet>
  ),
};
