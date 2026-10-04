import { describe, expect, it } from "vitest";
import { searchMarkets } from "./search";

const rows = [
  { base: "ENA", symbol: "ENA-USDT", name: "Ethena" },
  { base: "ENS", symbol: "ENS-USDT", name: "Ethereum Name Service" },
  { base: "ETC", symbol: "ETC-USDT", name: "Ethereum Classic" },
  { base: "ETHFI", symbol: "ETHFI-USDT", name: "ether.fi" },
  { base: "ETH", symbol: "ETH-BTC", name: "Ethereum" },
  { base: "ETH", symbol: "ETH-USDT", name: "Ethereum" },
  { base: "BTC", symbol: "BTC-USDT", name: "Bitcoin" },
];

describe("searchMarkets", () => {
  it("puts the coin's own code first, then codes that start with the query, then names", () => {
    expect(searchMarkets(rows, " eth ").map((r) => r.symbol)).toEqual(["ETH-BTC", "ETH-USDT", "ETHFI-USDT", "ENA-USDT", "ENS-USDT", "ETC-USDT"]);
  });
  it("finds a quote in the symbol and keeps everything for an empty query", () => {
    expect(searchMarkets(rows, "btc").map((r) => r.symbol)).toEqual(["BTC-USDT", "ETH-BTC"]);
    expect(searchMarkets(rows, "")).toHaveLength(rows.length);
    expect(searchMarkets(rows, "doge")).toEqual([]);
  });
});
