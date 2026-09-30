import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Home, markets and coin pages (design §7.2). Home shows announcements, so
// it loads the content strings too.
const Home = lazyPage(() => import("./Home"), () => import("../../i18n/markets"), () => import("../../i18n/content"));
const Markets = lazyPage(() => import("./Markets"), () => import("../../i18n/markets"));
const Coin = lazyPage(() => import("./Coin"), () => import("../../i18n/markets"));

export const homeRoutes: PageRoute[] = [
  { path: routes.home, element: <Home />, shell: "tabs" },
  { path: routes.markets, element: <Markets />, shell: "tabs" },
  { path: routes.coin(), element: <Coin />, shell: "page" },
];
