import { dec } from "@exchange/core";
import type { AdminSchemas } from "@exchange/core/api/admin";

// Sample data of the margin pages (A55): the margin API is a draft
// (api/admin/admin.yaml, tag margin) that admin-service does not serve
// yet, so the pages show these, typed by the draft, under a banner. The
// parameters are the E0 contract's seed (§9, now
// deploy/instruments/margin.json) with the design's thresholds (§4.4);
// the accounts, liquidations and interest are made up. Once E5 serves
// the API, api.ts reads it instead and this file goes.

export type MarginAsset = AdminSchemas["MarginAsset"];
export type MarginPair = AdminSchemas["MarginPair"];
export type MarginSettings = AdminSchemas["MarginSettings"];
export type MarginAccount = AdminSchemas["MarginAccount"];
export type MarginAccountDetail = AdminSchemas["MarginAccountDetail"];
export type MarginLiquidation = AdminSchemas["MarginLiquidation"];
export type MarginInterestBucket = AdminSchemas["MarginInterestBucket"];

const FLOATING = { base_rate: "0.000005", kink: "0.8", kink_rate: "0.00003", max_rate: "0.0001" };
const SEEDED = "2026-10-06T01:00:00Z";
const HOUR = "2026-10-06T08:00:00Z";

function asset(
  code: string, p: { pool: string; rate: string; haircut: string; lent: string; utilization: string; owed: string; borrowers: number },
  more: Partial<MarginAsset> = {},
): MarginAsset {
  return {
    asset: code, borrowable: true, collateral: true, haircut: p.haircut, pool_cap: p.pool, user_cap: dec.div(p.pool, "10", 8),
    interest_model: "FIXED", fixed_hourly_rate: p.rate, floating: FLOATING, lent: p.lent, pool_available: dec.sub(p.pool, p.lent),
    utilization: p.utilization, hourly_rate: p.rate, rate_hour: HOUR, interest_owed: p.owed, borrowers: p.borrowers, version: 1,
    updated_by: "system", updated_at: SEEDED, pending_approval_id: null, ...more,
  };
}

export const assets: MarginAsset[] = [
  // USDT on the floating curve: 0.000005 + (0.00003 - 0.000005) × 0.42 / 0.8.
  asset("USDT", { pool: "2000000", rate: "0.00001", haircut: "1", lent: "840000", utilization: "0.42", owed: "1286.4", borrowers: 37 }, {
    interest_model: "FLOATING", hourly_rate: "0.000018125", version: 3, updated_by: "ops-a@astras.vip", updated_at: "2026-10-06T03:12:40Z",
  }),
  asset("BTC", { pool: "20", rate: "0.000005", haircut: "0.95", lent: "6.42", utilization: "0.321", owed: "0.0041", borrowers: 12 }),
  asset("ETH", { pool: "400", rate: "0.000005", haircut: "0.95", lent: "151.8", utilization: "0.3795", owed: "0.0972", borrowers: 15 }),
  asset("BNB", { pool: "640", rate: "0.000015", haircut: "0.9", lent: "96", utilization: "0.15", owed: "0.1866", borrowers: 4 }),
  asset("SOL", { pool: "4200", rate: "0.000015", haircut: "0.9", lent: "820", utilization: "0.1952", owed: "1.594", borrowers: 6 }),
  asset("XRP", { pool: "340000", rate: "0.000015", haircut: "0.9", lent: "52000", utilization: "0.1529", owed: "101.1", borrowers: 3 }),
  asset("DOGE", { pool: "5300000", rate: "0.000015", haircut: "0.9", lent: "410000", utilization: "0.0774", owed: "797.3", borrowers: 5 }),
  asset("ADA", { pool: "1900000", rate: "0.000015", haircut: "0.9", lent: "0", utilization: "0", owed: "0", borrowers: 0 }),
  // ASTRA: borrowable with a low pool (§11), and a rate change waiting for a second ADMIN.
  asset("ASTRA", { pool: "100000", rate: "0.00003", haircut: "0.7", lent: "21500", utilization: "0.215", owed: "83.6", borrowers: 2 }, {
    pending_approval_id: "0199b7e2-5c11-7d40-8a3e-6b2f9c0d4e71",
  }),
];

