import type { Meta, StoryObj } from "@storybook/react-vite";
import { Bell, Maximize2, Settings, Star } from "lucide-react";
import { useState } from "react";
import { IconButton } from "./IconButton";

const meta = {
  title: "Base/IconButton",
  component: IconButton,
  args: { icon: <Settings />, label: "Settings" },
} satisfies Meta<typeof IconButton>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Variants: Story = {
  render: (args) => (
    <div className="flex items-center gap-3">
      <IconButton {...args} variant="ghost" />
      <IconButton {...args} variant="secondary" />
      <IconButton {...args} variant="outline" />
      <IconButton {...args} variant="primary" />
      <IconButton {...args} round variant="secondary" icon={<Bell />} label="Notifications" />
      <IconButton {...args} disabled />
    </div>
  ),
};

export const Sizes: Story = {
  render: (args) => (
    <div className="flex items-center gap-3">
      <IconButton {...args} size="xs" icon={<Maximize2 />} label="Full screen" />
      <IconButton {...args} size="sm" />
      <IconButton {...args} size="md" />
      <IconButton {...args} size="lg" />
    </div>
  ),
};

/** A toggle: the favourite star keeps aria-pressed in step. */
export const Toggle: Story = {
  render: () => {
    const [on, setOn] = useState(false);
    return (
      <IconButton
        icon={<Star className={on ? "fill-brand" : undefined} />}
        label={on ? "Remove favorite" : "Favorite"}
        active={on}
        onClick={() => setOn((v) => !v)}
      />
    );
  },
};
