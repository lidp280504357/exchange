import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// The spot and futures terminals (design §6.2), in the full-height terminal shell.
const SpotTerminal = lazyPage(() => import("./SpotTerminal"), () => import("../../i18n/trade"));
const FuturesTerminal = lazyPage(() => import("./FuturesTerminal"), () => import("../../i18n/trade"));

export const tradeRoutes: PageRoute[] = [
  { path: routes.trade(), element: <SpotTerminal /> },
  { path: routes.futures(), element: <FuturesTerminal /> },
];
