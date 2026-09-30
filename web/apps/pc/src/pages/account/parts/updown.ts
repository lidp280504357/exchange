import type { UpDown } from "@exchange/core";

// The rise/fall preview of the settings page. The page can only use the
// semantic up/down tokens, which already follow the current choice (the
// tokens swap under data-updown="red-up"). To draw the other choice, its
// rises take the token of the current falls, and the other way round.

export type Tone = "up" | "down";

/** previewTones gives the tokens that draw an option's rises and falls while `current` is applied. */
export function previewTones(option: UpDown, current: UpDown): { rise: Tone; fall: Tone } {
  return option === current ? { rise: "up", fall: "down" } : { rise: "down", fall: "up" };
}
