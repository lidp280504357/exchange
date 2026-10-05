// Query keys, in one place so pushes and pages agree on them.
export const qk = {
  pairs: ["market", "pairs"] as const,
  contracts: ["market", "contracts"] as const,
  assets: ["market", "assets"] as const,
  tickers: ["market", "tickers"] as const,
  summary: (limit: number) => ["market", "summary", limit] as const,
  candles: (symbol: string, interval: string) => ["market", "candles", symbol, interval] as const,
  sparkline: (symbol: string) => ["market", "sparkline", symbol] as const,
  depth: (symbol: string) => ["market", "depth", symbol] as const,
  trades: (symbol: string) => ["market", "trades", symbol] as const,
  markPrice: (symbol: string) => ["market", "mark-price", symbol] as const,
  /** The margin terms (public): assets with their pools and rates, pairs with their isolated terms. */
  marginAssets: ["market", "margin-assets"] as const,
  marginPairs: ["market", "margin-pairs"] as const,
  /** The platform profile (name, images, banner, registration), public. */
  platform: ["platform", "profile"] as const,

  profile: ["user", "profile"] as const,
  favorites: ["user", "favorites"] as const,
  balances: ["account", "balances"] as const,
  ledger: ["account", "ledger"] as const,
  transfers: ["account", "transfers"] as const,
  /** Orders by symbol ("" for all) and status filter ("ACTIVE", "FILLED", ...). */
  orders: (symbol: string, status: string) => ["orders", symbol, status] as const,
  allOrders: ["orders"] as const,
  fills: (symbol: string) => ["fills", symbol] as const,
  allFills: ["fills"] as const,
  notifications: ["notifications"] as const,
  deposits: ["wallet", "deposits"] as const,
  withdrawLimits: ["wallet", "limits"] as const,
  withdrawals: ["wallet", "withdrawals"] as const,
  networks: (asset: string) => ["wallet", "networks", asset] as const,
  positions: ["derivatives", "positions"] as const,
  derivatives: ["derivatives"] as const,
  marginAccounts: ["margin", "accounts"] as const,
};

/** The keys holding a signed-in user's data (cleared on sign-out). */
export const privateRoots = ["user", "account", "orders", "fills", "notifications", "wallet", "derivatives", "margin"] as const;
