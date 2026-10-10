import { adminApi, adminData, type AdminSchemas } from "@exchange/core/api/admin";
import { useQuery } from "@tanstack/react-query";
import { kindParam } from "../../kit/kinds";
import type { Page } from "../../kit/lists";

// The margin pages' data (design 2026-10-06 §8, E5): admin-service's
// /admin/v1/margin/*, which reads margin-service's terms and accounts and
// the read models' liquidations and interest.

export type MarginAsset = AdminSchemas["MarginAsset"];
export type MarginPair = AdminSchemas["MarginPair"];
export type MarginSettings = AdminSchemas["MarginSettings"];
export type MarginAccount = AdminSchemas["MarginAccount"];
export type MarginAccountDetail = AdminSchemas["MarginAccountDetail"];
export type MarginLiquidation = AdminSchemas["MarginLiquidation"];
export type MarginInterestBucket = AdminSchemas["MarginInterestBucket"];

export const marginKey = ["admin", "margin"];

/** keyOf is an account's key in the API's paths: MARGIN_CROSS, or MARGIN_ISOLATED:<symbol>. */
export const keyOf = (a: Pick<MarginAccount, "account" | "symbol">) => (a.account === "MARGIN_ISOLATED" ? `MARGIN_ISOLATED:${a.symbol}` : a.account);

export function useMarginAssets() {
  return useQuery({
    queryKey: [...marginKey, "assets"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/margin/assets")).items,
  });
}

export function useMarginPairs() {
  return useQuery({
    queryKey: [...marginKey, "pairs"],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/margin/pairs")).items,
  });
}

export function useMarginSettings() {
  return useQuery({ queryKey: [...marginKey, "settings"], queryFn: async () => adminData(await adminApi.GET("/admin/v1/margin/settings")) });
}

/** kind is the accounts' (L1): none, the humans'; BOT, TEST, SYSTEM or ALL. */
export type AccountQuery = { account?: MarginAccount["account"]; symbol?: string; user_id?: string; kind?: string };

/** useMarginAccounts asks the server for the accounts of a scope, riskiest first (the status tabs filter what comes back). */
export function useMarginAccounts(q: AccountQuery) {
  return useQuery({
    queryKey: [...marginKey, "accounts", q],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/margin/accounts", { params: { query: { ...q, kind: kindParam(q.kind) as never } } })),
  });
}

/** useMarginAccount reads an account by its holder and key (MARGIN_CROSS, or MARGIN_ISOLATED:<symbol>). */
export function useMarginAccount(userId: string, key: string) {
  return useQuery({
    queryKey: [...marginKey, "account", userId, key],
    queryFn: async () =>
      adminData(await adminApi.GET("/admin/v1/margin/accounts/{user_id}/{account}", { params: { path: { user_id: userId, account: key } } })),
  });
}

/** kind is the accounts' (L1): none, the humans'; BOT, TEST, SYSTEM or ALL. */
export type LiquidationQuery = {
  days: number; account?: MarginAccount["account"]; symbol?: string; trigger?: "AUTO" | "MANUAL"; user_id?: string; kind?: string;
};

/** liquidationsPage is a page of liquidations, newest first. */
export async function liquidationsPage(q: LiquidationQuery, cursor: string | undefined): Promise<Page<MarginLiquidation>> {
  return adminData(await adminApi.GET("/admin/v1/margin/liquidations", { params: { query: { ...q, kind: kindParam(q.kind) as never, cursor } } }));
}

/** kind as a LiquidationQuery's. */
export type InterestQuery = { days: number; bucket: "day" | "week" | "month"; asset?: string; kind?: string };

export function useMarginInterest(q: InterestQuery) {
  return useQuery({
    queryKey: [...marginKey, "interest", q],
    queryFn: async () => adminData(await adminApi.GET("/admin/v1/margin/interest", { params: { query: { ...q, kind: kindParam(q.kind) as never } } })),
  });
}
