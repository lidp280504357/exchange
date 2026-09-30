import { describe, expect, it } from "vitest";
import { applyBalance, applyFill, applyOrder } from "./private";

describe("private pushes into the cache", () => {
  it("updates or adds a balance", () => {
    const page = { balances: [{ account_type: "SPOT", asset: "USDT", available: "10", frozen: "0", total: "10" }] };
    const next = applyBalance(page, { account_type: "SPOT", asset: "USDT", available: "7", frozen: "3", entry_type: "ORDER_FREEZE" })!;
    expect(next.balances[0]).toEqual({ account_type: "SPOT", asset: "USDT", available: "7", frozen: "3", total: "10" });
    const added = applyBalance(next, { account_type: "SPOT", asset: "BTC", available: "0.1", frozen: "0", entry_type: "TRADE_SETTLE" })!;
    expect(added.balances).toHaveLength(2);
    expect(applyBalance(undefined, { account_type: "SPOT", asset: "BTC", available: "1", frozen: "0", entry_type: "" })).toBeUndefined();
  });
  it("moves orders in and out of the active list", () => {
    const page = { items: [{ order_id: "o1", status: "OPEN", symbol: "BTC-USDT", filled_quantity: "0" }], next_cursor: null };
    const partial = applyOrder(page, { order_id: "o1", symbol: "BTC-USDT", status: "PARTIALLY_FILLED", filled_quantity: "0.1" }, "ACTIVE", "")!;
    expect(partial.items[0]).toMatchObject({ status: "PARTIALLY_FILLED", filled_quantity: "0.1" });
    const filled = applyOrder(partial, { order_id: "o1", symbol: "BTC-USDT", status: "FILLED" }, "ACTIVE", "")!;
    expect(filled.items).toHaveLength(0);
    const created = applyOrder(filled, { order_id: "o2", symbol: "BTC-USDT", status: "NEW", side: "BUY", type: "LIMIT", price: "1" }, "ACTIVE", "BTC-USDT")!;
    expect(created.items[0]).toMatchObject({ order_id: "o2", side: "BUY", filled_quantity: "0" });
    const other = applyOrder(filled, { order_id: "o3", symbol: "ETH-USDT", status: "NEW", side: "BUY" }, "ACTIVE", "BTC-USDT")!;
    expect(other.items).toHaveLength(0);
  });
  it("prepends a fill once", () => {
    const page = { items: [], next_cursor: null };
    const fill = { trade_id: "t1", order_id: "o1", symbol: "BTC-USDT", side: "BUY", role: "TAKER", price: "1", quantity: "1", fee_asset: "BTC", fee: "0", executed_at: "" };
    const once = applyFill(page, fill, "")!;
    expect(applyFill(once, fill, "")!.items).toHaveLength(1);
    expect(applyFill(page, fill, "ETH-USDT")!.items).toHaveLength(0);
  });
});
