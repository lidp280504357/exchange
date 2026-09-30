import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "../components/Button";
import { LeverageDialog } from "./LeverageDialog";

const meta = {
  title: "Trading/LeverageDialog",
  component: LeverageDialog,
  args: { open: false, onOpenChange: () => {}, value: 10, max: 125, onConfirm: () => {} },
} satisfies Meta<typeof LeverageDialog>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Above 20× the risk warning appears. */
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
          max={125}
          symbol="BTC-USDT-PERP"
          info={(n) => `当前杠杆最大可开 ${n <= 20 ? "5,000,000" : n <= 50 ? "1,000,000" : "250,000"} USDT`}
          onConfirm={(n) => {
            setLev(n);
            setOpen(false);
          }}
        />
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
