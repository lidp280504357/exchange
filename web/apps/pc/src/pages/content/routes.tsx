import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Announcements and the help centre, from Markdown in the repository (design §6.2).
const Announcements = lazyPage(() => import("./Announcements"), () => import("../../i18n/content"));
const Help = lazyPage(() => import("./Help"), () => import("../../i18n/content"));
const Article = lazyPage(() => import("./Article"), () => import("../../i18n/content"));

export const contentRoutes: PageRoute[] = [
  { path: routes.announcements, element: <Announcements /> },
  { path: routes.announcement(), element: <Article section="announcements" /> },
  { path: routes.help, element: <Help /> },
  { path: routes.helpArticle(), element: <Article section="help" /> },
];
