import type { Meta, StoryObj } from "@storybook/react-vite";
import { Search } from "lucide-react";
import { useState } from "react";
import { Input } from "./Input";

const meta = {
  title: "Base/Input",
  component: Input,
  args: { placeholder: "输入内容" },
  decorators: [(Story) => <div className="w-80"><Story /></div>],
} satisfies Meta<typeof Input>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {};

export const Sizes: Story = {
  render: (args) => (
    <div className="flex flex-col gap-3">
      <Input {...args} size="sm" placeholder="Small" />
      <Input {...args} size="md" placeholder="Medium" />
      <Input {...args} size="lg" placeholder="Large" />
    </div>
  ),
};

export const Slots: Story = {
  render: () => {
    const [q, setQ] = useState("BTC");
    return (
      <div className="flex flex-col gap-3">
        <Input prefix={<Search size={14} />} value={q} onValueChange={setQ} clearable placeholder="搜索币种" />
        <Input prefix="价格" unit="USDT" defaultValue="63214.5" className="text-right" />
        <Input placeholder="地址" suffix={<button type="button" className="px-2 text-xs text-brand">粘贴</button>} />
      </div>
    );
  },
};

export const States: Story = {
  render: () => (
    <div className="flex flex-col gap-4">
      <Input defaultValue="0x12" error="地址格式不正确" />
      <Input placeholder="至少 10 位" hint="避免常见密码与连续字符" />
      <Input defaultValue="只读内容" readOnly />
      <Input defaultValue="已禁用" disabled />
    </div>
  ),
};
