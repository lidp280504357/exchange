import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "./Button";
import { Dialog, DialogClose } from "./Dialog";
import { Input } from "./Input";

const meta = {
  title: "Feedback/Dialog",
  component: Dialog,
  args: { title: "确认下单" },
} satisfies Meta<typeof Dialog>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Order confirmation: the default cancel/confirm footer. */
export const Confirm: Story = {
  render: (args) => {
    const [open, setOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>打开</Button>
        <Dialog
          {...args}
          open={open}
          onOpenChange={setOpen}
          description="限价买入 BTC/USDT"
          confirmVariant="buy"
          confirmText="买入 BTC"
          confirmLoading={busy}
          onConfirm={() => {
            setBusy(true);
            setTimeout(() => {
              setBusy(false);
              setOpen(false);
            }, 900);
          }}
        >
          <dl className="grid grid-cols-2 gap-y-2 text-sm">
            <dt className="text-fg-3">价格</dt>
            <dd className="text-right tabular-nums">63,214.50 USDT</dd>
            <dt className="text-fg-3">数量</dt>
            <dd className="text-right tabular-nums">0.0150 BTC</dd>
            <dt className="text-fg-3">金额</dt>
            <dd className="text-right tabular-nums">948.22 USDT</dd>
          </dl>
        </Dialog>
      </>
    );
  },
};

/** A trigger and a custom footer; outside clicks do not close it. */
export const CustomFooter: Story = {
  render: () => (
    <Dialog
      trigger={<Button variant="secondary">修改昵称</Button>}
      title="修改昵称"
      persistent
      footer={
        <>
          <DialogClose asChild>
            <Button variant="ghost">稍后</Button>
          </DialogClose>
          <DialogClose asChild>
            <Button>保存</Button>
          </DialogClose>
        </>
      }
    >
      <Input placeholder="2–20 个字符" />
    </Dialog>
  ),
};
