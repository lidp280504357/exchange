import type { Meta, StoryObj } from "@storybook/react-vite";
import { Info } from "lucide-react";
import { Badge } from "./Badge";
import { IconButton } from "./IconButton";
import { Tooltip } from "./Tooltip";

const meta = {
  title: "Base/Tooltip",
  component: Tooltip,
  args: { content: "资金费每 8 小时结算一次", children: <span /> },
} satisfies Meta<typeof Tooltip>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Basic: Story = {
  render: (args) => (
    <div className="flex items-center gap-6 p-10">
      <Tooltip content={args.content}>
        <IconButton icon={<Info />} label="Funding" />
      </Tooltip>
      <Tooltip content="PARTIALLY_FILLED" side="bottom">
        <span tabIndex={0}>
          <Badge tone="info">部分成交</Badge>
        </span>
      </Tooltip>
      <Tooltip content="0x3f5ce5fbfe3e9af3971dd833d26ba9b5c936f0be" side="right">
        <span tabIndex={0} className="max-w-32 truncate text-sm text-fg-2">
          0x3f5ce5fbfe3e9af3971dd833d26ba9b5c936f0be
        </span>
      </Tooltip>
    </div>
  ),
};
