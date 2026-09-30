import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { userApi } from "../api/client";
import { qk } from "../query/keys";
import {
  FAVORITES_KEY,
  FAVORITES_MAX,
  mergeFavorites,
  normalizeFavorites,
  planSignInMerge,
  readLocalFavorites,
  saveFavorites,
  toggleFavorite,
  writeLocalFavorites,
} from "./favorites";

function memoryStorage(initial: Record<string, string> = {}): Storage {
  const m = new Map(Object.entries(initial));
  return {
    get length() {
      return m.size;
    },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, String(v)),
  };
}

describe("normalizeFavorites", () => {
  it("upper-cases, drops malformed symbols and repeats, keeps the first", () => {
    expect(normalizeFavorites(["btc-usdt", "BTC-USDT", " eth-usdt-perp ", "nope", "A-B", "ETH-BTC"])).toEqual(["BTC-USDT", "ETH-USDT-PERP", "ETH-BTC"]);
  });

  it("caps the list like the server", () => {
    const many = Array.from({ length: 120 }, (_, i) => `C${i}-USDT`);
    expect(normalizeFavorites(many)).toHaveLength(FAVORITES_MAX);
  });
});

describe("the sign-in merge", () => {
  it("keeps the account's order and appends the device's new picks", () => {
    expect(mergeFavorites(["ETH-USDT", "BTC-USDT"], ["btc-usdt", "SOL-USDT", "BTC-USDT-PERP"])).toEqual([
      "ETH-USDT",
      "BTC-USDT",
      "SOL-USDT",
      "BTC-USDT-PERP",
    ]);
  });

  it("sends nothing when the device adds nothing new", () => {
    expect(planSignInMerge(["ETH-USDT", "BTC-USDT"], ["BTC-USDT"])).toEqual({ merged: ["ETH-USDT", "BTC-USDT"], put: false });
    expect(planSignInMerge([], [])).toEqual({ merged: [], put: false });
  });

  it("sends the merged list when the device has new picks", () => {
    expect(planSignInMerge([], ["ETH-BTC"])).toEqual({ merged: ["ETH-BTC"], put: true });
    expect(planSignInMerge(["BTC-USDT"], ["ETH-BTC", "BTC-USDT"])).toEqual({ merged: ["BTC-USDT", "ETH-BTC"], put: true });
  });

  it("respects the cap when the account is nearly full", () => {
    const server = Array.from({ length: FAVORITES_MAX }, (_, i) => `C${i}-USDT`);
    expect(planSignInMerge(server, ["NEW-USDT"])).toEqual({ merged: server, put: false });
  });
});

describe("toggleFavorite", () => {
  it("adds at the end and removes", () => {
    expect(toggleFavorite(["BTC-USDT"], "eth-usdt")).toEqual(["BTC-USDT", "ETH-USDT"]);
    expect(toggleFavorite(["BTC-USDT", "ETH-USDT"], "BTC-USDT")).toEqual(["ETH-USDT"]);
  });
});

describe("the device list", () => {
  it("round-trips through storage and survives junk", () => {
    const s = memoryStorage();
    writeLocalFavorites(["BTC-USDT", "ETH-BTC"], s);
    expect(readLocalFavorites(s)).toEqual(["BTC-USDT", "ETH-BTC"]);
    writeLocalFavorites([], s);
    expect(s.getItem(FAVORITES_KEY)).toBeNull();
    expect(readLocalFavorites(memoryStorage({ [FAVORITES_KEY]: "{not json" }))).toEqual([]);
    expect(readLocalFavorites(memoryStorage({ [FAVORITES_KEY]: '{"a":1}' }))).toEqual([]);
    expect(readLocalFavorites(memoryStorage({ [FAVORITES_KEY]: '["btc-usdt", 3, "x"]' }))).toEqual(["BTC-USDT"]);
    expect(readLocalFavorites(null)).toEqual([]);
  });
});

describe("saveFavorites", () => {
  afterEach(() => vi.restoreAllMocks());

  const ok = (symbols: string[]) => ({ data: { symbols, updated_at: "2026-09-30T00:00:00Z" }, response: new Response(null, { status: 200 }) });
  const fail = () => ({ error: { code: "COMMON_UNAVAILABLE", message: "down" }, response: new Response(null, { status: 503 }) });

  it("shows the change at once and takes the server's list", async () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.favorites, { symbols: ["BTC-USDT"], updated_at: null });
    const put = vi.spyOn(userApi, "PUT").mockResolvedValue(ok(["BTC-USDT", "ETH-USDT"]) as never);
    const p = saveFavorites(qc, ["BTC-USDT", "ETH-USDT"], ["BTC-USDT"]);
    expect(qc.getQueryData<{ symbols: string[] }>(qk.favorites)?.symbols).toEqual(["BTC-USDT", "ETH-USDT"]);
    await p;
    expect(put).toHaveBeenCalledOnce();
    expect(qc.getQueryData(qk.favorites)).toEqual({ symbols: ["BTC-USDT", "ETH-USDT"], updated_at: "2026-09-30T00:00:00Z" });
  });

  it("undoes a refused change", async () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.favorites, { symbols: ["BTC-USDT"], updated_at: null });
    vi.spyOn(userApi, "PUT").mockResolvedValue(fail() as never);
    await expect(saveFavorites(qc, [], ["BTC-USDT"])).rejects.toMatchObject({ code: "COMMON_UNAVAILABLE" });
    expect(qc.getQueryData<{ symbols: string[] }>(qk.favorites)?.symbols).toEqual(["BTC-USDT"]);
  });

  it("sends quick toggles in order and keeps the newest list when an older one fails", async () => {
    const qc = new QueryClient();
    qc.setQueryData(qk.favorites, { symbols: [], updated_at: null });
    const sent: string[][] = [];
    vi.spyOn(userApi, "PUT").mockImplementation((async (_path: string, init: { body: { symbols: string[] } }) => {
      sent.push(init.body.symbols);
      return sent.length === 1 ? fail() : ok(init.body.symbols);
    }) as never);
    const first = saveFavorites(qc, ["BTC-USDT"], []);
    const second = saveFavorites(qc, ["BTC-USDT", "ETH-USDT"], ["BTC-USDT"]);
    await expect(first).rejects.toBeTruthy();
    await second;
    expect(sent).toEqual([["BTC-USDT"], ["BTC-USDT", "ETH-USDT"]]);
    expect(qc.getQueryData<{ symbols: string[] }>(qk.favorites)?.symbols).toEqual(["BTC-USDT", "ETH-USDT"]);
  });
});
