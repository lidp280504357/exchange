import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Announcements and the help centre, from the Markdown the PC site shows (design §7.2).
const Announcements = lazyPage(() => import("./Announcements"), () => import("../../i18n/content"));
const Help = lazyPage(() => import("./Help"), () => import("../../i18n/content"));
const Article = lazyPage(() => import("./Article"), () => import("../../i18n/content"));

export const contentRoutes: PageRoute[] = [
  { path: routes.announcements, element: <Announcements />, shell: "page" },
  { path: routes.announcement(), element: <Article section="announcements" />, shell: "page" },
  { path: routes.help, element: <Help />, shell: "page" },
  { path: routes.helpArticle(), element: <Article section="help" />, shell: "page" },
];
