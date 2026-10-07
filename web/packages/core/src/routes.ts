// The route table both user sites share (design §4.1, §6.1): the PC site
// and the mobile site answer the same paths, so a shared link opens the
// same page on either and the device switch keeps the path.

export const routes = {
  home: "/",
  markets: "/markets",
  trade: (symbol = ":symbol") => `/trade/${symbol}`,
  futures: (symbol = ":symbol") => `/futures/${symbol}`,
  /** Every contract's futures data (design 2026-10-06 §3.3); a static path, so it wins over /futures/:symbol. */
  futuresData: "/futures/data",
  coin: (symbol = ":symbol") => `/coin/${symbol}`,
  assets: "/assets",
  deposit: "/assets/deposit",
  withdraw: "/assets/withdraw",
  transfer: "/assets/transfer",
  history: "/assets/history",
  /** The margin accounts (margin design 2026-10-06 §7). */
  margin: "/assets/margin",
  /** The username and the avatar (design 2026-10-07, avatars and usernames). */
  profile: "/account/profile",
  /** What closed product lines still hold of the user's, to wind down (design 2026-10-07, product line switches §1 #2). */
  closedProducts: "/assets/closed",
  /** The apps to download (design 2026-10-07, App download page §4). */
  download: "/download",
  security: "/account/security",
  settings: "/account/settings",
  sessions: "/account/sessions",
  notifications: "/notifications",
  announcements: "/announcements",
  announcement: (slug = ":slug") => `/announcements/${slug}`,
  help: "/help",
  helpArticle: (slug = ":slug") => `/help/${slug}`,
  /** The legal and information pages (design 2026-10-04 §4.4): terms, privacy, risk, fees, about, contact. */
  legal: (slug = ":slug") => `/legal/${slug}`,
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
