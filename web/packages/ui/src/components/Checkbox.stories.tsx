import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Checkbox } from "./Checkbox";

const meta = { title: "Base/Checkbox", component: Checkbox, args: { label: "我已阅读并同意用户协议" } } satisfies Meta<typeof Checkbox>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {
  render: (args) => {
    const [on, setOn] = useState(false);
    return <Checkbox {...args} checked={on} onCheckedChange={setOn} />;
  },
};

export const States: Story = {
  render: () => (
    <div className="flex flex-col gap-3">
      <Checkbox label="隐藏小额资产" description="估值低于 1 USDT 的资产不显示" defaultChecked />
      <Checkbox label="部分选中" checked="indeterminate" />
      <Checkbox label="必须勾选" invalid />
      <Checkbox label="已禁用" disabled defaultChecked />
      <Checkbox aria-label="Bare checkbox" />
    </div>
  ),
};
