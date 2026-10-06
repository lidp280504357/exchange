import { afterEach, describe, expect, it, vi } from "vitest";
import { marketApi } from "../api/client";
import { fetchContracts } from "./pairs";

describe("fetchContracts", () => {
  afterEach(() => vi.restoreAllMocks());

  it("leaves out the contracts still PREPARE", async () => {
    vi.spyOn(marketApi, "GET").mockResolvedValue({
      data: {
        contracts: [
          { symbol: "BTC-USDT-PERP", status: "TRADING" },
          { symbol: "SOL-USDT-PERP", status: "PREPARE" },
          { symbol: "ETH-USDT-PERP", status: "CANCEL_ONLY" },
        ],
      },
      response: new Response(),
    } as never);
    const list = await fetchContracts();
    expect(list.contracts.map((c) => c.symbol)).toEqual(["BTC-USDT-PERP", "ETH-USDT-PERP"]);
  });
});
