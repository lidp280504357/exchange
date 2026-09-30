import type { Meta, StoryObj } from "@storybook/react-vite";
import { Plus } from "lucide-react";
import { Button } from "./Button";

const meta = {
  title: "Base/Button",
  component: Button,
  args: { children: "Button" },
} satisfies Meta<typeof Button>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Variants: Story = {
  render: (args) => (
    <div className="flex flex-wrap gap-3">
      <Button {...args} variant="primary">Primary</Button>
      <Button {...args} variant="secondary">Secondary</Button>
      <Button {...args} variant="ghost">Ghost</Button>
      <Button {...args} variant="danger">Danger</Button>
      <Button {...args} variant="buy">Buy BTC</Button>
      <Button {...args} variant="sell">Sell BTC</Button>
    </div>
  ),
};

export const Sizes: Story = {
  render: (args) => (
    <div className="flex items-center gap-3">
      <Button {...args} size="sm">Small</Button>
      <Button {...args} size="md">Medium</Button>
      <Button {...args} size="lg">Large</Button>
    </div>
  ),
};

export const States: Story = {
  render: (args) => (
    <div className="flex items-center gap-3">
      <Button {...args} loading>Submitting</Button>
      <Button {...args} disabled>Disabled</Button>
      <Button {...args} icon={<Plus size={16} />}>With icon</Button>
      <div className="w-64">
        <Button {...args} block>Block</Button>
      </div>
    </div>
  ),
};
