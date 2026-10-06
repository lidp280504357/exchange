import { preloadable } from "@exchange/ui";

// The transfer, borrow and repay dialog, in a chunk of its own (B108): not
// on the margin page's or the terminal's first screen, preloaded while they
// are idle (core's useIdleImport) and opened at once after (B114). One for
// both pages, so that a preload on either serves the other (B115).
export const MarginDialog = preloadable(() => import("./MarginDialog"), (m) => m.MarginDialog);
