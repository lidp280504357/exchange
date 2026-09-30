// The route table both user sites share (design §4.1, §6.1): the PC site
// and the mobile site answer the same paths, so a shared link opens the
// same page on either and the device switch keeps the path.

export const routes = {
  home: "/",
  markets: "/markets",
  trade: (symbol = ":symbol") => `/trade/${symbol}`,
  futures: (symbol = ":symbol") => `/futures/${symbol}`,
  coin: (symbol = ":symbol") => `/coin/${symbol}`,
  assets: "/assets",
  deposit: "/assets/deposit",
  withdraw: "/assets/withdraw",
  transfer: "/assets/transfer",
  history: "/assets/history",
  security: "/account/security",
  settings: "/account/settings",
  sessions: "/account/sessions",
  notifications: "/notifications",
  announcements: "/announcements",
  announcement: (slug = ":slug") => `/announcements/${slug}`,
  help: "/help",
  helpArticle: (slug = ":slug") => `/help/${slug}`,
  login: "/login",
  register: "/register",
  reset: "/reset",
  /** The mobile site's "me" tab. */
  me: "/me",
} as const;

/** The default market of the trade entry points. */
export const DEFAULT_SYMBOL = "BTC-USDT";
export const DEFAULT_CONTRACT = "BTC-USDT-PERP";

/** Paths that need a session; the others are public (§6.1). */
export function needsSignIn(path: string): boolean {
  return /^\/(assets|account|notifications|me\/)/.test(path);
}
