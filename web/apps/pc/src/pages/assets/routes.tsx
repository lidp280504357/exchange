import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Assets: overview, deposit, withdraw, transfer and history (design §6.2),
// and the margin accounts (margin design 2026-10-06 §7).
// Each page is its own chunk; all of them need a session.
// The overview carries the notice of the paused product lines (design 2026-10-07, product line switches §1 #2).
const Overview = lazyPage(() => import("./Overview"), () => import("../../i18n/assets"), () => import("../../i18n/auth"), () => import("../../i18n/products"));
const Deposit = lazyPage(() => import("./Deposit"), () => import("../../i18n/assets"), () => import("../../i18n/auth"));
const Withdraw = lazyPage(() => import("./Withdraw"), () => import("../../i18n/assets"), () => import("../../i18n/auth"));
const Transfer = lazyPage(() => import("./Transfer"), () => import("../../i18n/assets"), () => import("../../i18n/auth"));
const History = lazyPage(() => import("./History"), () => import("../../i18n/assets"), () => import("../../i18n/auth"));
const Margin = lazyPage(() => import("./Margin"), () => import("../../i18n/assets"), () => import("../../i18n/margin"));
const ClosedProducts = lazyPage(() => import("./ClosedProducts"), () => import("../../i18n/assets"), () => import("../../i18n/products"));

export const assetRoutes: PageRoute[] = [
  { path: routes.assets, element: <Overview />, auth: true },
  { path: routes.deposit, element: <Deposit />, auth: true },
  { path: routes.withdraw, element: <Withdraw />, auth: true },
  { path: routes.transfer, element: <Transfer />, auth: true },
  { path: routes.history, element: <History />, auth: true },
  { path: routes.margin, element: <Margin />, auth: true },
  { path: routes.closedProducts, element: <ClosedProducts />, auth: true },
];
