import { preloadable } from "@exchange/ui";

// The futures data board in a chunk of its own: not on the terminal's
// first screen; the 数据 tab starts loading it when the pointer reaches
// the tab bar, so it is usually in by the click.
export const FuturesDataBoard = preloadable(() => import("./FuturesDataBoard"), (m) => m.FuturesDataBoard);
