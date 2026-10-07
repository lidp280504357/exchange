import "../test/setup";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Avatar } from "./Avatar";
import { DEFAULT_AVATARS, defaultAvatarIndex } from "./DefaultAvatar";

describe("built-in avatars", () => {
  it("are chosen by the user ID, the same whatever its case", () => {
    const id = "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e";
    expect(defaultAvatarIndex(id)).toBe(defaultAvatarIndex(id.toUpperCase()));
    // Pinned (FNV-1a of the ID, modulo 12): changing the rule would change
    // every user's image, here and in the console.
    expect(defaultAvatarIndex(id)).toBe(0);
    expect(defaultAvatarIndex("0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2f")).toBe(9);
    const seen = new Set<number>();
    for (let n = 0; n < 600; n++) seen.add(defaultAvatarIndex(`0192f0c4-8a3e-7b2d-9c1f-${n.toString(16).padStart(12, "0")}`));
    expect(seen.size).toBe(DEFAULT_AVATARS);
  });

  it("stand in for the initials when the avatar has a seed", () => {
    const id = "0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e";
    const { container } = render(<Avatar seed={id} name="user_k3x9q2ab" />);
    const art = container.querySelector("svg[data-avatar-default]");
    expect(art?.getAttribute("data-avatar-default")).toBe(String(defaultAvatarIndex(id)));
    expect(container.textContent).toBe("");
  });

  it("leave the initials to avatars without one", () => {
    const { container } = render(<Avatar name="Ada Lovelace" />);
    expect(container.querySelector("svg")).toBeNull();
    expect(container.textContent).toBe("AL");
  });
});
