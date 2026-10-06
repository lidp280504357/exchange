import { describe, expect, it } from "vitest";
import { convertValue, dayChange, distribution, isSmall, referenceChange, referencePrice, valuePortfolio, type Tickers } from "./valuation";

const tickers: Tickers = new Map([
  ["BTC-USDT", { last: "63214.5" }],
  ["ETH-USDT", { last: null }], // listed, never traded
  ["ETH-BTC", { last: "0.04" }],
  ["SOL-USDT", { last: "150.25" }],
  ["BTC-USDT-PERP", { last: "63300" }],
]);

describe("referencePrice", () => {
  it("counts USDT as 1 and prices through the USDT pair", () => {
    expect(referencePrice("USDT", tickers)).toBe("1");
    expect(referencePrice("BTC", tickers)).toBe("63214.5");
    expect(referencePrice("SOL", tickers)).toBe("150.25");
  });

  it("goes through BTC when the USDT pair has no price", () => {
    expect(referencePrice("ETH", tickers)).toBe("2528.58"); // 0.04 × 63214.5
  });

  it("gives up without a path", () => {
    expect(referencePrice("DOGE", tickers)).toBeNull();
    expect(referencePrice("BTC", new Map())).toBeNull();
    expect(referencePrice("ETH", new Map([["ETH-BTC", { last: "0.04" }]]))).toBeNull();
    expect(referencePrice("XRP", new Map([["XRP-USDT", { last: "0" }]]))).toBeNull();
  });
});

const prices = (a: string) => referencePrice(a, tickers);

describe("valuePortfolio", () => {
  const balances = [
    { account_type: "SPOT", asset: "USDT", available: "1000", frozen: "250.5", total: "1250.5" },
    { account_type: "SPOT", asset: "BTC", available: "0.01", frozen: "0", total: "0.01" },
    { account_type: "SPOT", asset: "DOGE", available: "500", frozen: "0", total: "500" },
    { account_type: "SPOT", asset: "ETH", available: "0", frozen: "0", total: "0" },
    { account_type: "FUTURES", asset: "USDT", available: "300", frozen: "200", total: "500" },
  ];

  it("values each account exactly and adds them up", () => {
    const p = valuePortfolio(balances, prices);
    expect(p.spot).toBe("1882.645"); // 1250.5 + 0.01 × 63214.5
    expect(p.futures).toBe("500");
    expect(p.total).toBe("2382.645");
  });

  it("lists unpriced holdings, valued at 0", () => {
    const p = valuePortfolio(balances, prices);
    expect(p.unpriced).toEqual(["DOGE"]);
    expect(p.rows.SPOT.find((r) => r.asset === "DOGE")?.value).toBeNull();
  });

  it("sorts rows by value, the unpriced after, and merges the accounts per asset", () => {
    const p = valuePortfolio(balances, prices);
    expect(p.rows.SPOT.map((r) => r.asset)).toEqual(["USDT", "BTC", "ETH", "DOGE"]);
    const usdt = p.rows.ALL.find((r) => r.asset === "USDT");
    expect(usdt).toMatchObject({ available: "1300", frozen: "450.5", total: "1750.5", value: "1750.5" });
    expect(p.rows.FUTURES).toHaveLength(1);
  });

  it("is zero without balances", () => {
    expect(valuePortfolio([], prices)).toMatchObject({ total: "0", spot: "0", futures: "0", margin: "0", unpriced: [] });
  });

  it("adds the margin accounts' nets, coin by coin, owed coins counting against", () => {
    const cross = { balances: [{ asset: "USDT", net: "-400" }, { asset: "BTC", net: "0.01" }] };
    const isolated = { balances: [{ asset: "USDT", net: "100" }, { asset: "SHIB", net: "5" }] };
    const p = valuePortfolio(balances, prices, [cross, isolated]);
    expect(p.margin).toBe("332.145"); // -400 + 100 + 0.01 × 63214.5
    expect(p.total).toBe("2714.79"); // 2382.645 + 332.145
    expect(p.marginRows).toEqual([
      { asset: "USDT", value: "-300" },
      { asset: "BTC", value: "632.145" },
      { asset: "SHIB", value: null },
    ]);
    expect(p.unpriced).toEqual(["DOGE", "SHIB"]);
    // A coin held and owed alike is not unpriced.
    expect(valuePortfolio([], prices, [{ balances: [{ asset: "SHIB", net: "0" }] }]).unpriced).toEqual([]);
  });
});

