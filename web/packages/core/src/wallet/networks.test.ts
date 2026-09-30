import { describe, expect, it } from "vitest";
import { assetsFor, explorerUrl, isTestnet, isUsable, networksFor, pickNetwork, shortAddress, sortAssets, walletFlow, type WalletNetwork } from "./networks";
import { addToBook, applyDepositPush, applyWithdrawalPush, prependItem, removeFromBook, type Pages } from "./push";
import type { Deposit, Withdrawal, WithdrawAddress } from "./networks";

const net = (asset: string, network: string, over: Partial<WalletNetwork> = {}): WalletNetwork => ({
  asset, network, display_name: network, chain: "1", address_format: "EVM", contract: null, confirmations: 12, eta_minutes: 3,
  min_deposit: "0.001", min_withdraw: "0.001", withdraw_fee: "0.0002", memo_required: false, deposit_enabled: true, withdraw_enabled: true,
  explorer_tx_url: "https://sepolia.etherscan.io/tx/{tx}", explorer_address_url: "https://sepolia.etherscan.io/address/{address}", ...over,
});

const list = [
  net("ETH", "ETH-SEPOLIA", { display_name: "Sepolia", chain: "11155111" }),
  net("USDT", "USDT-ERC20", { withdraw_enabled: false }),
  net("USDT", "USDT-TRC20", { address_format: "TRON" }),
  net("DOGE", "DOGE", { deposit_enabled: false, withdraw_enabled: false }),
  net("BTC", "BTC", { address_format: "BTC", deposit_enabled: false }),
];

