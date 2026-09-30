import type { Meta, StoryObj } from "@storybook/react-vite";
import { Avatar } from "./Avatar";

const meta = { title: "Base/Avatar", component: Avatar, args: { name: "Ada Lovelace" } } satisfies Meta<typeof Avatar>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Initials: Story = {
  render: () => (
    <div className="flex items-center gap-3">
      <Avatar name="Ada Lovelace" />
      <Avatar name="alice@example.com" />
      <Avatar name="张三" />
      <Avatar name="ops-bob" size={40} />
      <Avatar size={48} />
    </div>
  ),
};

/** A broken picture falls back to the initials. */
export const Fallback: Story = {
  args: { src: "/does-not-exist.png", size: 48 },
};
