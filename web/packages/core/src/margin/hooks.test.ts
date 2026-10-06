import { describe, expect, it } from "vitest";
import { leverageOf } from "./hooks";
import type { MarginPair } from "./math";

const pair = (symbol: string, isolated: boolean, leverage: number) => ({ symbol, isolated, leverage }) as MarginPair;

describe("leverageOf", () => {
  it("maps the pairs that take isolated accounts to their leverage, once per answer", () => {
    const data = { items: [pair("BTC-USDT", true, 10), pair("SOL-BTC", false, 3), pair("ETH-USDT", true, 10)] };
    const m = leverageOf(data);
    expect([...m]).toEqual([
      ["BTC-USDT", 10],
      ["ETH-USDT", 10],
    ]);
    // Every row of a list gets the same map; a new answer a new one.
    expect(leverageOf(data)).toBe(m);
    expect(leverageOf({ items: [...data.items] })).not.toBe(m);
  });
});