describe("networks", () => {
  it("lists the assets open for deposits or withdrawals, USDT, BTC and ETH first", () => {
    expect(assetsFor(list, "deposit")).toEqual(["USDT", "ETH"]);
    expect(assetsFor(list, "withdraw")).toEqual(["USDT", "BTC", "ETH"]);
    expect(sortAssets(["SOL", "ETH", "ADA", "USDT", "ETH"])).toEqual(["USDT", "ETH", "ADA", "SOL"]);
  });

  it("lists an asset's networks, the paused ones last", () => {
    expect(networksFor(list, "USDT", "withdraw").map((n) => n.network)).toEqual(["USDT-TRC20", "USDT-ERC20"]);
    expect(networksFor(list, "USDT", "deposit").map((n) => n.network)).toEqual(["USDT-ERC20", "USDT-TRC20"]);
  });

  it("keeps an open choice, else takes the only open network", () => {
    const usdt = networksFor(list, "USDT", "deposit");
    expect(pickNetwork(usdt, "USDT-TRC20", "deposit")).toBe("USDT-TRC20");
    expect(pickNetwork(usdt, null, "deposit")).toBeNull();
    expect(pickNetwork(networksFor(list, "USDT", "withdraw"), "USDT-ERC20", "withdraw")).toBe("USDT-TRC20");
    expect(pickNetwork(networksFor(list, "ETH", "deposit"), undefined, "deposit")).toBe("ETH-SEPOLIA");
    expect(pickNetwork(networksFor(list, "DOGE", "deposit"), "DOGE", "deposit")).toBeNull();
  });

  it("works out the flow: coin, network, then the rest", () => {
    // Still loading: an asset from the URL waits on the network step.
    expect(walletFlow(undefined, "ETH", null, "deposit")).toMatchObject({ step: 1, network: null, internalOnly: false, paused: false });
    expect(walletFlow(list, "", null, "deposit")).toMatchObject({ step: 0, network: null });
    // A lone open network is taken at once.
    const eth = walletFlow(list, "ETH", null, "deposit");
    expect(eth.step).toBe(2);
    expect(eth.network?.network).toBe("ETH-SEPOLIA");
    // Several open: the user picks, and a pick that is open holds.
    expect(walletFlow(list, "USDT", null, "deposit")).toMatchObject({ step: 1, network: null });
    expect(walletFlow(list, "USDT", "USDT-TRC20", "deposit").network?.network).toBe("USDT-TRC20");
    // No network at all: trade only; networks all paused: paused.
    expect(walletFlow(list, "SOL", null, "deposit")).toMatchObject({ step: 0, internalOnly: true, paused: false });
    expect(walletFlow(list, "DOGE", "DOGE", "withdraw")).toMatchObject({ step: 1, internalOnly: false, paused: true, network: null });
    expect(walletFlow(list, "BTC", null, "deposit")).toMatchObject({ step: 1, paused: true });
    expect(walletFlow(list, "BTC", null, "withdraw").step).toBe(2);
  });

  it("builds explorer links from the templates", () => {
    expect(explorerUrl("https://sepolia.etherscan.io/tx/{tx}", "0xabc")).toBe("https://sepolia.etherscan.io/tx/0xabc");
    expect(explorerUrl("https://tronscan.org/#/address/{address}", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t")).toBe(
      "https://tronscan.org/#/address/TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
    );
    expect(explorerUrl(null, "0xabc")).toBeNull();
    expect(explorerUrl("https://sepolia.etherscan.io/tx/{tx}", "internal:0192")).toBeNull();
    expect(explorerUrl("javascript:alert(1)//{tx}", "0xabc")).toBeNull();
    expect(explorerUrl("https://example.com/tx/", "0xabc")).toBeNull();
    expect(explorerUrl("https://example.com/tx/{tx}", "a/b?c")).toBe("https://example.com/tx/a%2Fb%3Fc");
  });

  it("tells test networks, shortens addresses and reads cooling-off periods", () => {
    expect(isTestnet(list[0]!)).toBe(true);
    expect(isTestnet(net("USDT", "USDT-TRC20", { display_name: "TRC20", chain: "tron" }))).toBe(false);
    expect(shortAddress("0x52908400098527886E0F7030069857D2E4169EE7")).toBe("0x529084…169EE7");
    expect(shortAddress("short")).toBe("short");
    const now = Date.parse("2026-09-30T10:00:00Z");
    expect(isUsable({ usable_at: "2026-09-30T09:59:59Z" }, now)).toBe(true);
    expect(isUsable({ usable_at: "2026-09-30T10:00:01Z" }, now)).toBe(false);
  });
});

const dep = (id: string, over: Partial<Deposit> = {}): Deposit => ({
  id, kind: "CHAIN", asset: "ETH", network: "ETH-SEPOLIA", address: "0x1", contract: null, tx_hash: `0x${id}`, log_index: -1, block_number: 1,
  amount: "0.1", raw_amount: "100000000000000000", confirmations: 1, required_confirmations: 12, status: "CONFIRMING", unclaimed: false,
  reason: null, detected_at: "2026-09-30T10:00:00Z", confirmed_at: null, credited_at: null, ...over,
});

const pages = <T>(...lists: T[][]): Pages<T> => ({ pages: lists.map((items) => ({ items, next_cursor: null })), pageParams: lists.map(() => "") });

describe("pushes", () => {
  it("updates a deposit in place, wherever its page", () => {
    const data = pages([dep("a")], [dep("b")]);
    const next = applyDepositPush(data, {
      deposit_id: "b", asset: "ETH", network: "ETH-SEPOLIA", tx_hash: "0xb", amount: "0.1", status: "CONFIRMING", confirmations: 7,
      required_confirmations: 12, unclaimed: false, reason: null,
    });
    expect(next?.pages[1]?.items[0]).toMatchObject({ id: "b", confirmations: 7, address: "0x1" });
    expect(next?.pages[0]).toBe(data.pages[0]);
  });

  it("shows a newly detected deposit at the top", () => {
    const next = applyDepositPush(
      pages([dep("a")]),
      { deposit_id: "n", asset: "ETH", network: "ETH-SEPOLIA", tx_hash: "0xn", amount: "0.5", status: "DETECTED", confirmations: 0, required_confirmations: 12, unclaimed: false, reason: null },
      "2026-09-30T11:00:00Z",
    );
    expect(next?.pages[0]?.items.map((d) => d.id)).toEqual(["n", "a"]);
    expect(next?.pages[0]?.items[0]).toMatchObject({ amount: "0.5", status: "DETECTED", detected_at: "2026-09-30T11:00:00Z" });
    expect(applyDepositPush(undefined, { deposit_id: "n" } as never)).toBeUndefined();
  });

  it("updates a withdrawal and ignores one it does not know", () => {
    const w = { id: "w1", status: "APPROVED", tx_hash: null, confirmations: 0, required_confirmations: 12 } as unknown as Withdrawal;
    const data = pages([w]);
    const next = applyWithdrawalPush(data, { withdrawal_id: "w1", asset: "ETH", amount: "1", status: "BROADCAST", tx_hash: "0xt", confirmations: 0, required_confirmations: 12 });
    expect(next?.pages[0]?.items[0]).toMatchObject({ status: "BROADCAST", tx_hash: "0xt" });
    expect(applyWithdrawalPush(data, { withdrawal_id: "zz", asset: "ETH", amount: "1", status: "BROADCAST", tx_hash: null, confirmations: 0, required_confirmations: 12 })).toBe(data);
  });

  it("prepends a new record once", () => {
    const data = pages([{ id: "a" }, { id: "b" }]);
    expect(prependItem(data, { id: "c" }, (x) => x.id)?.pages[0]?.items.map((x) => x.id)).toEqual(["c", "a", "b"]);
    expect(prependItem(data, { id: "b" }, (x) => x.id)?.pages[0]?.items.map((x) => x.id)).toEqual(["a", "b"]);
    expect(prependItem(undefined, { id: "c" }, (x) => x.id)).toBeUndefined();
  });

  it("keeps the address book in step with adds and removals", () => {
    const e = (id: string) => ({ id }) as WithdrawAddress;
    expect(addToBook({ items: [e("a")] }, e("b"))?.items.map((x) => x.id)).toEqual(["b", "a"]);
    expect(addToBook({ items: [e("a"), e("b")] }, e("b"))?.items.map((x) => x.id)).toEqual(["b", "a"]);
    expect(removeFromBook({ items: [e("a"), e("b")] }, "a")?.items.map((x) => x.id)).toEqual(["b"]);
  });
});
