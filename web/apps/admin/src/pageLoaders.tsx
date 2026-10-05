import type { Admin } from "@exchange/core/api/admin";
import { use, type ComponentType } from "react";

// The pages of the console's sections, each a chunk of its own, loaded at
// its first render or before: the address the console opens at (the entry,
// preload.ts), a sidebar link pointed at or focused, the browser idle
// (A40). Once its chunk is in, a page renders at once: React.lazy resolves
// its own import at the first render even when the module was fetched
// ahead, which showed the skeleton for a frame (review BJ ⑨). This is the
// one table of the pages: sections.tsx takes them from here by path.

type PageProps = { admin: Admin };
type PageModule = { default: ComponentType<PageProps> };

/** A section's page, which preload() fetches ahead of its first render. */
export type Page = ComponentType<PageProps> & { preload(): Promise<unknown> };

// The pages a render is waiting for (an administrator's navigation in
// progress), and who waits for there to be none.
let waiting = 0;
const idle: (() => void)[] = [];
const waits = new WeakMap<Promise<PageModule>, Promise<PageModule>>();

/** waitFor counts a render waiting for a page's chunk until it comes. */
function waitFor(p: Promise<PageModule>): Promise<PageModule> {
  let w = waits.get(p);
  if (!w) {
    waiting++;
    w = p.finally(() => {
      waiting--;
      if (waiting === 0) for (const go of idle.splice(0)) go();
    });
    waits.set(p, w);
  }
  return w;
}

/** pagesIdle resolves when no render waits for a page's chunk: what the administrator opened comes before what is fetched ahead. */
export function pagesIdle(): Promise<void> {
  return waiting === 0 ? Promise.resolve() : new Promise((go) => idle.push(go));
}

function page(load: () => Promise<PageModule>): Page {
  let promise: Promise<PageModule> | undefined;
  let mod: PageModule | undefined;
  const preload = () =>
    (promise ??= load().then(
      (m) => (mod = m),
      (e: unknown) => {
        promise = undefined; // a failed fetch is tried again at the next render
        throw e;
      },
    ));
  function Section(props: PageProps) {
    const m = mod ?? use(waitFor(preload()));
    return <m.default {...props} />;
  }
  return Object.assign(Section, { preload });
}

/** The pages by the path of their section (users/:id is a user's page). */
export const pages = {
  "": page(() => import("./pages/Overview")),
  users: page(() => import("./pages/users/Users")),
  "users/:id": page(() => import("./pages/users/UserPage")),
  "identity-requests": page(() => import("./pages/users/IdentityRequests")),
  deposits: page(() => import("./pages/wallet/Deposits")),
  withdrawals: page(() => import("./pages/wallet/Withdrawals")),
  custody: page(() => import("./pages/wallet/Custody")),
  adjustments: page(() => import("./pages/funds/Adjustments")),
  approvals: page(() => import("./pages/funds/Approvals")),
  ledger: page(() => import("./pages/Ledger")),
  orders: page(() => import("./pages/orders/Orders")),
  positions: page(() => import("./pages/trading/Positions")),
  liquidations: page(() => import("./pages/trading/Liquidations")),
  derivatives: page(() => import("./pages/Derivatives")),
  house: page(() => import("./pages/House")),
  "margin/accounts": page(() => import("./pages/margin/Accounts")),
  "margin/liquidations": page(() => import("./pages/margin/Liquidations")),
  "margin/interest": page(() => import("./pages/margin/Interest")),
  "margin/params": page(() => import("./pages/margin/Params")),
  instruments: page(() => import("./pages/Instruments")),
  sim: page(() => import("./pages/sim/Overview")),
  "sim/control": page(() => import("./pages/sim/Control")),
  "sim/events": page(() => import("./pages/sim/Events")),
  "sim/bots": page(() => import("./pages/sim/Bots")),
  "sim/token": page(() => import("./pages/sim/Token")),
  risk: page(() => import("./pages/Flags")),
  announcements: page(() => import("./pages/content/Announcements")),
  "help-articles": page(() => import("./pages/content/HelpArticles")),
  pages: page(() => import("./pages/content/FixedPages")),
  broadcasts: page(() => import("./pages/content/Broadcasts")),
  admins: page(() => import("./pages/system/Admins")),
  audit: page(() => import("./pages/Audit")),
  reports: page(() => import("./pages/Reports")),
  health: page(() => import("./pages/system/Health")),
  platform: page(() => import("./pages/system/Platform")),
  launch: page(() => import("./pages/system/Launch")),
  settings: page(() => import("./pages/system/Settings")),
  account: page(() => import("./pages/system/Account")),
} satisfies Record<string, Page>;

export type PagePath = keyof typeof pages;

/** pageAt is the page an address shows: /users/<id> a user's, /sim/control its own, else its first segment's. */
export function pageAt(pathname: string): Page | undefined {
  const [first = "", second] = pathname.replace(/^\/+|\/+$/g, "").split("/");
  const table: Record<string, Page> = pages;
  return (second && table[`${first}/${second}`]) || (first === "users" && second ? pages["users/:id"] : table[first]);
}
