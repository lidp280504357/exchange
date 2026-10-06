import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// The spot and futures terminals (design §6.2), in the full-height terminal shell.
// The spot terminal's margin mode opens the margin account's dialogs.
const SpotTerminal = lazyPage(() => import("./SpotTerminal"), () => import("../../i18n/trade"), () => import("../../i18n/margin"));
// The futures terminal's 数据 tab (design 2026-10-06 §3.3) has its words in the futures area.
const FuturesTerminal = lazyPage(() => import("./FuturesTerminal"), () => import("../../i18n/trade"), () => import("../../i18n/futures"));

export const tradeRoutes: PageRoute[] = [
  { path: routes.trade(), element: <SpotTerminal /> },
  { path: routes.futures(), element: <FuturesTerminal /> },
];
