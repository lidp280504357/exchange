import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "./Button";
import { Stepper } from "./Stepper";

const meta = {
  title: "Base/Stepper",
  component: Stepper,
  args: {
    current: 1,
    steps: [{ title: "选择币种" }, { title: "选择网络" }, { title: "充值地址" }],
  },
} satisfies Meta<typeof Stepper>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The deposit page's three steps; done steps can be revisited. */
export const Horizontal: Story = {
  render: (args) => {
    const [cur, setCur] = useState(1);
    return (
      <div className="flex w-[560px] flex-col gap-6">
        <Stepper {...args} current={cur} onStepClick={setCur} />
        <div className="flex gap-2">
          <Button size="sm" variant="secondary" onClick={() => setCur((c) => Math.max(0, c - 1))}>
            上一步
          </Button>
          <Button size="sm" onClick={() => setCur((c) => Math.min(3, c + 1))}>
            下一步
          </Button>
        </div>
      </div>
    );
  },
};

/** A withdrawal's status timeline, with a failed step. */
export const Timeline: Story = {
  render: () => (
    <div className="flex gap-16">
      <Stepper
        orientation="vertical"
        size="sm"
        current={3}
        steps={[
          { title: "风控", description: "10:12:03" },
          { title: "审核", description: "10:12:05" },
          { title: "签名", description: "10:14:40" },
          { title: "广播", description: "等待上链" },
          { title: "确认" },
          { title: "完成" },
        ]}
      />
      <Stepper
        orientation="vertical"
        current={2}
        steps={[
          { title: "已检测", description: "10:12:03" },
          { title: "确认中", description: "19/19" },
          { title: "入账失败", description: "托管方回调签名错误，等待人工处理", status: "error" },
        ]}
      />
    </div>
  ),
};
