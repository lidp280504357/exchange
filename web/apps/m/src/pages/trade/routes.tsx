import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// The spot and futures terminals in portrait (design §7.2).
const SpotTerminal = lazyPage(() => import("./SpotTerminal"), () => import("../../i18n/trade"));
const FuturesTerminal = lazyPage(() => import("./FuturesTerminal"), () => import("../../i18n/trade"));

export const tradeRoutes: PageRoute[] = [
  { path: routes.trade(), element: <SpotTerminal />, shell: "tabs" },
  { path: routes.futures(), element: <FuturesTerminal />, shell: "tabs" },
];
