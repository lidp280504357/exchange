import { describe, expect, it } from "vitest";
import { ALL_OPEN } from "./products";
import { futuresLineOf, hasWindDown, windDownOf } from "./windDown";

describe("winding down a closed product line", () => {
  it("keeps what a closed line still holds", () => {
    const x = {
      positions: [{ symbol: "BTC-USD-PERP" }, { symbol: "BTC-USDT-PERP" }],
      orders: [{ symbol: "ETH-USD-PERP" }],
      spotOrders: [{ symbol: "BTC-USDT" }],
      balances: [
        { account_type: "FUTURES", asset: "BTC", total: "0.01" },
        { account_type: "FUTURES", asset: "ETH", total: "0" },
        { account_type: "FUTURES", asset: "USDT", total: "12" },
        { account_type: "SPOT", asset: "BTC", total: "1" },
      ],
      margin: [
        { account: "MARGIN_CROSS", balances: [{ asset: "USDT", free: "0", locked: "0", borrowed: "5", interest: "0.01", net: "-5.01" }] },
        { account: "MARGIN_ISOLATED", symbol: "ETH-USDT", balances: [{ asset: "ETH", free: "0", locked: "0", borrowed: "0", interest: "0", net: "0" }] },
      ],
    };
    expect(futuresLineOf("USDT")).toBe("usdt_m");
    expect(futuresLineOf("ASTRA")).toBe("coin_m");
    expect(hasWindDown(windDownOf(x, ALL_OPEN))).toBe(false);
    const coin = windDownOf(x, { ...ALL_OPEN, coin_m: false });
    expect(coin.positions.map((p) => p.symbol)).toEqual(["BTC-USD-PERP"]);
    expect(coin.orders).toHaveLength(1);
    expect(coin.spotOrders).toHaveLength(0);
    expect(coin.balances.map((b) => b.asset)).toEqual(["BTC"]);
    expect(coin.margin).toHaveLength(0);
    const spot = windDownOf(x, { ...ALL_OPEN, spot: false });
    expect(spot.spotOrders).toHaveLength(1);
    // Margin closes with spot: the account that owes is left to repay, the empty one is not.
    expect(spot.margin.map((a) => a.account)).toEqual(["MARGIN_CROSS"]);
    expect(hasWindDown(windDownOf({ margin: x.margin }, { ...ALL_OPEN, spot: false }))).toBe(true);
  });
});
