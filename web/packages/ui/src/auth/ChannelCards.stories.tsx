import type { OtpChannel } from "@exchange/core/auth/otp";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { ChannelCards, type ChannelOption } from "./ChannelCards";

const meta = {
  title: "Auth/ChannelCards",
  component: ChannelCards,
  args: { options: [], value: "EMAIL", onValueChange: () => {} },
} satisfies Meta<typeof ChannelCards>;
export default meta;

type Story = StoryObj<typeof meta>;

function Picker({ options, width = "w-[336px]", size }: { options: ChannelOption[]; width?: string; size?: "md" | "lg" }) {
  const [value, setValue] = useState<OtpChannel>(options.find((o) => o.bound)?.channel ?? "EMAIL");
  return (
    <div className={width}>
      <ChannelCards options={options} value={value} onValueChange={setValue} size={size} />
    </div>
  );
}

/** The step-up's channels (F32), both bound: the email chosen, the phone a click (or an arrow key) away. */
export const BothBound: Story = {
  render: () => (
    <Picker
      options={[
        { channel: "EMAIL", target: "a***@example.com", bound: true },
        { channel: "SMS", target: "+86138****1234", bound: true },
      ]}
    />
  ),
};

/** An account with only an email: the SMS card greyed and marked 未绑定. On the phone, taller cards in the sheet's width. */
export const OneUnbound: Story = {
  render: () => (
    <div className="flex flex-col gap-4">
      <Picker
        options={[
          { channel: "EMAIL", target: "a***@example.com", bound: true },
          { channel: "SMS", bound: false },
        ]}
      />
      <Picker
        width="w-[358px]"
        size="lg"
        options={[
          { channel: "EMAIL", target: "a***@example.com", bound: true },
          { channel: "SMS", bound: false },
        ]}
      />
    </div>
  ),
};

/** A single channel: nothing to pick, one line says where the code goes. */
export const SingleChannel: Story = {
  render: () => <Picker options={[{ channel: "SMS", target: "+86138****1234", bound: true }]} />,
};

/** While the account's identities load (a cold cache, F41): the two cards' places, the same size. */
export const Loading: Story = {
  render: () => (
    <div className="w-[336px]">
      <ChannelCards options={[]} value="EMAIL" onValueChange={() => {}} loading />
    </div>
  ),
};
