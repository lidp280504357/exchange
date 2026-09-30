import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Assets: overview (a tab), deposit, withdraw, transfer and history (design
// §7.2). Each page is its own chunk and loads with the area's strings (the
// withdrawal also with the step-up's); all of them need a session.
const strings = () => import("../../i18n/assets");
const auth = () => import("../../i18n/auth");

const Overview = lazyPage(() => import("./Overview"), strings);
const Deposit = lazyPage(() => import("./Deposit"), strings);
const Withdraw = lazyPage(() => import("./Withdraw"), strings, auth);
const Transfer = lazyPage(() => import("./Transfer"), strings);
const History = lazyPage(() => import("./History"), strings);

export const assetRoutes: PageRoute[] = [
  { path: routes.assets, element: <Overview />, auth: true, shell: "tabs" },
  { path: routes.deposit, element: <Deposit />, auth: true, shell: "page" },
  { path: routes.withdraw, element: <Withdraw />, auth: true, shell: "page" },
  { path: routes.transfer, element: <Transfer />, auth: true, shell: "page" },
  { path: routes.history, element: <History />, auth: true, shell: "page" },
];
