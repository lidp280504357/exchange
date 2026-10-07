import { describe, expect, it } from "vitest";
import type { TickerData } from "../ws/types";
import {
  buildRows,
  categoryCount,
  categoryTags,
  filterRows,
  isNewListing,
  marqueeRows,
  normalizeQuery,
  parseCategory,
  parseSort,
  rankHighlights,
  rankOverview,
  sortRows,
  staleSymbols,
  tagLabel,
  type ContractLike,
  type PairLike,
} from "./list";

const listed = "2026-09-30T10:10:50Z";
const now = Date.parse("2026-10-05T00:00:00Z");

function pair(symbol: string, over: Partial<PairLike> = {}): PairLike {
  const [base = "", quote = ""] = symbol.split("-");
  return {
    symbol,
    base_asset: base,
    quote_asset: quote,
    base_name: base,
    rank: null,
    categories: [],
    price_decimals: 2,
    status: "TRADING",
    listed_at: listed,
    ...over,
  };
}

const pairs: PairLike[] = [
  pair("ETH-BTC", { base_name: "Ethereum", rank: 2, categories: ["layer-1", "smart-contracts"], price_decimals: 5 }),
  pair("BTC-USDT", { base_name: "Bitcoin", rank: 1, categories: ["layer-1", "pow"], reference_symbol: "BTCUSDT" }),
  pair("ETH-USDT", { base_name: "Ethereum", rank: 2, categories: ["layer-1", "smart-contracts"], status: "PREPARE" }),
  pair("PEPE-USDT", { base_name: "Pepe", rank: null, categories: ["meme"], listed_at: "2026-01-01T00:00:00Z" }),
  pair("OLD-USDT", { status: "DELISTED" }),
];

const contracts: ContractLike[] = [
  { symbol: "BTC-USDT-PERP", base_asset: "BTC", quote_asset: "USDT", index_symbol: "BTC-USDT", tick_size: "0.1", status: "TRADING", max_leverage: 50 },
];

function ticker(symbol: string, over: Partial<TickerData> = {}): TickerData {
  return {
    symbol,
    last: "1",
    open: "1",
    high: "1",
    low: "1",
    volume: "0",
    quote_volume: "0",
    trade_count: 0,
    change: "0",
    bid: null,
    ask: null,
    updated_at: listed,
    ...over,
  };
}

const rows = buildRows(pairs, contracts);
const symbols = (list: { symbol: string }[]) => list.map((r) => r.symbol);

describe("buildRows", () => {
  it("lists pairs and contracts, drops delisted ones, and gives contracts their index pair's data", () => {
    expect(symbols(rows)).toEqual(["ETH-BTC", "BTC-USDT", "ETH-USDT", "PEPE-USDT", "BTC-USDT-PERP"]);
    const perp = rows.find((r) => r.symbol === "BTC-USDT-PERP")!;
    expect(perp).toMatchObject({ kind: "perp", base: "BTC", name: "Bitcoin", rank: 1, priceDecimals: 1, maxLeverage: 50, listedAt: listed });
    expect(perp.categories).toEqual(["layer-1", "pow"]);
  });
});

describe("filterRows", () => {
  it("matches symbols in any spelling and names in both languages", () => {
    const q = (query: string) => symbols(filterRows(rows, { category: "all", query, now }));
    expect(q("btc/usdt")).toEqual(["BTC-USDT", "BTC-USDT-PERP"]);
    expect(q("BTCUSDT")).toEqual(["BTC-USDT", "BTC-USDT-PERP"]);
    expect(q("  eth-btc ")).toEqual(["ETH-BTC"]);
    expect(q("bitcoin")).toEqual(["BTC-USDT", "BTC-USDT-PERP"]);
    expect(q("以太")).toEqual(["ETH-BTC", "ETH-USDT"]);
    expect(q("perp")).toEqual(["BTC-USDT-PERP"]);
    expect(q("")).toHaveLength(5);
    expect(q("nothing")).toEqual([]);
  });

  it("filters by category", () => {
    const c = (category: ReturnType<typeof parseCategory>, favorites = new Set<string>()) => symbols(filterRows(rows, { category, favorites, now }));
    expect(c("spot")).toEqual(["ETH-BTC", "BTC-USDT", "ETH-USDT", "PEPE-USDT"]);
    expect(c("futures")).toEqual(["BTC-USDT-PERP"]);
    expect(c("favorites", new Set(["BTC-USDT-PERP", "ETH-BTC"]))).toEqual(["ETH-BTC", "BTC-USDT-PERP"]);
    expect(c("tag:meme")).toEqual(["PEPE-USDT"]);
    expect(c("tag:pow")).toEqual(["BTC-USDT", "BTC-USDT-PERP"]);
    // PEPE was listed long ago; the others within 30 days; ETH-USDT is coming soon.
    expect(c("new")).toEqual(["ETH-BTC", "BTC-USDT", "ETH-USDT", "BTC-USDT-PERP"]);
  });

  it("counts categories and tags", () => {
    expect(categoryCount(rows, "futures", new Set(), now)).toBe(1);
    expect(categoryTags(rows)).toEqual([
      { tag: "layer-1", count: 4 },
      { tag: "pow", count: 2 },
      { tag: "smart-contracts", count: 2 },
      { tag: "meme", count: 1 },
    ]);
  });
});