/** The thresholds a pair takes by its leverage when it has none of its own (E0 contract §9). */
const DEFAULTS = [
  { leverage: 3 as const, warn_level: "1.25", liquidation_level: "1.15" },
  { leverage: 5 as const, warn_level: "1.2", liquidation_level: "1.1" },
  { leverage: 10 as const, warn_level: "1.1", liquidation_level: "1.05" },
];

export const settings: MarginSettings = {
  cross: { leverage: 3, warn_level: "1.3", liquidation_level: "1.1" },
  liquidation_fee_rate: "0.02",
  isolated_defaults: DEFAULTS,
  version: 1,
  updated_by: "system",
  updated_at: SEEDED,
  pending_approval_id: null,
};

function pair(symbol: string, leverage: 3 | 5 | 10, accounts: number, more: Partial<MarginPair> = {}): MarginPair {
  const [base = "", quote = ""] = symbol.split("-");
  const levels = DEFAULTS.find((d) => d.leverage === leverage)!;
  return {
    symbol, base, quote, isolated: true, leverage, warn_level: levels.warn_level, liquidation_level: levels.liquidation_level, own_levels: false,
    accounts, version: 1, updated_by: "system", updated_at: SEEDED, pending_approval_id: null, ...more,
  };
}

export const pairs: MarginPair[] = [
  pair("BTC-USDT", 10, 9), pair("ETH-USDT", 10, 11), pair("BNB-USDT", 5, 2), pair("SOL-USDT", 5, 4), pair("XRP-USDT", 5, 1),
  pair("DOGE-USDT", 5, 3), pair("ADA-USDT", 5, 0, { isolated: false, version: 2, updated_by: "ops-a@astras.vip", updated_at: "2026-10-06T04:05:00Z" }),
  pair("ASTRA-USDT", 3, 2, { warn_level: "1.35", liquidation_level: "1.2", own_levels: true, version: 2, updated_by: "ops-a@astras.vip" }),
  pair("ETH-BTC", 3, 1),
];

const UPDATED = "2026-10-06T08:41:07Z";

function account(user: string, a: Partial<MarginAccount>): MarginAccount {
  return {
    user_id: user, account: "MARGIN_CROSS", symbol: null, leverage: 3, status: "NORMAL", margin_level: null, warn_level: "1.3",
    liquidation_level: "1.1", total_asset: "0", total_liability: "0", net_asset: "0", liquidation_price: null, warned_at: null, frozen_by: null,
    frozen_reason: null, frozen_at: null, unpriced: [], updated_at: UPDATED, ...a,
  };
}

const iso = (symbol: string) => {
  const p = pairs.find((x) => x.symbol === symbol)!;
  return { account: "MARGIN_ISOLATED" as const, symbol, leverage: p.leverage, warn_level: p.warn_level, liquidation_level: p.liquidation_level };
};

export const accounts: MarginAccount[] = [
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5c6", {
    ...iso("BTC-USDT"), margin_level: "1.04", total_asset: "62400", total_liability: "60000", net_asset: "2400",
    liquidation_price: "62815", status: "LIQUIDATING", warned_at: "2026-10-06T08:29:51Z",
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5c7", {
    ...iso("ETH-USDT"), margin_level: "1.08", total_asset: "21600", total_liability: "20000", net_asset: "1600",
    liquidation_price: "2353.9", status: "WARNED", warned_at: "2026-10-06T08:36:02Z",
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5c8", {
    margin_level: "1.24", total_asset: "37200", total_liability: "30000", net_asset: "7200", status: "WARNED",
    warned_at: "2026-10-06T07:58:13Z", unpriced: ["ASTRA"],
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5c9", {
    ...iso("ASTRA-USDT"), margin_level: "1.41", total_asset: "4230", total_liability: "3000", net_asset: "1230",
    liquidation_price: "0.8612", status: "FROZEN", frozen_reason: "疑似配合模拟市场事件砸盘，冻结待查", frozen_by: "ops-a@astras.vip",
    frozen_at: "2026-10-06T06:20:44Z",
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5ca", {
    margin_level: "1.67", total_asset: "50100", total_liability: "30000", net_asset: "20100",
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5cb", {
    ...iso("SOL-USDT"), margin_level: "2.31", total_asset: "11550", total_liability: "5000", net_asset: "6550", liquidation_price: "61.4",
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5cc", {
    margin_level: "3.92", total_asset: "19600", total_liability: "5000", net_asset: "14600",
  }),
  account("0199a1d2-3b4c-7d5e-8f60-718293a4b5cd", { total_asset: "8000", net_asset: "8000" }),
];

