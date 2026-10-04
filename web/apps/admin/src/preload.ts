// preloadConsole starts downloading the signed-in shell's chunk and the
// chunk of the page at the address while /admin/v1/me is asked, instead of
// one after the other once the session is known (C6: the overview scored 83
// for performance on Lighthouse, its chunks three round trips apart). The
// console's CSP forbids inline scripts, so index.html cannot do it as the
// user sites do (web/scripts/route-preload.mjs); the entry does. The lazy
// pages import the same modules, which the browser then has.

const pages: Record<string, () => Promise<unknown>> = {
  "": () => import("./pages/Overview"),
  users: () => import("./pages/users/Users"),
  "identity-requests": () => import("./pages/users/IdentityRequests"),
  deposits: () => import("./pages/wallet/Deposits"),
  withdrawals: () => import("./pages/wallet/Withdrawals"),
  custody: () => import("./pages/wallet/Custody"),
  adjustments: () => import("./pages/funds/Adjustments"),
  approvals: () => import("./pages/funds/Approvals"),
  ledger: () => import("./pages/Ledger"),
  orders: () => import("./pages/orders/Orders"),
  positions: () => import("./pages/trading/Positions"),
  liquidations: () => import("./pages/trading/Liquidations"),
  derivatives: () => import("./pages/Derivatives"),
  house: () => import("./pages/House"),
  instruments: () => import("./pages/Instruments"),
  sim: () => import("./pages/sim/Overview"),
  "sim/control": () => import("./pages/sim/Control"),
  "sim/events": () => import("./pages/sim/Events"),
  "sim/bots": () => import("./pages/sim/Bots"),
  "sim/token": () => import("./pages/sim/Token"),
  risk: () => import("./pages/Flags"),
  announcements: () => import("./pages/content/Announcements"),
  "help-articles": () => import("./pages/content/HelpArticles"),
  pages: () => import("./pages/content/FixedPages"),
  broadcasts: () => import("./pages/content/Broadcasts"),
  admins: () => import("./pages/system/Admins"),
  audit: () => import("./pages/Audit"),
  reports: () => import("./pages/Reports"),
  health: () => import("./pages/system/Health"),
  platform: () => import("./pages/system/Platform"),
  launch: () => import("./pages/system/Launch"),
  settings: () => import("./pages/system/Settings"),
  account: () => import("./pages/system/Account"),
};

/** pageOf is the chunk loader of the page at path ("users/<id>" is the user's page). */
function pageOf(path: string): (() => Promise<unknown>) | undefined {
  const [first = "", second] = path.replace(/^\/+|\/+$/g, "").split("/");
  return (second && pages[`${first}/${second}`]) || (first === "users" && second ? () => import("./pages/users/UserPage") : pages[first]);
}

const fail = () => undefined; // the page's own import reports a failure

/** preloadConsole starts the chunks a signed-in visit to pathname needs; nothing for the sign-in and setup pages. */
export function preloadConsole(pathname: string) {
  if (/^\/*(login|setup)(\/|$)/.test(pathname)) return;
  void import("./layout/SignedIn").catch(fail);
  void pageOf(pathname)?.().catch(fail);
}

/** prefetchPage starts the chunk of the page at path: a sidebar link pointed at or focused (A40). */
export function prefetchPage(path: string) {
  void pageOf(path)?.().catch(fail);
}

/**
 * prefetchPages loads the chunks of the pages at paths one after another
 * while the browser has nothing else to do (A40): a section opened later
 * finds its page loaded. Not when the browser asks to save data.
 */
export function prefetchPages(paths: readonly string[]) {
  if ((navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData) return;
  const idle = (next: () => void) => (typeof requestIdleCallback === "function" ? requestIdleCallback(next, { timeout: 5000 }) : setTimeout(next, 200));
  const queue = [...paths];
  const next = () => {
    const path = queue.shift();
    if (path === undefined) return;
    void (pageOf(path)?.() ?? Promise.resolve()).catch(fail).finally(() => idle(next));
  };
  idle(next);
}