describe("isNewListing", () => {
  it("counts coming-soon pairs and recent listings", () => {
    expect(isNewListing({ status: "PREPARE", listedAt: "" }, now)).toBe(true);
    expect(isNewListing({ status: "TRADING", listedAt: "2026-09-10T00:00:00Z" }, now)).toBe(true);
    expect(isNewListing({ status: "TRADING", listedAt: "2026-08-01T00:00:00Z" }, now)).toBe(false);
    expect(isNewListing({ status: "TRADING", listedAt: "" }, now)).toBe(false);
  });
});

describe("parse helpers", () => {
  it("reads categories and sorts from URL parameters", () => {
    expect(parseCategory("futures")).toBe("futures");
    expect(parseCategory("tag:layer-1")).toBe("tag:layer-1");
    expect(parseCategory("tag:<script>")).toBe("all");
    expect(parseCategory(null)).toBe("all");
    expect(parseSort("change", "asc")).toEqual({ key: "change", desc: false });
    expect(parseSort("turnover", null)).toEqual({ key: "turnover", desc: true });
    expect(parseSort("bogus", "asc")).toBeNull();
  });

  it("normalizes queries and labels tags", () => {
    expect(normalizeQuery(" BTC / usdt ")).toBe("btcusdt");
    expect(tagLabel("layer-1")).toBe("Layer 1");
    expect(tagLabel("smart-contracts")).toBe("Smart Contracts");
  });
});

describe("sortRows", () => {
  const tickers = new Map<string, TickerData>([
    ["BTC-USDT", ticker("BTC-USDT", { last: "85226.01", change: "0.0108", quote_volume: "1352353240.3" })],
    ["BTC-USDT-PERP", ticker("BTC-USDT-PERP", { last: "85226.1", change: "0.0108", quote_volume: "1352353240.3" })],
    ["ETH-USDT", ticker("ETH-USDT", { last: "2730.63", change: "-0.0025", quote_volume: "735996609.4" })],
    ["ETH-BTC", ticker("ETH-BTC", { last: "0.0405", change: "0.0075", quote_volume: "0.017" })],
    ["PEPE-USDT", ticker("PEPE-USDT", { last: null, change: null, quote_volume: "0" })],
  ]);
  const of = (s: string) => tickers.get(s);

  it("keeps the default order: rank, spot before contract, USDT first, unranked last", () => {
    expect(symbols(sortRows(rows, of, null))).toEqual(["BTC-USDT", "BTC-USDT-PERP", "ETH-USDT", "ETH-BTC", "PEPE-USDT"]);
    expect(symbols(sortRows(rows, of, { key: "rank", desc: true }))).toEqual(["ETH-BTC", "ETH-USDT", "BTC-USDT-PERP", "BTC-USDT", "PEPE-USDT"]);
  });

  it("sorts decimals exactly with missing values last either way", () => {
    expect(symbols(sortRows(rows, of, { key: "change", desc: true }))).toEqual(["BTC-USDT", "BTC-USDT-PERP", "ETH-BTC", "ETH-USDT", "PEPE-USDT"]);
    expect(symbols(sortRows(rows, of, { key: "change", desc: false }))).toEqual(["ETH-USDT", "ETH-BTC", "BTC-USDT", "BTC-USDT-PERP", "PEPE-USDT"]);
    expect(symbols(sortRows(rows, of, { key: "last", desc: true }))).toEqual(["BTC-USDT-PERP", "BTC-USDT", "ETH-USDT", "ETH-BTC", "PEPE-USDT"]);
    expect(symbols(sortRows(rows, of, { key: "turnover", desc: false }))[0]).toBe("PEPE-USDT");
  });

  it("sorts by symbol and by listing time", () => {
    expect(symbols(sortRows(rows, of, { key: "symbol", desc: false }))).toEqual(["BTC-USDT", "BTC-USDT-PERP", "ETH-BTC", "ETH-USDT", "PEPE-USDT"]);
    expect(symbols(sortRows(rows, of, { key: "listed", desc: true })).at(-1)).toBe("PEPE-USDT");
  });

  it("does not change its input", () => {
    const before = symbols(rows);
    sortRows(rows, of, { key: "change", desc: true });
    expect(symbols(rows)).toEqual(before);
  });
});

