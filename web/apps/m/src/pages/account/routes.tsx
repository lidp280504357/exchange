import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Me (a tab), security, devices and sign-ins, settings, notifications
// (design §7.2). Security and devices ask for step-ups, whose sheet uses
// the auth strings.
// The visitors' market glance on "me" shows market names, with the markets strings.
// The profile: the avatar and the username (design 2026-10-07, avatars and usernames).
const Profile = lazyPage(() => import("./Profile"), () => import("../../i18n/account"), () => import("../../i18n/profile"));
const Me = lazyPage(() => import("./Me"), () => import("../../i18n/account"), () => import("../../i18n/markets"));
const Security = lazyPage(() => import("./Security"), () => import("../../i18n/account"), () => import("../../i18n/auth"));
const Sessions = lazyPage(() => import("./Sessions"), () => import("../../i18n/account"), () => import("../../i18n/auth"));
const Settings = lazyPage(() => import("./Settings"), () => import("../../i18n/account"));
const Notifications = lazyPage(() => import("./Notifications"), () => import("../../i18n/account"));

export const accountRoutes: PageRoute[] = [
  { path: routes.me, element: <Me />, shell: "tabs" },
  { path: routes.profile, element: <Profile />, auth: true, shell: "page" },
  { path: routes.security, element: <Security />, auth: true, shell: "page" },
  { path: routes.sessions, element: <Sessions />, auth: true, shell: "page" },
  // Device-local preferences: open to visitors too (the "me" tab lists it for them).
  { path: routes.settings, element: <Settings />, shell: "page" },
  { path: routes.notifications, element: <Notifications />, auth: true, shell: "page" },
];
