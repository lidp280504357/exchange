import { afterEach, describe, expect, it, vi } from "vitest";
import { marketApi } from "../api/client";
import { fetchSparkline } from "./sparkline";

describe("fetchSparkline", () => {
  afterEach(() => vi.restoreAllMocks());

  it("asks for the rows that ask together in one request per range", async () => {
    const get = vi.spyOn(marketApi, "GET").mockImplementation((async (_path: string, init: { params: { query: { symbols: string; range: string } } }) => {
      const { symbols, range } = init.params.query;
      const lines = Object.fromEntries(
        symbols
          .split(",")
          .filter((s) => s !== "NOPE-USDT")
          .map((s) => [s, range === "24h" ? ["1", "2"] : ["3", "4", "5"]]),
      );
      return { data: { range, interval: "1h", sparklines: lines }, response: new Response() };
    }) as never);
    const [btc, eth, nope, day] = await Promise.all([
      fetchSparkline("BTC-USDT"),
      fetchSparkline("ETH-USDT"),
      fetchSparkline("NOPE-USDT"),
      fetchSparkline("BTC-USDT", "24h"),
    ]);
    expect(btc).toEqual(["3", "4", "5"]);
    expect(eth).toEqual(["3", "4", "5"]);
    expect(nope).toEqual([]);
    expect(day).toEqual(["1", "2"]);
    expect(get).toHaveBeenCalledTimes(2);
    expect(get.mock.calls.map((c) => (c[1] as { params: { query: { symbols: string } } }).params.query.symbols).sort()).toEqual([
      "BTC-USDT",
      "BTC-USDT,ETH-USDT,NOPE-USDT",
    ]);
  });

  it("fails every row of a failed request", async () => {
    vi.spyOn(marketApi, "GET").mockRejectedValue(new Error("offline"));
    await expect(Promise.all([fetchSparkline("BTC-USDT"), fetchSparkline("ETH-USDT")])).rejects.toThrow("offline");
  });
});
