import type { Meta, StoryObj } from "@storybook/react-vite";
import { Avatar } from "./Avatar";
import { DEFAULT_AVATARS, DefaultAvatar } from "./DefaultAvatar";

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

/**
 * The twelve built-in avatars: a user without an uploaded picture gets the
 * one its user ID hashes to (design 2026-10-07, avatars and usernames §1 #4),
 * at the sizes the sites use.
 */
export const BuiltIn: Story = {
  render: () => (
    <div className="flex flex-col gap-4">
      {[24, 40, 88].map((size) => (
        <div key={size} className="flex flex-wrap items-center gap-3">
          {Array.from({ length: DEFAULT_AVATARS }, (_, i) => (
            <span key={i} className="inline-flex overflow-hidden rounded-full" style={{ width: size, height: size }}>
              <DefaultAvatar index={i} />
            </span>
          ))}
        </div>
      ))}
    </div>
  ),
};

/** With a user ID, a missing or broken picture shows that user's built-in avatar. */
export const BySeed: Story = {
  render: () => (
    <div className="flex items-center gap-3">
      <Avatar seed="0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e" name="user_k3x9q2ab" size={40} />
      <Avatar seed="0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2f" name="satoshi_n" size={40} />
      <Avatar seed="0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e" src="/does-not-exist.webp" name="user_k3x9q2ab" size={40} />
    </div>
  ),
};
