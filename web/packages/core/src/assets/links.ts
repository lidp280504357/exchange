// Where the "trade" buttons of the assets pages go: an asset's own market,
// preferably against USDT, else any pair it is part of.

type PairLike = { symbol: string; base_asset: string; quote_asset: string; status: string };
type ContractLike = { symbol: string; base_asset: string; quote_asset: string; status: string; settle_asset?: string };

const LIVE = new Set(["TRADING", "PREPARE", "CANCEL_ONLY", "HALT"]);

/**
 * tradeSymbolFor picks the spot pair to trade an asset on: <ASSET>-USDT,
 * else a pair with the asset as base, else one with it as quote (USDT →
 * BTC-USDT); null when it has none. Trading pairs come before the others.
 */
export function tradeSymbolFor(asset: string, pairs: readonly PairLike[]): string | null {
  const live = pairs.filter((p) => LIVE.has(p.status));
  for (const tier of [live.filter((p) => p.status === "TRADING"), live.filter((p) => p.status !== "TRADING")]) {
    const found =
      tier.find((p) => p.base_asset === asset && p.quote_asset === "USDT") ??
      tier.find((p) => p.base_asset === asset) ??
      tier.find((p) => p.quote_asset === asset);
    if (found) return found.symbol;
  }
  return null;
}

/**
 * contractFor picks the perpetual an asset's FUTURES account trades: one
 * settled in it (USDT → BTC-USDT-PERP, BTC → BTC-USD-PERP once
 * coin-margined contracts are listed), else <ASSET>-USDT-PERP, else one
 * quoted in it.
 */
export function contractFor(asset: string, contracts: readonly ContractLike[]): string | null {
  const live = contracts.filter((c) => LIVE.has(c.status));
  return (
    live.find((c) => c.settle_asset === asset)?.symbol ??
    live.find((c) => c.base_asset === asset)?.symbol ??
    live.find((c) => c.quote_asset === asset)?.symbol ??
    null
  );
}
