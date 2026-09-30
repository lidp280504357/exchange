import type { Meta, StoryObj } from "@storybook/react-vite";
import { LogOut, Settings, Shield, User } from "lucide-react";
import { useState } from "react";
import { Avatar } from "./Avatar";
import { Button } from "./Button";
import { DropdownMenu } from "./DropdownMenu";

const meta = {
  title: "Base/DropdownMenu",
  component: DropdownMenu,
  args: { trigger: <Button size="sm">菜单</Button>, items: [] },
} satisfies Meta<typeof DropdownMenu>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The user menu of the top bar. */
export const UserMenu: Story = {
  render: () => {
    const [depth, setDepth] = useState(true);
    return (
      <div className="flex justify-end p-4">
        <DropdownMenu
          trigger={
            <button type="button" aria-label="Account" className="rounded-full">
              <Avatar name="Ada Lovelace" />
            </button>
          }
          items={[
            { type: "label", key: "who", label: "ada@example.com" },
            { key: "account", label: "账户", icon: <User /> },
            { key: "security", label: "安全", icon: <Shield />, shortcut: "⌘S" },
            { key: "settings", label: "设置", icon: <Settings /> },
            { type: "separator", key: "s1" },
            { type: "checkbox", key: "depth", label: "显示深度图", checked: depth, onCheckedChange: setDepth },
            { type: "separator", key: "s2" },
            { key: "logout", label: "退出登录", icon: <LogOut />, danger: true },
          ]}
        />
      </div>
    );
  },
};
