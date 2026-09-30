import type { Meta, StoryObj } from "@storybook/react-vite";
import { SlidersHorizontal } from "lucide-react";
import { Button } from "./Button";
import { Checkbox } from "./Checkbox";
import { IconButton } from "./IconButton";
import { Popover, PopoverClose } from "./Popover";

const meta = {
  title: "Base/Popover",
  component: Popover,
  args: { trigger: <Button size="sm">打开</Button>, children: "内容" },
} satisfies Meta<typeof Popover>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {};

/** Filter settings in a popover, closed from inside. */
export const Filters: Story = {
  render: () => (
    <div className="p-6">
      <Popover trigger={<IconButton icon={<SlidersHorizontal />} label="Filters" variant="outline" />} align="start" arrow className="w-64">
        <div className="flex flex-col gap-3">
          <div className="text-sm font-medium">显示设置</div>
          <Checkbox label="隐藏其他交易对" defaultChecked />
          <Checkbox label="显示深度图" />
          <div className="flex justify-end">
            <PopoverClose asChild>
              <Button size="sm">完成</Button>
            </PopoverClose>
          </div>
        </div>
      </Popover>
    </div>
  ),
};
