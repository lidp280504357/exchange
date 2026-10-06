import { describe, expect, it } from "vitest";
import type { TickerData } from "../ws/types";
import { buildRows, type PairLike } from "../markets/list";
import type { ContractSpec, FuturesDataPoint, FuturesOverviewItem, Liquidation } from "./data";
import { hasFuturesData, isFuturesPeriod, noFuturesData } from "./data";
import {
  filterOverview,
  groupOf,
  openContracts,
  overviewRows,
  overviewTotals,
  parseFuturesSort,
  parseMarginGroup,
  parseOverviewSort,
  sortFuturesRows,
  sortOverview,
} from "./list";
import { LIQUIDATIONS_KEPT, liquidationKey, mergeLiquidations } from "./liquidations";
import { axisTime, chartPoints, formatValue, METRIC_FORMS, METRIC_VALUES, pointTime, windowChange } from "./series";
import { ApiError } from "../api/errors";

// Samples as the test server answered on 2026-10-07 (market.yaml
// FuturesDataPoint, FuturesOverviewItem, Liquidation).

function contract(symbol: string, over: Partial<ContractSpec> = {}): ContractSpec {
  const [base = "", quote = ""] = symbol.split("-");
  const coin = quote === "USD";
  return {
    symbol, type: "PERPETUAL", base_asset: base, quote_asset: quote, index_symbol: `${base}-USDT`, tick_size: base === "BTC" ? "0.1" : "0.01",
    lot_size: coin ? "1" : "0.001", min_quantity: coin ? "1" : "0.001", max_quantity: "1000", min_notional: coin ? "100" : "5", price_band: "0.05",
    max_leverage: 125, risk_tiers: [], funding_interval_hours: 8, interest_rate: "0.0001", funding_cap: "0.0075", impact_notional: "10000",
    maker_fee_rate: "0.0002", taker_fee_rate: "0.0005", status: "TRADING", margin_type: coin ? "COIN" : "USDT", settle_asset: coin ? base : "USDT",
    contract_size: coin ? (base === "BTC" ? "100" : "10") : "0", reference_symbol: base === "ASTRA" ? null : coin ? `${base}USD_PERP` : `${base}USDT`, ...over,
  } as ContractSpec;
}

function item(symbol: string, over: Partial<FuturesOverviewItem> = {}): FuturesOverviewItem {
  return {
    symbol, mark_price: "85757.5", index_price: "85796.40282609", funding_rate: "-0.00000808", next_funding_time: "2026-10-07T00:00:00Z",
    open_interest: "96106.962", open_interest_value: "8241892793.72", change: "0.00483685", quote_volume: "9526117101.79", futures_data: true, ...over,
  };
}

function liq(at: string, over: Partial<Liquidation> = {}): Liquidation {
  return { symbol: "BTC-USDT-PERP", position_side: "SHORT", price: "86100.1", average_price: "85778.5", quantity: "0.005", value_usd: "428.8925", traded_at: at, ...over };
}

describe("futures data", () => {
  it("reads periods and knows a contract without data", () => {
    expect(isFuturesPeriod("4h")).toBe(true);
    expect(isFuturesPeriod("30m")).toBe(false);
    expect(isFuturesPeriod(null)).toBe(false);
    expect(noFuturesData(new ApiError(404, "MARKET_NO_FUTURES_DATA", "no"))).toBe(true);
    expect(noFuturesData(new ApiError(404, "COMMON_NOT_FOUND", "no"))).toBe(false);
    expect(noFuturesData(new Error("x"))).toBe(false);
    expect(hasFuturesData(contract("BTC-USDT-PERP"))).toBe(true);
    expect(hasFuturesData(contract("ASTRA-USDT-PERP"))).toBe(false);
    expect(hasFuturesData(undefined)).toBe(false);
  });
});

