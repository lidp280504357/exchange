import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "../components/Button";
import { LeverageDialog } from "./LeverageDialog";

const meta = {
  title: "Trading/LeverageDialog",
  component: LeverageDialog,
  args: { open: false, onOpenChange: () => {}, value: 10, max: 150, onConfirm: () => {} },
} satisfies Meta<typeof LeverageDialog>;
export default meta;

type Story = StoryObj<typeof meta>;

/** BTC-USDT-PERP's first tier is 150× (Binance's, F28): marks 1, 30 … 150; above 20× the risk warning appears. */
export const Adjust: Story = {
  render: () => {
    const [open, setOpen] = useState(false);
    const [lev, setLev] = useState(10);
    return (
      <>
        <Button variant="secondary" onClick={() => setOpen(true)}>
          {lev}x
        </Button>
        <LeverageDialog
          open={open}
          onOpenChange={setOpen}
          value={lev}
          max={150}
          symbol="BTC-USDT-PERP"
          info={(n) => `当前杠杆最大可开 ${n <= 20 ? "5,000,000" : n <= 50 ? "1,000,000" : n <= 125 ? "600,000" : "300,000"} USDT`}
          onConfirm={(n) => {
            setLev(n);
            setOpen(false);
          }}
        />
      </>
    );
  },
};

/** A coin-margined contract's 125× (BTCUSD): marks 1, 25 … 125. */
export const CoinMargined: Story = {
  render: () => {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button variant="secondary" onClick={() => setOpen(true)}>
          BTC-USD-PERP 20x
        </Button>
        <LeverageDialog open={open} onOpenChange={setOpen} value={20} max={125} symbol="BTC-USD-PERP" onConfirm={() => setOpen(false)} />
      </>
    );
  },
};

export const SmallMax: Story = {
  render: () => {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button variant="secondary" onClick={() => setOpen(true)}>
          最大 10x
        </Button>
        <LeverageDialog open={open} onOpenChange={setOpen} value={3} max={10} onConfirm={() => setOpen(false)} />
      </>
    );
  },
};
