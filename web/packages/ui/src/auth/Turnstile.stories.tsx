import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { Turnstile } from "./Turnstile";

const meta = { title: "Base/Turnstile", component: Turnstile, args: { onToken: () => {} } } satisfies Meta<typeof Turnstile>;
export default meta;

type Story = StoryObj<typeof meta>;

/** Without a site key in the build it says so; with one it renders Cloudflare's widget. */
export const Widget: Story = {
  render: () => {
    const [token, setToken] = useState("");
    return (
      <div className="flex w-80 flex-col gap-2">
        <Turnstile onToken={setToken} />
        <p className="text-xs text-fg-3">token: {token ? `${token.slice(0, 12)}…` : "—"}</p>
      </div>
    );
  },
};

/** fill: as wide as the form around it (Cloudflare's flexible size), as the code steps use it (F32). */
export const Fill: Story = {
  render: () => {
    const [token, setToken] = useState("");
    return (
      <div className="flex w-[336px] flex-col gap-2">
        <Turnstile onToken={setToken} fill />
        <p className="text-xs text-fg-3">token: {token ? `${token.slice(0, 12)}…` : "—"}</p>
      </div>
    );
  },
};
