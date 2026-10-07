import type { Meta, StoryObj } from "@storybook/react-vite";
import { Avatar } from "../components/Avatar";
import { UploadRing } from "./UploadRing";

// The ring round an avatar while its picture goes up (design 2026-10-07,
// avatars and usernames): turning while the picture is prepared, filling
// with the upload's progress, turning again while the server saves it.

const SEED = "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e";

const meta = { title: "Profile/UploadRing", component: UploadRing, args: { state: { phase: "preparing" } } } satisfies Meta<typeof UploadRing>;
export default meta;

type Story = StoryObj<typeof meta>;

export const Phases: Story = {
  render: () => (
    <div className="flex items-center gap-8">
      {(
        [
          { phase: "preparing" },
          { phase: "uploading", preview: "", progress: 0.4 },
          { phase: "uploading", preview: "", progress: 1 },
        ] as const
      ).map((state, i) => (
        <span key={i} className="relative" style={{ width: 88, height: 88 }}>
          <Avatar seed={SEED} size={88} />
          <UploadRing state={state} />
        </span>
      ))}
    </div>
  ),
};
