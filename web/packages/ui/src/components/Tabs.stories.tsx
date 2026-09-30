import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Button } from "./Button";
import { Tabs, TabsPanel } from "./Tabs";

const meta = {
  title: "Base/Tabs",
  component: Tabs,
  args: {
    items: [
      { value: "open", label: "当前委托", count: 3 },
      { value: "history", label: "历史委托" },
      { value: "fills", label: "成交明细" },
      { value: "assets", label: "资产" },
    ],
  },
} satisfies Meta<typeof Tabs>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Underline: terminal panels, with content panels and an extra action. */
export const Underline: Story = {
  render: (args) => (
    <div className="w-[640px]">
      <Tabs
        {...args}
        variant="underline"
        extra={
          <Button size="sm" variant="ghost">
            全部撤单
          </Button>
        }
      >
        <TabsPanel value="open" className="py-4 text-sm text-fg-2">当前委托列表</TabsPanel>
        <TabsPanel value="history" className="py-4 text-sm text-fg-2">历史委托列表</TabsPanel>
        <TabsPanel value="fills" className="py-4 text-sm text-fg-2">成交明细列表</TabsPanel>
        <TabsPanel value="assets" className="py-4 text-sm text-fg-2">资产列表</TabsPanel>
      </Tabs>
    </div>
  ),
};

/** Pill: categories and filters (a bar without panels). */
export const Pill: Story = {
  render: () => {
    const [v, setV] = useState("all");
    return (
      <div className="flex flex-col gap-3">
        <Tabs
          variant="pill"
          size="sm"
          value={v}
          onValueChange={setV}
          aria-label="Category"
          items={[
            { value: "all", label: "全部" },
            { value: "fav", label: "自选" },
            { value: "spot", label: "现货" },
            { value: "futures", label: "合约" },
            { value: "new", label: "新上线" },
          ]}
        />
        <span className="text-xs text-fg-3">value: {v}</span>
      </div>
    );
  },
};

export const Block: Story = {
  render: () => (
    <div className="w-80">
      <Tabs
        block
        items={[
          { value: "chart", label: "图表" },
          { value: "book", label: "盘口" },
          { value: "trades", label: "成交" },
        ]}
      />
    </div>
  ),
};
