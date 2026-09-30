import { describe, expect, it } from "vitest";
import { previewTones } from "./updown";

describe("rise and fall preview", () => {
  it("draws the applied choice with its own tokens", () => {
    expect(previewTones("green-up", "green-up")).toEqual({ rise: "up", fall: "down" });
    expect(previewTones("red-up", "red-up")).toEqual({ rise: "up", fall: "down" });
  });
  it("draws the other choice with the tokens swapped", () => {
    // Red rises while green-up applies: red is the current fall colour.
    expect(previewTones("red-up", "green-up")).toEqual({ rise: "down", fall: "up" });
    expect(previewTones("green-up", "red-up")).toEqual({ rise: "down", fall: "up" });
  });
});
