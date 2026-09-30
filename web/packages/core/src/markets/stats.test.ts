import { describe, expect, it } from "vitest";
import type { TickerData } from "../ws/types";
import { buildRows } from "./list";
import { createLimiter, sparkValues } from "./sparkline";
import { compactParts, headline } from "./stats";

describe("compactParts", () => {
  it("scales exactly into Chinese and English units", () => {
    expect(compactParts("2088349849.68", "zh-CN")).toEqual({ value: "20.88", suffix: "亿" });
    expect(compactParts("2088349849.68", "en")).toEqual({ value: "2.09", suffix: "B" });
    expect(compactParts("735996609.375873", "en")).toEqual({ value: "736", suffix: "M" });
    expect(compactParts("12345", "zh-CN")).toEqual({ value: "1.23", suffix: "万" });
    expect(compactParts("999.999", "en")).toEqual({ value: "1000", suffix: "" });
    expect(compactParts("0", "en")).toEqual({ value: "0", suffix: "" });
    expect(compactParts("oops", "en")).toEqual({ value: "0", suffix: "" });
  });
});

describe("headline", () => {
  it("sums the USDT spot turnover and counts markets", () => {
    const rows = buildRows(
      [
        { symbol: "BTC-USDT", base_asset: "BTC", quote_asset: "USDT", base_name: "Bitcoin", rank: 1, categories: [], price_decimals: 2, status: "TRADING", listed_at: "" },
        { symbol: "ETH-BTC", base_asset: "ETH", quote_asset: "BTC", base_name: "Ethereum", rank: 2, categories: [], price_decimals: 5, status: "TRADING", listed_at: "" },
        { symbol: "ETH-USDT", base_asset: "ETH", quote_asset: "USDT", base_name: "Ethereum", rank: 2, categories: [], price_decimals: 2, status: "PREPARE", listed_at: "" },
      ],
      [
        { symbol: "BTC-USDT-PERP", base_asset: "BTC", quote_asset: "USDT", index_symbol: "BTC-USDT", tick_size: "0.1", status: "TRADING", max_leverage: 50 },
        { symbol: "ETH-USDT-PERP", base_asset: "ETH", quote_asset: "USDT", index_symbol: "ETH-USDT", tick_size: "0.01", status: "TRADING", max_leverage: 25 },
      ],
    );
    const t = (symbol: string, quote_volume: string) => ({ symbol, quote_volume }) as TickerData;
    const tickers = new Map([
      ["BTC-USDT", t("BTC-USDT", "1352353240.3036124")],
      ["ETH-USDT", t("ETH-USDT", "735996609.375873")],
      ["ETH-BTC", t("ETH-BTC", "0.017478")],
      ["BTC-USDT-PERP", t("BTC-USDT-PERP", "1352353240.3036124")],
    ]);
    expect(headline(rows, (s) => tickers.get(s))).toEqual({
      turnover: "2088349849.6794854",
      spot: 3,
      perps: 2,
      maxLeverage: 50,
      coins: 2,
    });
  });
});

describe("sparkValues", () => {
  it("keeps short series and thins long ones, first and last kept", () => {
    const candles = (n: number) => Array.from({ length: n }, (_, i) => ({ close: String(i) }));
    expect(sparkValues(candles(3))).toEqual(["0", "1", "2"]);
    const thin = sparkValues(candles(168), 56);
    expect(thin).toHaveLength(56);
    expect(thin[0]).toBe("0");
    expect(thin[55]).toBe("167");
    expect(thin.map(Number)).toEqual([...thin.map(Number)].sort((a, b) => a - b));
  });
});

describe("createLimiter", () => {
  it("runs at most max tasks at once, in order", async () => {
    const run = createLimiter(2);
    let active = 0;
    let peak = 0;
    const order: number[] = [];
    const task = (i: number) => () =>
      new Promise<number>((resolve) => {
        active++;
        peak = Math.max(peak, active);
        order.push(i);
        setTimeout(() => {
          active--;
          resolve(i);
        }, 5);
      });
    const results = await Promise.all([0, 1, 2, 3, 4].map((i) => run(task(i))));
    expect(results).toEqual([0, 1, 2, 3, 4]);
    expect(peak).toBe(2);
    expect(order).toEqual([0, 1, 2, 3, 4]);
  });

  it("keeps going after a failure", async () => {
    const run = createLimiter(1);
    await expect(run(() => Promise.reject(new Error("x")))).rejects.toThrow("x");
    await expect(run(() => Promise.resolve(7))).resolves.toBe(7);
  });
});
