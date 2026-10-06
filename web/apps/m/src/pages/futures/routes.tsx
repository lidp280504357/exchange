import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Every contract's futures data (design 2026-10-06 §3.3, batch F2), a sub
// page with a back bar; its static path wins over the terminal's /futures/:symbol.
const FuturesData = lazyPage(() => import("./FuturesData"), () => import("../../i18n/futures"));

export const futuresRoutes: PageRoute[] = [{ path: routes.futuresData, element: <FuturesData />, shell: "page" }];