describe("series", () => {
  const points: FuturesDataPoint[] = [
    { time: "2026-10-06T18:05:00Z", values: { open_interest: "96174.19", open_interest_value: "8244413857.064557" } },
    { time: "2026-10-06T18:00:00Z", values: { open_interest: "96257.545", open_interest_value: "8246707733.080103" } },
    { time: "not a time", values: { open_interest: "1" } },
    { time: "2026-10-06T18:10:00Z", values: { open_interest: "96143.788", open_interest_value: "bad" } },
    { time: "2026-10-06T18:10:00Z", values: { open_interest: "96143.789" } },
  ];

  it("turns points into chart points, oldest first, one per time", () => {
    const c = chartPoints(points);
    expect(c.map((p) => p.t)).toEqual([Date.parse("2026-10-06T18:00:00Z"), Date.parse("2026-10-06T18:05:00Z"), Date.parse("2026-10-06T18:10:00Z")]);
    expect(c[0]!.v.open_interest).toBeCloseTo(96257.545);
    expect(c[0]!.raw.open_interest_value).toBe("8246707733.080103");
    // The later of two points at one time stays; an invalid value is left out.
    expect(c[2]!.raw).toEqual({ open_interest: "96143.789" });
  });

  it("moves over the window as an exact fraction", () => {
    const c = chartPoints(points);
    expect(windowChange(c, "open_interest")).toBe("-0.00118179");
    expect(windowChange(c.slice(0, 1), "open_interest")).toBeNull();
    expect(windowChange(chartPoints([{ time: "2026-10-06T18:00:00Z", values: { x: "0" } }, { time: "2026-10-06T18:05:00Z", values: { x: "1" } }]), "x")).toBeNull();
  });

  it("gives every statistic a form whose values it lists", () => {
    for (const [metric, form] of Object.entries(METRIC_FORMS)) {
      const keys = METRIC_VALUES[metric as keyof typeof METRIC_VALUES].map((v) => v.key);
      const drawn = form.kind === "line" || form.kind === "columns" ? [form.key] : [form.up, form.down];
      for (const k of drawn) expect(keys, metric).toContain(k);
    }
  });

  it("formats the values by unit", () => {
    const ctx = { priceDecimals: 1, qtyDecimals: 3, locale: "zh-CN" };
    expect(formatValue("qty", "96143.788", ctx)).toBe("96,143.788");
    expect(formatValue("qty", "12570113", { ...ctx, qtyDecimals: 0 })).toBe("1257.01万");
    expect(formatValue("qty", "12570113", { ...ctx, locale: "en" })).toBe("12.57M");
    expect(formatValue("usd", "8241892793.72", { ...ctx, locale: "en" })).toBe("8.24B");
    expect(formatValue("share", "0.7689", ctx)).toBe("76.89%");
    expect(formatValue("ratio", "3.3547", ctx)).toBe("3.35");
    expect(formatValue("price", "-46.20106407", ctx)).toBe("-46.2");
    expect(formatValue("rate", "0.00002245", ctx)).toBe("+0.0022%");
    expect(formatValue("rate", "-0.0005", ctx)).toBe("-0.0500%");
    expect(formatValue("rate", undefined, ctx)).toBe("—");
  });

  it("labels times by period", () => {
    const t = Date.parse("2026-10-06T16:00:00Z");
    expect(axisTime(t, "5m", "zh-CN", "UTC")).toBe("16:00");
    expect(axisTime(t, "4h", "zh-CN", "UTC")).toBe("10-06 16:00");
    expect(axisTime(t, "funding", "zh-CN", "UTC")).toBe("10-06 16:00");
    expect(axisTime(t, "1d", "zh-CN", "UTC")).toBe("10-06");
    expect(pointTime(t, "1h", "zh-CN", "UTC")).toBe("2026-10-06 16:00");
    expect(pointTime(t, "1d", "zh-CN", "UTC")).toBe("2026-10-06");
  });
});

describe("liquidations", () => {
  it("merges pushes into the list once, newest first", () => {
    const a = liq("2026-10-06T18:03:17.209Z");
    const b = liq("2026-10-06T18:03:14.099Z", { quantity: "0.019" });
    const list = mergeLiquidations([], [a, b]);
    expect(list).toEqual([a, b]);
    // The same order pushed again (another object, the same fields) changes nothing.
    expect(mergeLiquidations(list, [{ ...a }])).toBe(list);
    const c = liq("2026-10-06T18:05:00Z", { position_side: "LONG" });
    expect(mergeLiquidations(list, [c])).toEqual([c, a, b]);
    // An older one from a reload goes in its place.
    const old = liq("2026-10-06T18:01:08.082Z");
    expect(mergeLiquidations([c, a, b], [old]).at(-1)).toBe(old);
    expect(liquidationKey(a)).not.toBe(liquidationKey(b));
  });

  it("keeps at most the newest", () => {
    const many = Array.from({ length: LIQUIDATIONS_KEPT + 5 }, (_, i) => liq(new Date(Date.UTC(2026, 9, 6, 18, 0, i)).toISOString()));
    const list = mergeLiquidations([], many);
    expect(list).toHaveLength(LIQUIDATIONS_KEPT);
    expect(list[0]!.traded_at).toBe(many.at(-1)!.traded_at);
    expect(mergeLiquidations(list, [], 10)).toHaveLength(10);
  });
});

