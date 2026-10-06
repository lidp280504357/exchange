import { preloadable } from "@exchange/ui";

// The futures data board in a chunk of its own: not on the terminal's
// first screen; the 数据 tab loads it the first time it is shown.
export const FuturesDataBoard = preloadable(() => import("./FuturesDataBoard"), (m) => m.FuturesDataBoard);
