import { describe, expect, it } from "vitest";
import { GUARDS, guardProgress, isNew, NEW_FOR_MS, ordersPath, ordersTabOf, splitShares } from "./logic";

describe("guardProgress", () => {
  it("counts what is on and suggests the first that is not, authenticator first", () => {
    expect(guardProgress({ totp: false, phone: false, email: true, antiPhishing: false })).toEqual({ done: 1, max: 4, next: "totp" });
    expect(guardProgress({ totp: true, phone: false, email: true, antiPhishing: true })).toEqual({ done: 3, max: 4, next: "phone" });
    expect(guardProgress({ totp: true, phone: true, email: true, antiPhishing: true })).toEqual({ done: 4, max: GUARDS.length, next: null });
  });
});

describe("ordersPath", () => {
  it("opens the spot pair traded last, skipping contracts", () => {
    expect(ordersPath(["BTC-USDT-PERP", "ETH-USDT", "BTC-USDT"], "history")).toBe("/trade/ETH-USDT?orders=history");
  });

  it("falls back to the default pair", () => {
    expect(ordersPath([], "open")).toBe("/trade/BTC-USDT?orders=open");
    expect(ordersPath(["ETH-USDT-PERP"], "fills")).toBe("/trade/BTC-USDT?orders=fills");
  });

  it("reads back as the tab it names", () => {
    expect(ordersTabOf("history")).toBe("history");
    expect(ordersTabOf("fills")).toBe("fills");
    expect(ordersTabOf(null)).toBe("open");
    expect(ordersTabOf("everything")).toBe("open");
  });
});

describe("isNew", () => {
  const day = Date.parse("2026-10-01");
  it("marks announcements of the last three days", () => {
    expect(isNew("2026-10-01", day + 3600_000)).toBe(true);
    expect(isNew("2026-09-29", day)).toBe(true);
    expect(isNew("2026-09-28", day)).toBe(false); // exactly three days old
    expect(isNew("2026-09-01", day + NEW_FOR_MS)).toBe(false);
  });

  it("ignores dates in the future and unreadable ones", () => {
    expect(isNew("2026-10-05", day)).toBe(false);
    expect(isNew("", day)).toBe(false);
  });
});

describe("splitShares", () => {
  it("divides the total between the accounts", () => {
    expect(splitShares("750", "250")).toEqual({ spot: 0.75, futures: 0.25, margin: 0 });
    expect(splitShares("10000", "0")).toEqual({ spot: 1, futures: 0, margin: 0 });
    expect(splitShares("500", "250", "250")).toEqual({ spot: 0.5, futures: 0.25, margin: 0.25 });
  });

  it("gives an account owing more than it holds no share", () => {
    expect(splitShares("100", "0", "-20")).toEqual({ spot: 1, futures: 0, margin: 0 });
  });

  it("is empty without funds", () => {
    expect(splitShares("0", "0")).toEqual({ spot: 0, futures: 0, margin: 0 });
  });
});
