// WebSocket messages of /v1/ws (requirements §7.3; the gateway's
// internal/gateway/ws*.go). Amounts are decimal strings.

/** A public market push; depth carries its own seq and prev_seq. */
export type MarketPush<T = unknown> = {
  channel: string;
  type?: "snapshot" | "update" | "closed" | "estimate" | "settled";
  seq?: number;
  prev_seq?: number;
  data: T;
};

/** A private push with the user's sequence. */
export type PrivatePush<T = unknown> = { channel: string; seq: number; data: T };

export type WsPush<T = unknown> = MarketPush<T> | PrivatePush<T>;

/** A reply to a client message, or a server notice (ping, error, resync). */
export type WsReply = {
  op: string;
  ok?: boolean;
  code?: string;
  message?: string;
  args?: string[];
  user_id?: string;
  ts?: number;
};

export type Level = [price: string, quantity: string];

export type DepthData = { bids: Level[]; asks: Level[] };

export type TickerData = {
  symbol: string;
  last: string | null;
  open: string | null;
  high: string | null;
  low: string | null;
  volume: string;
  quote_volume: string;
  trade_count: number;
  change: string | null;
  bid: string | null;
  ask: string | null;
  updated_at: string;
};

export type TradeData = {
  trade_id: string;
  trade_number: number;
  price: string;
  quantity: string;
  quote_quantity: string;
  taker_side: "BUY" | "SELL";
  executed_at: string;
};

export type CandleData = {
  open_time: string;
  open: string;
  high: string;
  low: string;
  close: string;
  volume: string;
  quote_volume: string;
  trade_count: number;
  closed: boolean;
};

export type MarkData = {
  symbol: string;
  mark_price: string;
  index_price: string;
  funding_rate: string;
  next_funding_time: string;
  updated_at: string;
};

export type FundingData = {
  symbol: string;
  funding_rate: string;
  interest_rate: string;
  funding_time: string;
  mark_price: string | null;
};

export type BalanceData = { account_type: string; asset: string; available: string; frozen: string; entry_type: string };

export type OrderData = {
  order_id: string;
  client_order_id?: string;
  symbol: string;
  status: string;
  side?: string;
  type?: string;
  price?: string;
  quantity?: string;
  quote_amount?: string;
  filled_quantity?: string;
  filled_quote?: string;
  cancel_reason?: string;
  reject_reason?: string;
  sequence?: number;
  /** With the acceptance: SPOT or the margin account (margin design 2026-10-06). */
  account?: string;
  side_effect?: string;
};

export type FillData = {
  trade_id: string;
  order_id: string;
  symbol: string;
  side: string;
  role: string;
  price: string;
  quantity: string;
  quote_quantity?: string;
  fee_asset: string;
  fee: string;
  executed_at: string;
  position_side?: string;
  closed_quantity?: string;
  realized_pnl?: string;
  liquidation?: boolean;
};

export type NotificationData = { id: string; type: string; title: string; body: string };

/** The private channels (need auth). */
export const PRIVATE_CHANNELS = ["balances", "notifications", "orders", "fills", "deposits", "withdrawals", "positions", "risk"] as const;
export type PrivateChannel = (typeof PRIVATE_CHANNELS)[number];

export function isPrivateChannel(ch: string): ch is PrivateChannel {
  return (PRIVATE_CHANNELS as readonly string[]).includes(ch);
}

/** Channel names, so callers never assemble them by hand. */
export const channels = {
  tickers: "tickers",
  ticker: (symbol: string) => `ticker:${symbol}`,
  depth: (symbol: string) => `depth:${symbol}`,
  trades: (symbol: string) => `trades:${symbol}`,
  candles: (symbol: string, interval: string) => `candles:${symbol}:${interval}`,
  markPrice: (symbol: string) => `mark-price:${symbol}`,
  funding: (symbol: string) => `funding:${symbol}`,
} as const;

/** High-frequency channels are paused while the page is hidden. */
export function isHighFrequency(ch: string): boolean {
  return ch === "tickers" || /^(ticker|depth|trades|candles|mark-price):/.test(ch);
}
