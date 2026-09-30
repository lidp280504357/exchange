import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "../components/Button";
import { TpSlDialog } from "./TpSlDialog";
import type { TpSlValues } from "./tpsl";

const meta = {
  title: "Trading/TpSlDialog",
  component: TpSlDialog,
  args: { open: false, onOpenChange: () => {}, side: "LONG", priceDecimals: 1, onConfirm: () => {} },
} satisfies Meta<typeof TpSlDialog>;
export default meta;

type Story = StoryObj<typeof meta>;

/** A long: take profit must be above, stop loss below the watched price. */
export const Long: Story = {
  render: () => {
    const [open, setOpen] = useState(false);
    const [saved, setSaved] = useState<TpSlValues>();
    return (
      <div className="flex flex-col items-start gap-2">
        <Button variant="secondary" onClick={() => setOpen(true)}>
          止盈止损
        </Button>
        <span className="text-xs text-fg-3">{saved ? JSON.stringify(saved) : "—"}</span>
        <TpSlDialog
          open={open}
          onOpenChange={setOpen}
          side="LONG"
          symbol="BTC-USDT-PERP"
          entryPrice="62810.5"
          quantity="0.25"
          markPrice="63214.5"
          lastPrice="63220"
          priceDecimals={1}
          initial={saved}
          onConfirm={(v) => {
            setSaved(v);
            setOpen(false);
          }}
        />
      </div>
    );
  },
};

/** A short, reversed rules; try a take profit above the mark to see the error. */
export const Short: Story = {
  render: () => {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button variant="secondary" onClick={() => setOpen(true)}>
          止盈止损（空）
        </Button>
        <TpSlDialog
          open={open}
          onOpenChange={setOpen}
          side="SHORT"
          entryPrice="2580.11"
          quantity="3.2"
          markPrice="2614.32"
          lastPrice="2615.01"
          priceDecimals={2}
          initial={{ takeProfit: { price: "2700", triggerType: "MARK" } }}
          onConfirm={() => setOpen(false)}
        />
      </>
    );
  },
};
