import type { components } from "../api/gen/wallet";

// The networks assets move on (GET /v1/wallet/networks): the deposit and
// withdrawal pages are built from them, so a network added on the server
// (USDT on TRC20/BEP20/ERC20, BTC, ETH with the custody wallet, design §9)
// appears without a front-end change. USDT has one balance whatever network
// it arrives on; the network is chosen per deposit address and withdrawal.

export type WalletNetwork = components["schemas"]["WalletNetwork"];
export type DepositAddress = components["schemas"]["DepositAddress"];
export type Deposit = components["schemas"]["Deposit"];
export type Withdrawal = components["schemas"]["Withdrawal"];
export type WithdrawAddress = components["schemas"]["WithdrawAddress"];

export type Purpose = "deposit" | "withdraw";

/** The assets the custody wallet serves come first (design §3 #2), the rest by code. */
const PREFERRED = ["USDT", "BTC", "ETH"];

/** sortAssets orders asset codes as the pages list them: USDT, BTC, ETH, then alphabetically. */
export function sortAssets(codes: Iterable<string>): string[] {
  const rank = (c: string) => {
    const i = PREFERRED.indexOf(c);
    return i < 0 ? PREFERRED.length : i;
  };
  return [...new Set(codes)].sort((a, b) => rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0));
}

/** openFor reports whether a network takes deposits (or withdrawals) now. */
export function openFor(n: Pick<WalletNetwork, "deposit_enabled" | "withdraw_enabled">, purpose: Purpose): boolean {
  return purpose === "deposit" ? n.deposit_enabled : n.withdraw_enabled;
}

/** assetsFor lists the assets with at least one network open for the purpose. */
export function assetsFor(networks: readonly WalletNetwork[], purpose: Purpose): string[] {
  return sortAssets(networks.filter((n) => openFor(n, purpose)).map((n) => n.asset));
}

/**
 * networksFor lists an asset's networks for a page: the open ones first,
 * then the paused ones (shown, but not selectable).
 */
export function networksFor(networks: readonly WalletNetwork[], asset: string, purpose: Purpose): WalletNetwork[] {
  const mine = networks.filter((n) => n.asset === asset);
  return [...mine.filter((n) => openFor(n, purpose)), ...mine.filter((n) => !openFor(n, purpose))];
}

/**
 * pickNetwork keeps the chosen network while it is still open, else takes
 * the only open one; with several, the user chooses (null).
 */
export function pickNetwork(list: readonly WalletNetwork[], chosen: string | null | undefined, purpose: Purpose): string | null {
  const open = list.filter((n) => openFor(n, purpose));
  if (chosen && open.some((n) => n.network === chosen)) return chosen;
  return open.length === 1 ? open[0]!.network : null;
}

export type WalletFlow = {
  /** The asset's networks for the page, the open ones first. */
  list: WalletNetwork[];
  /** The network in use: the chosen one while open, else the only open one. */
  network: WalletNetwork | null;
  /** The asset has no network at all: it is only traded here. */
  internalOnly: boolean;
  /** The asset has networks, but none is open for the purpose now. */
  paused: boolean;
  /** Where the flow stands: 0 pick the coin, 1 pick the network, 2 the rest (address, amount). */
  step: 0 | 1 | 2;
};

/**
 * walletFlow works out the deposit or withdrawal flow from the networks
 * (undefined while loading), the chosen asset ("" for none) and the chosen
 * network (from the URL).
 */
export function walletFlow(
  networks: readonly WalletNetwork[] | undefined,
  asset: string,
  chosen: string | null | undefined,
  purpose: Purpose,
): WalletFlow {
  const list = asset ? networksFor(networks ?? [], asset, purpose) : [];
  const code = asset ? pickNetwork(list, chosen, purpose) : null;
  const network = list.find((n) => n.network === code) ?? null;
  const internalOnly = asset !== "" && networks !== undefined && list.length === 0;
  const paused = list.length > 0 && !list.some((n) => openFor(n, purpose));
  const step = !asset || internalOnly ? 0 : network ? 2 : 1;
  return { list, network, internalOnly, paused, step };
}

/**
 * explorerUrl fills a block explorer template ("…/tx/{tx}", "…/address/{address}")
 * with a hash or an address. It returns null without a template, for a
 * template that is not a web link or has no placeholder, and for the
 * ledger-only transfers between users (tx hash "internal:<id>").
 */
export function explorerUrl(template: string | null | undefined, value: string | null | undefined): string | null {
  if (!template || !value || value.startsWith("internal:")) return null;
  if (!/^https?:\/\//i.test(template) || !/\{(tx|address)\}/.test(template)) return null;
  return template.replace(/\{(tx|address)\}/g, encodeURIComponent(value));
}

/** isTestnet tells a test network (Sepolia, Nile, testnet, signet...) by its code, name or chain. */
export function isTestnet(n: Pick<WalletNetwork, "network" | "display_name" | "chain">): boolean {
  return /sepolia|goerli|holesky|testnet|nile|shasta|signet|regtest|chapel/i.test(`${n.network} ${n.display_name} ${n.chain}`);
}

/** shortAddress keeps the start and the end of a long address or hash: 0x5290…9EE7. */
export function shortAddress(value: string, head = 8, tail = 6): string {
  return value.length > head + tail + 1 ? `${value.slice(0, head)}…${value.slice(-tail)}` : value;
}

/** isUsable reports whether an address book entry's cooling-off period is over. */
export function isUsable(entry: Pick<WithdrawAddress, "usable_at">, now: number = Date.now()): boolean {
  const at = Date.parse(entry.usable_at);
  return Number.isNaN(at) || at <= now;
}