/** keyOf names an account as the console's addresses do: MARGIN_CROSS, or MARGIN_ISOLATED:<symbol>. */
export const keyOf = (a: Pick<MarginAccount, "account" | "symbol">) => (a.symbol ? `${a.account}:${a.symbol}` : a.account);

function liq(id: string, a: Pick<MarginAccount, "user_id" | "account" | "symbol">, more: Partial<MarginLiquidation>): MarginLiquidation {
  return {
    liquidation_id: id, user_id: a.user_id, account: a.account, symbol: a.symbol, trigger: "AUTO", approval_id: null, status: "COMPLETED",
    margin_level: "0", total_asset: "0", total_liability: "0", repaid: [], remaining: [], fee: "0", insurance_covered: "0", started_at: "",
    completed_at: null, ...more,
  };
}

export const liquidations: MarginLiquidation[] = [
  liq("0199b7c1-0a01-7b22-8d33-4e5f60718201", accounts[0]!, {
    margin_level: "1.05", total_asset: "63000", total_liability: "60000", repaid: [{ asset: "USDT", amount: "36000" }], fee: "720",
    status: "STARTED", started_at: "2026-10-06T08:40:58Z",
  }),
  liq("0199b7c1-0a01-7b22-8d33-4e5f60718202", { user_id: "0199a1d2-3b4c-7d5e-8f60-718293a4b5ce", account: "MARGIN_CROSS", symbol: null }, {
    margin_level: "1.09", total_asset: "21800", total_liability: "20000", repaid: [{ asset: "USDT", amount: "20000" }],
    remaining: [{ asset: "USDT", amount: "1217" }], fee: "433", started_at: "2026-10-05T22:14:03Z", completed_at: "2026-10-05T22:14:09Z",
  }),
  liq("0199b7c1-0a01-7b22-8d33-4e5f60718203", { user_id: "0199a1d2-3b4c-7d5e-8f60-718293a4b5cf", account: "MARGIN_ISOLATED", symbol: "ETH-USDT" }, {
    margin_level: "1.04", total_asset: "10400", total_liability: "10000", repaid: [{ asset: "USDT", amount: "10000" }], fee: "197.4",
    insurance_covered: "327.4", started_at: "2026-10-05T14:02:41Z", completed_at: "2026-10-05T14:02:44Z",
  }),
  liq("0199b7c1-0a01-7b22-8d33-4e5f60718204", { user_id: "0199a1d2-3b4c-7d5e-8f60-718293a4b5d0", account: "MARGIN_ISOLATED", symbol: "DOGE-USDT" }, {
    trigger: "MANUAL", approval_id: "0199b6f0-2b3c-7e4d-9a5b-6c7d8e9f0a12", margin_level: "1.32", total_asset: "6600",
    total_liability: "5000", repaid: [{ asset: "USDT", amount: "5000" }], remaining: [{ asset: "USDT", amount: "1409.2" }], fee: "130.8",
    started_at: "2026-10-04T10:31:15Z", completed_at: "2026-10-04T10:31:19Z",
  }),
  liq("0199b7c1-0a01-7b22-8d33-4e5f60718205", { user_id: "0199a1d2-3b4c-7d5e-8f60-718293a4b5d1", account: "MARGIN_ISOLATED", symbol: "BTC-USDT" }, {
    margin_level: "1.05", total_asset: "105000", total_liability: "100000", repaid: [{ asset: "USDT", amount: "100000" }],
    remaining: [{ asset: "BTC", amount: "0.0369" }], fee: "2088", started_at: "2026-10-03T03:47:22Z", completed_at: "2026-10-03T03:47:31Z",
  }),
];

