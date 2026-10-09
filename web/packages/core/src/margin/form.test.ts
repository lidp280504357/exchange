import { describe, expect, it } from "vitest";
import { accountOf, assetChoices, inwardOf, keyFor, settle, transferInMax, transferOutMax } from "./form";

describe("forms", () => {
  const cross = {
    account: "MARGIN_CROSS", symbol: null, leverage: 3, status: "NORMAL", margin_level: "2.5", warn_level: "1.3",
    liquidation_level: "1.1", total_asset: "250", total_liability: "100", net_asset: "150", liquidation_price: null,
    balances: [
      { asset: "USDT", free: "250", locked: "0", borrowed: "100", interest: "0.001", net: "149.999" },
      { asset: "BTC", free: "0.01", locked: "0", borrowed: "0", interest: "0", net: "0.01" },
    ],
    updated_at: "2026-10-06T00:00:00Z",
  } as const;
  const isolated = { ...cross, account: "MARGIN_ISOLATED", symbol: "ETH-USDT", balances: [] } as const;
  const terms = [
    { asset: "USDT", borrowable: true, collateral: true },
    { asset: "ASTRA", borrowable: false, collateral: true },
    { asset: "BTC", borrowable: true, collateral: true },
  ];

  it("finds the cross account or an isolated one", () => {
    const data = { cross: { ...cross, balances: [...cross.balances] }, isolated: [{ ...isolated, balances: [] }] };
    expect(accountOf(data, "MARGIN_CROSS", "")?.account).toBe("MARGIN_CROSS");
    expect(accountOf(data, "MARGIN_ISOLATED", "ETH-USDT")?.symbol).toBe("ETH-USDT");
    expect(accountOf(data, "MARGIN_ISOLATED", "BTC-USDT")).toBeUndefined();
    expect(accountOf(undefined, "MARGIN_CROSS", "")).toBeUndefined();
  });

  it("offers what the account may move, borrow or repay", () => {
    const owner = { ...cross, balances: [...cross.balances] };
    expect(assetChoices("transfer", "MARGIN_CROSS", undefined, terms, owner)).toEqual(["USDT", "ASTRA", "BTC"]);
    expect(assetChoices("borrow", "MARGIN_CROSS", undefined, terms, owner)).toEqual(["USDT", "BTC"]);
    expect(assetChoices("repay", "MARGIN_CROSS", undefined, terms, owner)).toEqual(["USDT"]);
    expect(assetChoices("repay", "MARGIN_CROSS", undefined, terms, { ...owner, balances: [] })).toEqual(["USDT", "BTC"]);
    expect(assetChoices("borrow", "MARGIN_ISOLATED", { base: "ETH", quote: "USDT" }, terms, undefined)).toEqual(["ETH", "USDT"]);
    expect(assetChoices("transfer", "MARGIN_ISOLATED", undefined, terms, undefined)).toEqual([]);
    // Spot closed: a transfer in only repays what the account owes; out is as before (F20).
    expect(assetChoices("transfer", "MARGIN_CROSS", undefined, terms, owner, { direction: "IN", repayOnly: true })).toEqual(["USDT"]);
    expect(assetChoices("transfer", "MARGIN_CROSS", undefined, terms, { ...owner, balances: [] }, { direction: "IN", repayOnly: true })).toEqual([]);
    expect(assetChoices("transfer", "MARGIN_CROSS", undefined, terms, owner, { direction: "OUT", repayOnly: true })).toEqual(["USDT", "ASTRA", "BTC"]);
    expect(assetChoices("transfer", "MARGIN_CROSS", undefined, terms, owner, { direction: "IN", repayOnly: false })).toEqual(["USDT", "ASTRA", "BTC"]);
  });
});

describe("transferOutMax", () => {
  it("is what is free, no more than the net, never below zero", () => {
    expect(transferOutMax({ free: "120", net: "20" })).toBe("20");
    expect(transferOutMax({ free: "5", net: "20" })).toBe("5");
    expect(transferOutMax({ free: "5", net: "-3" })).toBe("0");
  });
});

describe("transferInMax", () => {
  it("is the SPOT balance; while spot is closed no more than the debt, rounded up to repay it in full (F22)", () => {
    const debt = { borrowed: "100", interest: "0.0010001" };
    expect(transferInMax("500", debt, false, 6)).toBe("500");
    expect(transferInMax("500", debt, true, 6)).toBe("100.001001");
    expect(transferInMax("20", debt, true, 6)).toBe("20");
    expect(transferInMax("500", { borrowed: "0", interest: "0" }, true, 6)).toBe("0");
  });
});

describe("inwardOf", () => {
  it("offers a transfer in, while spot is closed only to an account that owes, and until the accounts are read (F22)", () => {
    expect(inwardOf(false, false, [])).toBe(true);
    expect(inwardOf(true, true, [])).toBe(true);
    expect(inwardOf(true, false, [])).toBe(false);
    expect(inwardOf(true, false, ["USDT"])).toBe(true);
  });
});

describe("keyFor", () => {
  it("keeps an unsettled action's key until it is answered or ten minutes pass", () => {
    const now = Date.parse("2026-10-06T00:00:00Z");
    const first = keyFor("transfer:IN:a", now);
    expect(keyFor("transfer:IN:a", now + 60_000)).toBe(first);
    expect(keyFor("transfer:IN:b", now)).not.toBe(first);
    settle("transfer:IN:a");
    const second = keyFor("transfer:IN:a", now);
    expect(second).not.toBe(first);
    expect(keyFor("transfer:IN:a", now + 11 * 60_000)).not.toBe(second);
  });
});
