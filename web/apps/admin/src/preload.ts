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

/** preloadConsole starts the chunks a signed-in visit to pathname needs; nothing for the sign-in and setup pages. */
export function preloadConsole(pathname: string) {
  const path = pathname.replace(/^\/+|\/+$/g, "");
  if (/^(login|setup)(\/|$)/.test(path)) return;
  const fail = () => undefined; // the page's own import reports a failure
  void import("./layout/SignedIn").catch(fail);
  const [first = "", second] = path.split("/");
  const load = (second && pages[`${first}/${second}`]) || (first === "users" && second ? () => import("./pages/users/UserPage") : pages[first]);
  void load?.().catch(fail);
}
