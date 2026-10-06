import { describe, expect, it } from "vitest";
import type { MarginAccount } from "../margin/math";
import { applyBalance, applyFill, applyMarginAccount, applyOrder, holdingsOf } from "./private";

const account = (over: Partial<MarginAccount>): MarginAccount => ({
  account: "MARGIN_CROSS", symbol: null, leverage: 5, status: "NORMAL", margin_level: null, warn_level: "1.3", liquidation_level: "1.1",
  total_asset: "100", total_liability: "0", net_asset: "100", liquidation_price: null,
  balances: [{ asset: "USDT", free: "100", locked: "0", borrowed: "0", interest: "0", net: "100" }], updated_at: "2026-10-06T07:00:00Z",
  ...over,
});

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
  it("replaces a margin account with its push, telling holdings from valuations", () => {
    const btc = account({ account: "MARGIN_ISOLATED", symbol: "BTC-USDT", leverage: 10 });
    const page = { cross: account({}), isolated: [btc] };
    // A borrow: the cross account owes USDT now.
    const borrowed = account({
      margin_level: "2", total_asset: "200", total_liability: "100", net_asset: "100", updated_at: "2026-10-06T07:00:01Z",
      balances: [{ asset: "USDT", free: "200", locked: "0", borrowed: "100", interest: "0.01", net: "99.99" }],
    });
    const after = applyMarginAccount(page, borrowed);
    expect(after.held).toBe(true);
    expect(after.page!.cross.margin_level).toBe("2");
    expect(after.page!.isolated).toEqual([btc]);
    // Only its valuation moved: the holdings are the same.
    const moved = applyMarginAccount(after.page, { ...borrowed, total_asset: "190", margin_level: "1.9", updated_at: "2026-10-06T07:00:02Z" });
    expect(moved).toMatchObject({ held: false, page: { cross: { margin_level: "1.9" } } });
    // A push older than the cached account (a poll answered after it) is left out.
    const late = applyMarginAccount(moved.page, { ...borrowed, margin_level: "5", updated_at: "2026-10-06T07:00:01.5Z" });
    expect(late).toEqual({ page: moved.page, held: false });
    // An isolated account replaces its pair's; a new pair's joins the list.
    const eth = account({ account: "MARGIN_ISOLATED", symbol: "ETH-USDT", updated_at: "2026-10-06T07:00:03Z" });
    const joined = applyMarginAccount(moved.page, eth);
    expect(joined.held).toBe(true);
    expect(joined.page!.isolated.map((a) => a.symbol)).toEqual(["BTC-USDT", "ETH-USDT"]);
    const btcNow = applyMarginAccount(joined.page, { ...btc, status: "WARNED", updated_at: "2026-10-06T07:00:04Z" });
    expect(btcNow.page!.isolated.map((a) => a.status)).toEqual(["WARNED", "NORMAL"]);
    // Nothing cached: nothing to put it in; against its last push only a
    // change of holdings counts, and without one it may have changed anything.
    expect(applyMarginAccount(undefined, eth)).toEqual({ page: undefined, held: true });
    const revalued = { ...eth, total_asset: "99", updated_at: "2026-10-06T07:00:05Z" };
    expect(applyMarginAccount(undefined, revalued, holdingsOf(eth))).toEqual({ page: undefined, held: false });
    const owing = { ...eth, balances: [{ asset: "USDT", free: "100", locked: "0", borrowed: "5", interest: "0", net: "95" }] };
    expect(applyMarginAccount(undefined, owing, holdingsOf(eth))).toEqual({ page: undefined, held: true });
    // The last push decides over the cached account.
    expect(applyMarginAccount(joined.page, revalued, holdingsOf(eth)).held).toBe(false);
    // An isolated account without its pair is left out.
    expect(applyMarginAccount(joined.page, { ...eth, symbol: null })).toEqual({ page: joined.page, held: false });
  });
  it("prepends a fill once", () => {
    const page = { items: [], next_cursor: null };
    const fill = { trade_id: "t1", order_id: "o1", symbol: "BTC-USDT", side: "BUY", role: "TAKER", price: "1", quantity: "1", fee_asset: "BTC", fee: "0", executed_at: "" };
    const once = applyFill(page, fill, "")!;
    expect(applyFill(once, fill, "")!.items).toHaveLength(1);
    expect(applyFill(page, fill, "ETH-USDT")!.items).toHaveLength(0);
  });
});
