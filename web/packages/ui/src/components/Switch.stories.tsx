import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Switch } from "./Switch";

const meta = { title: "Base/Switch", component: Switch, args: { label: "下单确认" } } satisfies Meta<typeof Switch>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {
  render: (args) => {
    const [on, setOn] = useState(true);
    return <Switch {...args} checked={on} onCheckedChange={setOn} />;
  },
};

/** A settings row: label first, switch at the right. */
export const SettingsRow: Story = {
  render: () => (
    <div className="flex w-80 flex-col gap-4">
      <Switch labelFirst label="下单确认" description="提交订单前弹出确认窗口" defaultChecked />
      <Switch labelFirst label="隐藏小额资产" size="sm" />
      <Switch labelFirst label="已禁用" disabled />
    </div>
  ),
};
