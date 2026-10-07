import { afterEach, describe, expect, it, vi } from "vitest";
import { derivativesApi } from "../api/client";
import { closeAtMarket } from "./derivatives";

const order = (quantity: string, filled: string, status = "CANCELED") => ({ order_id: `o-${quantity}`, quantity, filled_quantity: filled, status });

describe("closeAtMarket", () => {
  afterEach(() => vi.restoreAllMocks());

  it("sends what is left until the position is closed, three orders at most (B129)", async () => {
    // The user's 5 BTC close of 2026-10-07: 1.567 of 5, then the rest.
    const fills = [order("5", "1.567"), order("3.433", "3.433", "FILLED")];
    const post = vi.spyOn(derivativesApi, "POST").mockImplementation((async () => ({ data: fills.shift(), response: new Response(null, { status: 201 }) })) as never);
    const res = await closeAtMarket({ symbol: "BTC-USDT-PERP", quantity: "-5", position_side: "BOTH" });
    expect(res).toEqual({ closed: "5", left: "0", orders: 2, status: "FILLED", reason: "" });
    const bodies = post.mock.calls.map((c) => (c[1] as { body: { side: string; quantity: string; reduce_only?: boolean } }).body);
    expect(bodies.map((b) => [b.side, b.quantity, b.reduce_only])).toEqual([["BUY", "5", true], ["BUY", "3.433", true]]);
  });

  it("stops once an order fills nothing, and after three", async () => {
    vi.spyOn(derivativesApi, "POST").mockResolvedValueOnce({ data: order("2", "0"), response: new Response(null, { status: 201 }) } as never);
    expect(await closeAtMarket({ symbol: "BTC-USD-PERP", quantity: "2", position_side: "LONG" })).toEqual({ closed: "0", left: "2", orders: 1, status: "CANCELED", reason: "" });
    const fills = [order("9", "1"), order("8", "1"), order("7", "1")];
    vi.spyOn(derivativesApi, "POST").mockImplementation((async () => ({ data: fills.shift(), response: new Response(null, { status: 201 }) })) as never);
    expect(await closeAtMarket({ symbol: "BTC-USD-PERP", quantity: "9", position_side: "LONG" })).toMatchObject({ closed: "3", left: "6", orders: 3 });
    // A refused order says why.
    vi.spyOn(derivativesApi, "POST").mockResolvedValueOnce({
      data: { ...order("2", "0", "REJECTED"), reject_reason: "DERIV_REDUCE_ONLY_REJECTED" }, response: new Response(null, { status: 201 }),
    } as never);
    // A refusal is not tried again: one order (review R14').
    expect(await closeAtMarket({ symbol: "BTC-USD-PERP", quantity: "2", position_side: "LONG" })).toMatchObject({
      closed: "0",
      status: "REJECTED",
      reason: "DERIV_REDUCE_ONLY_REJECTED",
      orders: 1,
    });
  });
});
