import type { Locale } from "@exchange/core/settings/store";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { LanguageSelect } from "./LanguageSelect";

const meta = {
  title: "Form/LanguageSelect",
  component: LanguageSelect,
  args: { value: "zh-CN", onValueChange: () => {}, "aria-label": "语言" },
} satisfies Meta<typeof LanguageSelect>;
export default meta;

type Story = StoryObj<typeof meta>;

/** The settings' language (F30): each language by its own name, its English name beside it in the list. */
export const Settings: Story = {
  render: (args) => {
    const [value, setValue] = useState<Locale>(args.value);
    return <LanguageSelect {...args} value={value} onValueChange={setValue} className="w-80" />;
  },
};

/** The phone's settings row: the full width, a larger trigger. */
export const Phone: Story = {
  render: (args) => {
    const [value, setValue] = useState<Locale>("en");
    return (
      <div className="w-[358px]">
        <LanguageSelect {...args} value={value} onValueChange={setValue} size="lg" className="w-full" />
      </div>
    );
  },
};