describe("contract lists", () => {
  const all = [contract("BTC-USDT-PERP"), contract("BTC-USD-PERP"), contract("ETH-USD-PERP"), contract("ASTRA-USDT-PERP")];

  it("groups by margin type and leaves out the contracts still PREPARE", () => {
    expect(parseMarginGroup("coin")).toBe("coin");
    expect(parseMarginGroup("x")).toBe("usdt");
    expect(groupOf(all[1])).toBe("coin");
    expect(groupOf({})).toBe("usdt");
    const listed = [...all, contract("SOL-USDT-PERP", { status: "PREPARE" }), contract("SOL-USD-PERP", { status: "PREPARE" })];
    expect(openContracts(listed).map((c) => c.symbol)).toEqual(all.map((c) => c.symbol));
  });

  it("sorts the futures category by open interest value and funding", () => {
    const pairs: PairLike[] = [
      { symbol: "BTC-USDT", base_asset: "BTC", quote_asset: "USDT", base_name: "Bitcoin", rank: 1, categories: [], price_decimals: 2, status: "TRADING", listed_at: "" },
      { symbol: "ETH-USDT", base_asset: "ETH", quote_asset: "USDT", base_name: "Ethereum", rank: 2, categories: [], price_decimals: 2, status: "TRADING", listed_at: "" },
    ];
    const rows = buildRows(pairs, all).filter((r) => r.kind === "perp");
    const overview = new Map([
      ["BTC-USDT-PERP", item("BTC-USDT-PERP")],
      ["BTC-USD-PERP", item("BTC-USD-PERP", { open_interest_value: "1257011300", funding_rate: "-0.00000495" })],
      ["ETH-USD-PERP", item("ETH-USD-PERP", { open_interest_value: "363165340", funding_rate: "0.00006727" })],
      ["ASTRA-USDT-PERP", item("ASTRA-USDT-PERP", { open_interest_value: null, funding_rate: "0.0001" })],
    ]);
    const of = (s: string) => overview.get(s);
    const tickerOf = (_: string): TickerData | undefined => undefined;
    expect(parseFuturesSort("oi", "desc")).toEqual({ key: "oi", desc: true });
    expect(parseFuturesSort("funding", "asc")).toEqual({ key: "funding", desc: false });
    expect(parseFuturesSort("change", null)).toEqual({ key: "change", desc: true });
    expect(parseFuturesSort("nope", null)).toBeNull();
    // Without a value (ASTRA's open interest), last in either direction.
    expect(sortFuturesRows(rows, tickerOf, of, { key: "oi", desc: true }).map((r) => r.symbol)).toEqual(["BTC-USDT-PERP", "BTC-USD-PERP", "ETH-USD-PERP", "ASTRA-USDT-PERP"]);
    expect(sortFuturesRows(rows, tickerOf, of, { key: "oi", desc: false }).map((r) => r.symbol)).toEqual(["ETH-USD-PERP", "BTC-USD-PERP", "BTC-USDT-PERP", "ASTRA-USDT-PERP"]);
    expect(sortFuturesRows(rows, tickerOf, of, { key: "funding", desc: true }).map((r) => r.symbol)).toEqual(["ASTRA-USDT-PERP", "ETH-USD-PERP", "BTC-USD-PERP", "BTC-USDT-PERP"]);
    // Other keys sort as the market list does.
    expect(sortFuturesRows(rows, tickerOf, of, null).map((r) => r.symbol)).toEqual(["BTC-USDT-PERP", "BTC-USD-PERP", "ETH-USD-PERP", "ASTRA-USDT-PERP"]);
  });

  it("builds, filters, sorts and sums the overview", () => {
    const items = [
      item("BTC-USDT-PERP"),
      item("BTC-USD-PERP", { open_interest: "12570113", open_interest_value: "1257011300", funding_rate: "-0.00000495", quote_volume: "769492900" }),
      item("ETH-USD-PERP", { open_interest_value: "363165340", funding_rate: "0.00006727", quote_volume: "294673130" }),
      item("ASTRA-USDT-PERP", { open_interest: null, open_interest_value: null, funding_rate: "0.0001", quote_volume: "2456077.5423", futures_data: false }),
      item("GONE-USDT-PERP"),
    ];
    const rows = overviewRows(items, all, (base) => (base === "BTC" ? ["比特币"] : []));
    expect(rows.map((r) => r.symbol)).toEqual(["BTC-USDT-PERP", "BTC-USD-PERP", "ETH-USD-PERP", "ASTRA-USDT-PERP"]);
    expect(rows[1]).toMatchObject({ group: "coin", base: "BTC", quote: "USD", settle: "BTC", priceDecimals: 1, qtyDecimals: 0 });
    expect(filterOverview(rows, "usdt").map((r) => r.symbol)).toEqual(["BTC-USDT-PERP", "ASTRA-USDT-PERP"]);
    expect(filterOverview(rows, "coin", "比特币").map((r) => r.symbol)).toEqual(["BTC-USD-PERP"]);
    expect(filterOverview(rows, "usdt", "astra usdt").map((r) => r.symbol)).toEqual(["ASTRA-USDT-PERP"]);
    expect(parseOverviewSort(null, null)).toEqual({ key: "oi", desc: true });
    expect(parseOverviewSort("funding", "asc")).toEqual({ key: "funding", desc: false });
    expect(sortOverview(rows, { key: "funding", desc: false }).map((r) => r.symbol)).toEqual(["BTC-USDT-PERP", "BTC-USD-PERP", "ETH-USD-PERP", "ASTRA-USDT-PERP"]);
    expect(sortOverview(rows, { key: "oi", desc: true }).at(-1)!.symbol).toBe("ASTRA-USDT-PERP");
    expect(sortOverview(rows, { key: "symbol", desc: false })[0]!.symbol).toBe("ASTRA-USDT-PERP");
    expect(overviewTotals(rows)).toEqual({
      contracts: 4,
      openInterestValue: "9862069433.72",
      volume: "10592739209.3323",
      positive: 2,
      negative: 2,
    });
  });
});
