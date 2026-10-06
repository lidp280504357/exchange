import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Home, markets and coin pages (design §6.2).
const Home = lazyPage(() => import("./Home"), () => import("../../i18n/markets"), () => import("../../i18n/content"));
// The futures category's columns and groups (design 2026-10-06 §3.3) have their words in the futures area.
const Markets = lazyPage(() => import("./Markets"), () => import("../../i18n/markets"), () => import("../../i18n/content"), () => import("../../i18n/futures"));
const Coin = lazyPage(() => import("./Coin"), () => import("../../i18n/markets"), () => import("../../i18n/content"));

export const marketRoutes: PageRoute[] = [
  { path: routes.home, element: <Home /> },
  { path: routes.markets, element: <Markets /> },
  { path: routes.coin(), element: <Coin /> },
];
