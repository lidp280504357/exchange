import { routes } from "@exchange/core";
import { ProductGate } from "../../features/products/ProductGate";
import { lazyPage, type PageRoute } from "../../routing";

// The spot and futures terminals in portrait (design §7.2).
// The spot terminal's margin mode opens the margin account's sheets.
const SpotTerminal = lazyPage(() => import("./SpotTerminal"), () => import("../../i18n/trade"), () => import("../../i18n/margin"));
// The futures terminal's 数据 tab (design 2026-10-06 §3.3) has its words in the futures area.
const FuturesTerminal = lazyPage(() => import("./FuturesTerminal"), () => import("../../i18n/trade"), () => import("../../i18n/futures"));

// A closed product line's terminal says it is not open (design 2026-10-07, product line switches §1 #2).
export const tradeRoutes: PageRoute[] = [
  {
    path: routes.trade(),
    element: (
      <ProductGate kind="trade">
        <SpotTerminal />
      </ProductGate>
    ),
    shell: "tabs",
  },
  {
    path: routes.futures(),
    element: (
      <ProductGate kind="futures">
        <FuturesTerminal />
      </ProductGate>
    ),
    shell: "tabs",
  },
];