describe("home page picks", () => {
  it("fills the marquee with one market per coin first, then the others by rank", () => {
    expect(symbols(marqueeRows(rows, 10))).toEqual(["BTC-USDT", "BTC-USDT-PERP", "ETH-USDT", "ETH-BTC", "PEPE-USDT"]);
    expect(symbols(marqueeRows(rows, 2))).toEqual(["BTC-USDT", "ETH-USDT"]);
  });

  it("ranks the overview like /v1/market/summary: trading USDT pairs with a price", () => {
    const tickers = new Map<string, TickerData>([
      ["BTC-USDT", ticker("BTC-USDT", { change: "0.01", quote_volume: "100" })],
      ["A-USDT", ticker("A-USDT", { change: "0.05", quote_volume: "5" })],
      ["B-USDT", ticker("B-USDT", { change: "-0.03", quote_volume: "500" })],
      ["ETH-USDT", ticker("ETH-USDT", { change: "0.2", quote_volume: "900" })],
      ["ETH-BTC", ticker("ETH-BTC", { change: "0.3", quote_volume: "9" })],
    ]);
    const list = buildRows([...pairs, pair("A-USDT", { rank: 5 }), pair("B-USDT", { rank: 6 })], contracts);
    const o = rankOverview(list, (s) => tickers.get(s), 2);
    expect(symbols(o.gainers)).toEqual(["A-USDT", "BTC-USDT"]);
    expect(symbols(o.losers)).toEqual(["B-USDT", "BTC-USDT"]);
    expect(symbols(o.turnover)).toEqual(["B-USDT", "BTC-USDT"]);
    // Spot closed (product line switches §1 #2): the contracts rank instead.
    expect(symbols(rankOverview(buildRows([], contracts), (s) => ticker(s, { change: "0.01" }), 2).gainers)).toEqual(["BTC-USDT-PERP"]);
  });
});

describe("markets page boards", () => {
  const tickers = new Map<string, TickerData>([
    ["BTC-USDT", ticker("BTC-USDT", { change: "0.0108", quote_volume: "1352353240.3" })],
    ["BTC-USDT-PERP", ticker("BTC-USDT-PERP", { change: "0.0108", quote_volume: "1352353240.3" })],
    ["ETH-USDT", ticker("ETH-USDT", { change: "-0.0025", quote_volume: "735996609.4" })],
    ["ETH-BTC", ticker("ETH-BTC", { change: "0.0075", quote_volume: "0.017" })],
  ]);
  const of = (s: string) => tickers.get(s);

  it("ranks every trading market, contracts included", () => {
    const h = rankHighlights(rows, of, now, 3);
    expect(symbols(h.hot)).toEqual(["BTC-USDT", "BTC-USDT-PERP"]);
    expect(symbols(h.gainers)).toEqual(["BTC-USDT", "BTC-USDT-PERP", "ETH-BTC"]);
    expect(symbols(h.losers)).toEqual(["ETH-BTC", "BTC-USDT", "BTC-USDT-PERP"]);
    expect(h.newest).toHaveLength(3);
    expect(h.newest.map((r) => r.symbol)).not.toContain("PEPE-USDT");
  });

  it("ranks the coin-margined contracts by turnover when nothing is quoted in USDT (the other lines closed)", () => {
    const coin: ContractLike[] = [
      { symbol: "BTC-USD-PERP", base_asset: "BTC", quote_asset: "USD", index_symbol: "BTC-USDT", tick_size: "0.1", status: "TRADING", max_leverage: 125 },
      { symbol: "ETH-USD-PERP", base_asset: "ETH", quote_asset: "USD", index_symbol: "ETH-USDT", tick_size: "0.01", status: "TRADING", max_leverage: 125 },
    ];
    const usd = new Map<string, TickerData>([
      ["BTC-USD-PERP", ticker("BTC-USD-PERP", { quote_volume: "900" })],
      ["ETH-USD-PERP", ticker("ETH-USD-PERP", { quote_volume: "1200" })],
    ]);
    expect(symbols(rankHighlights(buildRows([], coin), (s) => usd.get(s), now, 3).hot)).toEqual(["ETH-USD-PERP", "BTC-USD-PERP"]);
  });

  it("flags stalled reference tickers only", () => {
    const at = (iso: string) => new Map([["BTC-USDT", ticker("BTC-USDT", { updated_at: iso })], ["ETH-BTC", ticker("ETH-BTC", { updated_at: "2026-01-01T00:00:00Z" })]]);
    const fresh = at(new Date(now - 5_000).toISOString());
    const stale = at(new Date(now - 45_000).toISOString());
    expect(staleSymbols(rows, (s) => fresh.get(s), now)).toEqual([]);
    // BTC-USDT and its contract follow BTCUSDT; ETH-BTC shows the platform's own ticker and is never stale.
    expect(staleSymbols(rows, (s) => stale.get(s), now)).toEqual(["BTC-USDT"]);
    expect(rows.find((r) => r.symbol === "BTC-USDT-PERP")?.reference).toBe(true);
  });
});
