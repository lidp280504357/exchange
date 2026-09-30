import { buildRows, parseSort, type ContractLike, type PairLike } from "@exchange/core/markets/index";
import { describe, expect, it } from "vitest";
import { SORT_OPTIONS, boardPath, categoryPills, coinFromParam, coinMarkets, maxLeverage, sortOptionOf, sortParams } from "./logic";

function pair(symbol: string, over: Partial<PairLike> = {}): PairLike {
  const [base = "", quote = ""] = symbol.split("-");
  return {
    symbol,
    base_asset: base,
    quote_asset: quote,
    base_name: base,
    rank: base === "BTC" ? 1 : base === "ETH" ? 2 : null,
    categories: ["layer-1"],
    price_decimals: 2,
    status: "TRADING",
    listed_at: "2026-09-30T00:00:00Z",
    reference_symbol: `${base}${quote}`,
    ...over,
  };
}

function contract(symbol: string, over: Partial<ContractLike> = {}): ContractLike {
  const base = symbol.split("-")[0]!;
  return { symbol, base_asset: base, quote_asset: "USDT", index_symbol: `${base}-USDT`, tick_size: "0.1", status: "TRADING", max_leverage: 50, ...over };
}

const rows = buildRows(
  [pair("ETH-BTC", { price_decimals: 5 }), pair("BTC-USDT"), pair("ETH-USDT", { status: "PREPARE" }), pair("SOL-USDT", { status: "HALT" })],
  [contract("BTC-USDT-PERP", { max_leverage: 100 }), contract("ETH-USDT-PERP")],
);

describe("coinFromParam", () => {
  it("reads a coin from the path, whatever the case or pair", () => {
    expect(coinFromParam("btc")).toBe("BTC");
    expect(coinFromParam("BTC-USDT")).toBe("BTC");
    expect(coinFromParam("eth-usdt-perp")).toBe("ETH");
    expect(coinFromParam(" sol ")).toBe("SOL");
    expect(coinFromParam(undefined)).toBe("");
    expect(coinFromParam("")).toBe("");
  });
});

describe("coinMarkets", () => {
  it("prices a coin by its USDT spot pair and trades where orders are taken", () => {
    const eth = coinMarkets(rows, "ETH");
    expect(eth.markets.map((r) => r.symbol)).toEqual(["ETH-USDT", "ETH-BTC", "ETH-USDT-PERP"]);
    expect(eth.primary?.symbol).toBe("ETH-USDT");
    // ETH-USDT is not trading yet: the trade button goes to ETH-BTC.
    expect(eth.tradeTarget?.symbol).toBe("ETH-BTC");
    expect(eth.perp?.symbol).toBe("ETH-USDT-PERP");
  });

  it("falls back to any trading market, then to the primary", () => {
    const sol = coinMarkets(rows, "SOL");
    expect(sol.primary?.symbol).toBe("SOL-USDT");
    expect(sol.tradeTarget?.symbol).toBe("SOL-USDT");
    expect(sol.perp).toBeUndefined();
    const onlyPerp = coinMarkets(buildRows([], [contract("BTC-USDT-PERP")]), "BTC");
    expect(onlyPerp.primary?.symbol).toBe("BTC-USDT-PERP");
    expect(onlyPerp.tradeTarget?.symbol).toBe("BTC-USDT-PERP");
  });

  it("has nothing for a coin without markets", () => {
    const usdt = coinMarkets(rows, "USDT");
    expect(usdt.markets).toEqual([]);
    expect(usdt.primary).toBeUndefined();
    expect(usdt.tradeTarget).toBeUndefined();
  });
});

describe("categoryPills", () => {
  it("lists the fixed pills, then the sectors", () => {
    expect(categoryPills("all", ["layer-1", "defi"])).toEqual(["all", "favorites", "spot", "futures", "tag:layer-1", "tag:defi"]);
  });

  it("adds the category a link asked for when it is not a pill", () => {
    expect(categoryPills("new", ["defi"])).toEqual(["all", "favorites", "spot", "futures", "new", "tag:defi"]);
    expect(categoryPills("tag:meme", [])).toEqual(["all", "favorites", "spot", "futures", "tag:meme"]);
    expect(categoryPills("tag:defi", ["defi"])).toEqual(["all", "favorites", "spot", "futures", "tag:defi"]);
  });
});

describe("sort options", () => {
  it("name the order of the address, and turn back into it", () => {
    expect(sortOptionOf(null)).toBe("default");
    expect(sortOptionOf(parseSort("change", "desc"))).toBe("changeDesc");
    expect(sortOptionOf(parseSort("change", "asc"))).toBe("changeAsc");
    expect(sortOptionOf(parseSort("turnover", "desc"))).toBe("turnoverDesc");
    // An order the sheet does not offer (from a PC link) marks nothing.
    expect(sortOptionOf(parseSort("high", "desc"))).toBeNull();
    for (const o of SORT_OPTIONS) {
      const p = sortParams(o.sort);
      expect(sortOptionOf(parseSort(p.sort, p.dir))).toBe(o.id);
    }
    expect(sortParams(null)).toEqual({ sort: null, dir: null });
  });
});

describe("boards and figures", () => {
  it("links a board to the list in its order", () => {
    expect(boardPath("gainers")).toBe("/markets?sort=change&dir=desc");
    expect(boardPath("losers")).toBe("/markets?sort=change&dir=asc");
  });

  it("finds the largest leverage, or the fallback", () => {
    expect(maxLeverage(rows)).toBe(100);
    expect(maxLeverage(buildRows([pair("BTC-USDT")], []))).toBe(50);
  });
});
