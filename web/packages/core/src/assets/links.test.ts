import { describe, expect, it } from "vitest";
import { contractFor, tradeSymbolFor } from "./links";

const pair = (symbol: string, status = "TRADING") => {
  const [base_asset = "", quote_asset = ""] = symbol.split("-");
  return { symbol, base_asset, quote_asset, status };
};

describe("tradeSymbolFor", () => {
  const pairs = [pair("BTC-USDT"), pair("ETH-BTC"), pair("ETH-USDT", "PREPARE"), pair("OLD-USDT", "DELISTED")];

  it("prefers the USDT pair, trading ones first", () => {
    expect(tradeSymbolFor("BTC", pairs)).toBe("BTC-USDT");
    expect(tradeSymbolFor("ETH", pairs)).toBe("ETH-BTC");
    expect(tradeSymbolFor("ETH", [pair("ETH-BTC"), pair("ETH-USDT")])).toBe("ETH-USDT");
  });

  it("falls back to a pair quoted in the asset, or none", () => {
    expect(tradeSymbolFor("USDT", pairs)).toBe("BTC-USDT");
    expect(tradeSymbolFor("OLD", pairs)).toBeNull();
    expect(tradeSymbolFor("DOGE", pairs)).toBeNull();
  });
});

describe("contractFor", () => {
  const contracts = [
    { symbol: "BTC-USDT-PERP", base_asset: "BTC", quote_asset: "USDT", status: "TRADING" },
    { symbol: "ETH-USDT-PERP", base_asset: "ETH", quote_asset: "USDT", status: "TRADING" },
  ];
  it("finds the asset's perpetual, or one settled in it", () => {
    expect(contractFor("ETH", contracts)).toBe("ETH-USDT-PERP");
    expect(contractFor("USDT", contracts)).toBe("BTC-USDT-PERP");
    expect(contractFor("SOL", contracts)).toBeNull();
  });
});
