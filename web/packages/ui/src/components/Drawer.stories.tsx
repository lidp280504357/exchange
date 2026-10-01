import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { KeyValue } from "../data/KeyValue";
import { Badge } from "./Badge";
import { Button } from "./Button";
import { Drawer } from "./Drawer";

const meta = {
  title: "Feedback/Drawer",
  component: Drawer,
  args: { open: false, onOpenChange: () => {}, title: "用户详情" },
} satisfies Meta<typeof Drawer>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The console's detail panel: a user opened from the list. */
export const UserDetail: Story = {
  render: (args) => {
    const [open, setOpen] = useState(false);
    return (
      <>
        <Button onClick={() => setOpen(true)}>打开</Button>
        <Drawer
          {...args}
          open={open}
          onOpenChange={setOpen}
          description="0192a1b2-3c4d-7e5f-8a9b-0c1d2e3f4a5b"
          actions={<Badge tone="success">正常</Badge>}
        >
          <KeyValue
            items={[
              { label: "地区", value: "SG" },
              { label: "语言", value: "zh-CN" },
              { label: "KYC 等级", value: "0" },
              { label: "注册时间", value: "2026-10-01 08:00:00" },
            ]}
          />
        </Drawer>
      </>
    );
  },
};