/** The detail of a sample account: its balances, loans, interest and liquidations, made up to fit its figures. */
export function accountDetail(a: MarginAccount): MarginAccountDetail {
  const [base = "BTC", quote = "USDT"] = (a.symbol ?? "BTC-USDT").split("-");
  const owes = a.margin_level !== null;
  const price = base === "BTC" ? "62600" : base === "ETH" ? "2390" : base === "SOL" ? "141.2" : base === "ASTRA" ? "1.01" : "1";
  const held = owes ? dec.round(dec.div(a.total_asset, dec.mul(price, "0.95"), 8), 4) : "0.12";
  const interest = owes ? "3.6" : "0";
  const principal = owes ? dec.sub(a.total_liability, interest) : "0";
  const balances: MarginAccountDetail["balances"] = [
    {
      asset: base, free: held, locked: "0", borrowed: "0", interest: "0", net: held, price_usdt: price, asset_usdt: a.total_asset, liability_usdt: "0",
      haircut: "0.95", hourly_rate: "0.000005",
    },
    {
      asset: quote, free: owes ? "0" : "500", locked: "0", borrowed: principal, interest, net: owes ? dec.neg(a.total_liability) : "500",
      price_usdt: "1", asset_usdt: owes ? "0" : "500", liability_usdt: a.total_liability, haircut: "1", hourly_rate: "0.000018125",
    },
  ];
  const loans: MarginAccountDetail["loans"] = owes
    ? [{ asset: quote, principal, interest, interest_model: "FLOATING", hourly_rate: "0.000018125", opened_at: "2026-10-05T19:02:11Z", updated_at: HOUR }]
    : [];
  const loanChanges: MarginAccountDetail["loan_changes"] = owes
    ? [
        {
          id: "0199b7d0-3c01-7f10-8b21-9a0c1d2e3f01", asset: quote, kind: "BORROW", amount: principal, principal_part: principal, interest_part: "0",
          reason: "AUTO_BORROW", order_id: "0199b7cf-1a2b-7c3d-8e4f-5a6b7c8d9e01", liquidation_id: null,
          journal_id: "0199b7d0-3c01-7f10-8b21-9a0c1d2e3f11", created_at: "2026-10-05T19:02:11Z",
        },
        {
          id: "0199b7d0-3c01-7f10-8b21-9a0c1d2e3f02", asset: quote, kind: "REPAY", amount: "502.4", principal_part: "500", interest_part: "2.4",
          reason: "USER", order_id: null, liquidation_id: null, journal_id: "0199b7d0-3c01-7f10-8b21-9a0c1d2e3f12", created_at: "2026-10-05T12:40:00Z",
        },
      ]
    : [];
  const charges: MarginAccountDetail["interest"] = owes
    ? ["08", "07", "06", "05"].map((h, i) => ({
        interest_id: `0199b7e0-4d01-7a20-9c31-0b1c2d3e4f0${i}`, asset: quote, principal, interest_model: "FLOATING" as const,
        hourly_rate: "0.000018125", interest: dec.round(dec.mul(principal, "0.000018125"), 8, "up"), hour: `2026-10-06T${h}:00:00Z`,
        journal_id: `0199b7e0-4d01-7a20-9c31-0b1c2d3e4f1${i}`,
      }))
    : [];
  return {
    ...a, balances, loans, loan_changes: loanChanges, interest: charges,
    liquidations: liquidations.filter((l) => l.user_id === a.user_id && l.account === a.account && l.symbol === a.symbol),
  };
}

/** The interest report's sample: the last 7 days of USDT, BTC and ETH. */
export const interest: MarginInterestBucket[] = ["2026-09-30", "2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06"]
  .flatMap((day, i) => [
    bucket(day, "USDT", 310 + i * 22, 284 + i * 19, 900 + i * 55, 760000 + i * 12000, "0.000017", 31 + i, "1"),
    bucket(day, "BTC", 0.0008 + i * 0.00004, 0.0006 + i * 0.00003, 0.003 + i * 0.0002, 6.1 + i * 0.05, "0.000005", 10 + (i % 3), "62600"),
    bucket(day, "ETH", 0.017 + i * 0.0011, 0.014 + i * 0.001, 0.07 + i * 0.004, 140 + i * 2, "0.000005", 12 + (i % 4), "2390"),
  ]);

function bucket(
  day: string, asset: string, charged: number, repaid: number, owed: number, principal: number, rate: string, accounts: number, price: string,
): MarginInterestBucket {
  const fixed = (v: number) => dec.normalize(v.toFixed(8));
  return {
    day, asset, charged: fixed(charged), repaid: fixed(repaid), owed: fixed(owed), principal_avg: fixed(principal), hourly_rate_avg: rate, accounts,
    charged_usdt: fixed(charged * Number(price)), repaid_usdt: fixed(repaid * Number(price)),
  };
}
