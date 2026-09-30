import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "./Button";
import { ConfirmDialog } from "./ConfirmDialog";
import { Stepper } from "./Stepper";

const userId = "0192e4c7-5b1a-7c3e-9f00-6d2b8e11a3f4";

const meta = {
  title: "Feedback/ConfirmDialog",
  component: ConfirmDialog,
  args: {
    open: false,
    onOpenChange: () => {},
    title: "冻结账户",
    target: userId,
    confirmWord: userId.slice(-4),
    onConfirm: () => {},
  },
} satisfies Meta<typeof ConfirmDialog>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Freezing a user: a reason of 10+ characters and the ID's last 4 characters. */
export const FreezeUser: Story = {
  render: (args) => {
    const [open, setOpen] = useState(false);
    const [log, setLog] = useState("");
    return (
      <div className="flex flex-col items-start gap-3">
        <Button variant="danger" onClick={() => setOpen(true)}>
          冻结
        </Button>
        {log && <span className="text-xs text-fg-3">{log}</span>}
        <ConfirmDialog
          {...args}
          open={open}
          onOpenChange={setOpen}
          description="冻结后该用户只能查看，不能交易或提现。"
          target={
            <span>
              用户 <code className="font-mono">{userId}</code>
            </span>
          }
          onConfirm={(reason) =>
            new Promise<void>((resolve) =>
              setTimeout(() => {
                setLog(`reason: ${reason}`);
                setOpen(false);
                resolve();
              }, 800),
            )
          }
        />
      </div>
    );
  },
};

/** A two-person approval shows its timeline under the target. */
export const Approval: Story = {
  render: (args) => {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>批准提现</Button>
        <ConfirmDialog
          {...args}
          open={open}
          onOpenChange={setOpen}
          danger={false}
          title="批准提现"
          target="提现 5,000 USDT（TRC20）至 TQn9Y2khEsLJW1ChVWFMSMeRDow5KcbLSE"
          confirmWord="LSE"
          confirmText="批准"
          onConfirm={() => setOpen(false)}
        >
          <Stepper
            orientation="vertical"
            size="sm"
            current={1}
            steps={[
              { title: "第一位审批人", description: "ops-alice · 2026-09-30 10:12" },
              { title: "第二位审批人", description: "等待你确认" },
              { title: "提交托管方" },
            ]}
          />
        </ConfirmDialog>
      </>
    );
  },
};
