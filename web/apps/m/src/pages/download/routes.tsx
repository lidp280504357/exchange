import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// The apps to download (design 2026-10-07, App download page §4), a sub
// page with a back bar.
const Download = lazyPage(() => import("./Download"), () => import("../../i18n/download"));

export const downloadRoutes: PageRoute[] = [{ path: routes.download, element: <Download />, shell: "page" }];