describe("isSmall", () => {
  it("hides empty rows and rows under 1 USDT, never unpriced ones", () => {
    expect(isSmall({ total: "0", value: "0" })).toBe(true);
    expect(isSmall({ total: "0.00001", value: "0.63" })).toBe(true);
    expect(isSmall({ total: "1", value: "1" })).toBe(false);
    expect(isSmall({ total: "500", value: null })).toBe(false);
    expect(isSmall({ total: "0", value: null })).toBe(true);
  });
});

describe("distribution", () => {
  const row = (asset: string, value: string | null) => ({ asset, available: "1", frozen: "0", total: "1", price: "1", value });

  it("keeps the top five and folds the rest into others", () => {
    const rows = [row("A", "40"), row("B", "20"), row("C", "10"), row("D", "10"), row("E", "10"), row("F", "6"), row("G", "4"), row("H", null)];
    const s = distribution(rows);
    expect(s.map((x) => x.asset)).toEqual(["A", "B", "C", "D", "E", null]);
    expect(s.map((x) => x.share)).toEqual(["0.4", "0.2", "0.1", "0.1", "0.1", "0.1"]);
    expect(s[5]?.value).toBe("10");
  });

  it("has no others slice when five or fewer assets hold value", () => {
    const s = distribution([row("A", "3"), row("B", "0"), row("C", "1")]);
    expect(s.map((x) => [x.asset, x.share])).toEqual([["A", "0.75"], ["C", "0.25"]]);
  });

  it("is empty when nothing is valued", () => {
    expect(distribution([row("A", null), row("B", "0")])).toEqual([]);
  });
});

describe("convertValue", () => {
  it("expresses a USDT value in BTC, cut down", () => {
    expect(convertValue("2382.645", "63214.5", 8)).toBe("0.03769143");
    expect(convertValue("100", null, 8)).toBeNull();
  });
});

describe("referenceChange", () => {
  const moving: Tickers = new Map([
    ["BTC-USDT", { last: "63214.5", change: "0.02" }],
    ["ETH-USDT", { last: null, change: null }],
    ["ETH-BTC", { last: "0.04", change: "-0.01" }],
    ["SOL-USDT", { last: "150.25", change: "-0.05" }],
  ]);

  it("follows the path of the price", () => {
    expect(referenceChange("USDT", moving)).toBe("0");
    expect(referenceChange("SOL", moving)).toBe("-0.05");
    expect(referenceChange("ETH", moving)).toBe("0.0098"); // 0.99 × 1.02 − 1
  });

  it("has none without a priced pair", () => {
    expect(referenceChange("DOGE", moving)).toBeNull();
    expect(referenceChange("BTC", new Map([["BTC-USDT", { last: "63214.5" }]]))).toBeNull();
  });
});

describe("dayChange", () => {
  const changes: Record<string, string | null> = { USDT: "0", BTC: "0.25", SOL: "-0.2", DOGE: null };
  const row = (asset: string, value: string | null) => ({ asset, value });

  it("adds each holding's move over the day", () => {
    // BTC worth 125 now was 100: +25; SOL worth 80 was 100: −20.
    const d = dayChange([row("USDT", "1000"), row("BTC", "125"), row("SOL", "80"), row("DOGE", "7"), row("XRP", null)], (a) => changes[a] ?? null);
    expect(d.value).toBe("5");
    expect(d.ratio).toBe("0.004142"); // 5 ÷ 1207 (DOGE counts, unmoved), cut down
  });

  it("is zero, without a ratio, for nothing held", () => {
    expect(dayChange([], () => "0.1")).toEqual({ value: "0", ratio: null });
  });
});
