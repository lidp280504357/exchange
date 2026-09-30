import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// The account: security, devices and sign-ins, settings, notifications (design §6.2).
const Security = lazyPage(() => import("./Security"), () => import("../../i18n/account"), () => import("../../i18n/auth"));
const Sessions = lazyPage(() => import("./Sessions"), () => import("../../i18n/account"), () => import("../../i18n/auth"));
const Settings = lazyPage(() => import("./Settings"), () => import("../../i18n/account"), () => import("../../i18n/auth"));
const Notifications = lazyPage(() => import("./Notifications"), () => import("../../i18n/account"), () => import("../../i18n/auth"));

export const accountRoutes: PageRoute[] = [
  { path: routes.security, element: <Security />, auth: true },
  { path: routes.sessions, element: <Sessions />, auth: true },
  // Device-local preferences: open to visitors too.
  { path: routes.settings, element: <Settings /> },
  { path: routes.notifications, element: <Notifications />, auth: true },
];
