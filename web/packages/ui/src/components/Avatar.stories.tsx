import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
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

// An uploaded avatar stand-in, drawn here (the real ones are 256 and 64 px WebP).
function picture(): string {
  const c = document.createElement("canvas");
  c.width = 256;
  c.height = 256;
  const g = c.getContext("2d");
  if (!g) return "";
  const grad = g.createLinearGradient(0, 0, 256, 256);
  grad.addColorStop(0, "orange");
  grad.addColorStop(1, "royalblue");
  g.fillStyle = grad;
  g.fillRect(0, 0, 256, 256);
  g.fillStyle = "white";
  g.beginPath();
  g.arc(128, 128, 64, 0, Math.PI * 2);
  g.fill();
  return c.toDataURL("image/png");
}

/** An uploaded picture at the sites' sizes (top bar 24, menu and side column 40, profile 88). */
export const Image: Story = {
  render: () => {
    const [src] = useState(picture);
    return (
      <div className="flex items-center gap-3">
        {[24, 40, 52, 88].map((size) => (
          <Avatar key={size} src={src} seed="0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e" name="satoshi_n" size={size} />
        ))}
      </div>
    );
  },
};
